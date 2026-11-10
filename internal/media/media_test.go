package media

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := gif.Encode(&buffer, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// hugePNG is a valid PNG header that declares more pixels than LinkedIn accepts.
func hugePNG() []byte {
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	header := binary.BigEndian.AppendUint32(nil, 40000)
	header = binary.BigEndian.AppendUint32(header, 40000)
	header = append(header, 8, 2, 0, 0, 0)
	return append(append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", header)...), chunk("IDAT", []byte{0})...)
}

func TestValidateAcceptsOnlyTheRightFileTypes(t *testing.T) {
	mp4 := append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, MinVideoBytes)...)
	cases := []struct {
		name string
		kind Kind
		data []byte
		ok   bool
	}{
		{"png", Image, pngBytes(t), true},
		{"jpeg", Image, jpegBytes(t), true},
		{"gif", Image, gifBytes(t), true},
		{"html as image", Image, []byte("<html><script>alert(1)</script></html>"), false},
		{"svg as image", Image, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), false},
		{"pdf as image", Image, []byte("%PDF-1.7 ..."), false},
		{"empty image", Image, nil, false},
		{"too many pixels", Image, hugePNG(), false},
		{"pdf", Document, []byte("%PDF-1.7\n..."), true},
		{"pptx or docx", Document, []byte("PK\x03\x04rest"), true},
		{"legacy office", Document, []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"), true},
		{"png as document", Document, pngBytes(t), false},
		{"mp4", Video, mp4, true},
		{"tiny mp4", Video, []byte("\x00\x00\x00\x18ftypmp42"), false},
		{"png as video", Video, pngBytes(t), false},
	}
	for _, tc := range cases {
		err := Validate(tc.kind, tc.data)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestDecodeBase64EnforcesTheInlineLimit(t *testing.T) {
	data, err := DecodeBase64(base64.StdEncoding.EncodeToString([]byte("hello")))
	if err != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if data, err = DecodeBase64(base64.RawStdEncoding.EncodeToString([]byte("hello!"))); err != nil || string(data) != "hello!" {
		t.Fatalf("unpadded: data=%q err=%v", data, err)
	}
	if data, err = DecodeBase64("aGVs\nbG8="); err != nil || string(data) != "hello" {
		t.Fatalf("with a newline: data=%q err=%v", data, err)
	}
	if _, err := DecodeBase64("not base64 !!"); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
	huge := strings.Repeat("A", MaxInlineBytes*4/3+100)
	if _, err := DecodeBase64(huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestValidateURLRejectsUnsafeAddresses(t *testing.T) {
	good := []string{"https://example.com/a.png", "https://cdn.example.com:443/a.png?x=1", "https://93.184.216.34/a.png"}
	for _, raw := range good {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("%s rejected: %v", raw, err)
		}
	}
	bad := []string{
		"", "example.com/a.png", "http://example.com/a.png", "ftp://example.com/a", "file:///etc/passwd",
		"https://user:pass@example.com/a.png", "https:///a.png", "https://example.com:8443/a.png",
		"https://localhost/a.png", "https://LOCALHOST./a.png", "https://app.localhost/a.png", "https://svc.internal/a.png",
		"https://127.0.0.1/a.png", "https://10.0.0.5/a.png", "https://172.16.0.1/a.png", "https://192.168.1.1/a.png",
		"https://169.254.169.254/latest/meta-data", "https://100.64.0.1/a", "https://0.0.0.0/a", "https://[::1]/a",
		"https://[fe80::1]/a", "https://[fd00::1]/a", "https://[::ffff:127.0.0.1]/a", "https://[::ffff:10.0.0.1]/a",
		"https://2130706433/a.png", "https://0x7f000001/a.png", "https://[64:ff9b::7f00:1]/a",
	}
	for _, raw := range bad {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestBlockedAddress(t *testing.T) {
	for raw, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.31.0.1": true, "192.168.0.1": true, "169.254.169.254": true,
		"100.64.0.1": true, "0.0.0.0": true, "224.0.0.1": true, "255.255.255.255": true, "198.18.0.1": true,
		"::1": true, "fe80::1": true, "fc00::1": true, "::ffff:10.0.0.1": true, "::": true, "ff02::1": true,
		"64:ff9b::a00:1": true, "2002:7f00:1::": true,
		"93.184.216.34": false, "8.8.8.8": false, "2606:4700:4700::1111": false,
	} {
		if got := blockedAddress(netip.MustParseAddr(raw)); got != want {
			t.Errorf("blockedAddress(%s) = %v, want %v", raw, got, want)
		}
	}
}

// testFetcher talks to httptest TLS servers: it trusts their certificate and
// allows their (non-443) ports, but keeps every other rule. blockedPorts lists
// server ports the address guard must refuse.
func testFetcher(blockedPorts ...string) *Fetcher {
	f := NewFetcher()
	f.tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test certificate
	f.checkURL = func(u *url.URL) error {
		if u.Scheme != "https" {
			return ErrBlocked
		}
		return nil
	}
	f.blocked = func(addr netip.AddrPort) bool {
		for _, port := range blockedPorts {
			if addr.Port() == mustPort(port) {
				return true
			}
		}
		return false
	}
	return f
}

func mustPort(s string) uint16 {
	u, _ := url.Parse("https://x:" + s)
	var p uint16
	for _, c := range u.Port() {
		p = p*10 + uint16(c-'0')
	}
	return p
}

func portOf(s *httptest.Server) string { u, _ := url.Parse(s.URL); return u.Port() }

func TestFetchReturnsTheBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials were sent to the media host")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("body"))
	}))
	defer server.Close()
	data, err := testFetcher().Fetch(context.Background(), server.URL+"/a.png", 100)
	if err != nil || string(data) != "body" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestFetchEnforcesTheSizeCap(t *testing.T) {
	declared := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(make([]byte, 1000))
	}))
	defer declared.Close()
	chunked := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 20; i++ {
			_, _ = w.Write(make([]byte, 100))
			w.(http.Flusher).Flush()
		}
	}))
	defer chunked.Close()
	for name, server := range map[string]*httptest.Server{"content-length": declared, "streamed": chunked} {
		if _, err := testFetcher().Fetch(context.Background(), server.URL, 500); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v, want ErrTooLarge", name, err)
		}
	}
}

func TestFetchRejectsNonSuccessAndTextResponses(t *testing.T) {
	notFound := httptest.NewTLSServer(http.NotFoundHandler())
	defer notFound.Close()
	html := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer html.Close()
	for name, server := range map[string]*httptest.Server{"404": notFound, "html": html} {
		if _, err := testFetcher().Fetch(context.Background(), server.URL, 1000); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFetchRefusesLoopbackWithTheDefaultGuard(t *testing.T) {
	hit := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer server.Close()
	f := NewFetcher()
	f.tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test certificate
	f.checkURL = func(*url.URL) error { return nil }    // skip the static checks so the dial guard is what refuses
	if _, err := f.Fetch(context.Background(), server.URL, 100); err == nil {
		t.Fatal("a loopback server was fetched")
	}
	if hit {
		t.Fatal("the request reached the loopback server")
	}
}

func TestFetchDoesNotFollowARedirectToABlockedAddress(t *testing.T) {
	hit := false
	private := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer private.Close()
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL, http.StatusFound)
	}))
	defer public.Close()
	if _, err := testFetcher(portOf(private)).Fetch(context.Background(), public.URL, 100); err == nil {
		t.Fatal("the redirect was followed")
	}
	if hit {
		t.Fatal("the blocked server was reached")
	}
}

func TestFetchRejectsRedirectsToHTTPAndRedirectLoops(t *testing.T) {
	toHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/a.png", http.StatusFound)
	}))
	defer toHTTP.Close()
	loop := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer loop.Close()
	for name, server := range map[string]*httptest.Server{"http": toHTTP, "loop": loop} {
		if _, err := testFetcher().Fetch(context.Background(), server.URL+"/p", 100); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFetchValidatesTheURLFirst(t *testing.T) {
	if _, err := NewFetcher().Fetch(context.Background(), "http://example.com/a.png", 100); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}
