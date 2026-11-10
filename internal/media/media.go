// Package media gets the bytes of an image, document or video a tool uploads
// to LinkedIn: from inline base64 or from a public https URL. Everything it
// returns has been size-limited and checked against the file's real type, and a
// URL is fetched only after SSRF checks, so a caller cannot make this server
// read an internal address.
package media

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register the GIF decoder for image.DecodeConfig
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	_ "image/png"  // register the PNG decoder for image.DecodeConfig
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Kind is the type of file a tool uploads.
type Kind int

const (
	Image Kind = iota
	Document
	Video
)

// Size limits. LinkedIn accepts larger files; these keep one tool call bounded
// in this server's memory.
const (
	// MaxInlineBytes caps a base64 payload after decoding.
	MaxInlineBytes = 10 << 20
	// MinVideoBytes is LinkedIn's minimum video size (75 KB).
	MinVideoBytes = 75 << 10
	// MaxImagePixels is LinkedIn's limit on an image's pixel count.
	MaxImagePixels = 36152320
)

var (
	// ErrTooLarge means a file is over its size limit.
	ErrTooLarge = errors.New("the file is too large")
	// ErrType means a file is not an allowed type for its use.
	ErrType = errors.New("the file is not an allowed type")
	// ErrBlocked means a URL is not a public https address that may be fetched.
	ErrBlocked = errors.New("only public https URLs on port 443 may be fetched")
)

// FetchLimit is the largest file fetched from a URL for kind.
func FetchLimit(kind Kind) int64 {
	switch kind {
	case Document:
		return 50 << 20
	case Video:
		return 100 << 20
	}
	return 10 << 20
}

// DecodeBase64 decodes a base64 payload (padded or not, newlines allowed),
// refusing one that would decode to more than MaxInlineBytes.
func DecodeBase64(text string) ([]byte, error) {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
	if len(text) > MaxInlineBytes*4/3+8 {
		return nil, ErrTooLarge
	}
	text = strings.TrimRight(text, "=")
	data, err := base64.RawStdEncoding.DecodeString(text)
	if err != nil {
		return nil, errors.New("the base64 data is not valid")
	}
	if len(data) > MaxInlineBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

// Validate checks data against its kind by file signature, never by a
// caller-supplied name or content type. Images must also be under LinkedIn's
// pixel limit and videos over its minimum size.
func Validate(kind Kind, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: it is empty", ErrType)
	}
	switch kind {
	case Image:
		if !bytes.HasPrefix(data, []byte("\xFF\xD8\xFF")) && !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) &&
			!bytes.HasPrefix(data, []byte("GIF87a")) && !bytes.HasPrefix(data, []byte("GIF89a")) {
			return fmt.Errorf("%w: images must be JPEG, PNG or GIF", ErrType)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%w: the image could not be read", ErrType)
		}
		if int64(config.Width)*int64(config.Height) >= MaxImagePixels {
			return fmt.Errorf("%w: the image has too many pixels", ErrTooLarge)
		}
	case Document:
		if !bytes.HasPrefix(data, []byte("%PDF-")) && !bytes.HasPrefix(data, []byte("PK\x03\x04")) && !bytes.HasPrefix(data, []byte("\xD0\xCF\x11\xE0")) {
			return fmt.Errorf("%w: documents must be PDF, PPT, PPTX, DOC or DOCX", ErrType)
		}
	case Video:
		if len(data) < 12 || string(data[4:8]) != "ftyp" {
			return fmt.Errorf("%w: videos must be MP4", ErrType)
		}
		if len(data) < MinVideoBytes {
			return fmt.Errorf("%w: videos must be at least 75 KB", ErrType)
		}
	}
	return nil
}

var numericHost = regexp.MustCompile(`^(0x[0-9a-f]+|[0-9]+)(\.(0x[0-9a-f]+|[0-9]+))*$`)

// ValidateURL applies the static SSRF checks without any network call: https
// only, no credentials, port 443, no internal names and no private IP literal.
// A hostname that resolves to a private address is refused later, when
// connecting.
func ValidateURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ErrBlocked
	}
	return checkURL(parsed)
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || u.Host == "" {
		return ErrBlocked
	}
	if port := u.Port(); port != "" && port != "443" {
		return ErrBlocked
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" {
		return ErrBlocked
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".localdomain"} {
		if strings.HasSuffix(host, suffix) {
			return ErrBlocked
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || blockedAddress(addr) {
			return ErrBlocked
		}
		return nil
	}
	// Integer and hex spellings of an IP ("2130706433", "0x7f000001").
	if numericHost.MatchString(host) {
		return ErrBlocked
	}
	return nil
}

var blockedPrefixes = func() []netip.Prefix {
	var prefixes []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "100::/64", "2001:db8::/32",
	} {
		prefixes = append(prefixes, netip.MustParsePrefix(p))
	}
	return prefixes
}()

// blockedAddress reports whether connecting to addr could reach an internal,
// reserved or otherwise non-public destination.
func blockedAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Fetcher downloads files from public https URLs.
type Fetcher struct {
	// Fields below are replaced only by tests.
	checkURL  func(*url.URL) error
	blocked   func(netip.AddrPort) bool
	tlsConfig *tls.Config
}

// NewFetcher returns a Fetcher with every SSRF protection on.
func NewFetcher() *Fetcher {
	return &Fetcher{
		checkURL: checkURL,
		blocked:  func(addr netip.AddrPort) bool { return blockedAddress(addr.Addr()) },
	}
}

const maxRedirects = 3

// Fetch downloads rawURL and returns its body, which is at most maxBytes long.
// It refuses non-public destinations when it connects (so a name that resolves
// to a private address, or a redirect to one, fails), follows at most three
// redirects with each one checked again, uses no proxy and sends no
// credentials.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || f.checkURL(parsed) != nil {
		return nil, ErrBlocked
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			addr, err := netip.ParseAddrPort(address)
			if err != nil || f.blocked(addr) {
				return ErrBlocked
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       f.tlsConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("too many redirects")
			}
			return f.checkURL(req.URL)
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, ErrBlocked
	}
	request.Header.Set("User-Agent", "linkedin-mcp")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		return nil, errors.New("could not download the file")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not download the file: the server answered HTTP %d", response.StatusCode)
	}
	if kind := strings.ToLower(response.Header.Get("Content-Type")); strings.HasPrefix(kind, "text/") || strings.Contains(kind, "json") {
		return nil, fmt.Errorf("%w: the server sent %s", ErrType, strings.SplitN(kind, ";", 2)[0])
	}
	if response.ContentLength > maxBytes {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("could not download the file")
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}
