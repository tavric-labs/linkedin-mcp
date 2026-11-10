// Package linkedin is a small client for the two LinkedIn calls this server
// makes on behalf of a signed-in member: reading their OpenID profile and
// publishing a post. It never stores a token; the caller passes one per call.
package linkedin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized means LinkedIn rejected the member's token: it expired
// (self-serve tokens last about 60 days and cannot be refreshed) or was
// revoked. The member has to sign in again.
var ErrUnauthorized = errors.New("linkedin rejected the member's token")

// ErrVersionInactive means LinkedIn answered HTTP 426: the Linkedin-Version
// header names a version that is no longer active (versions sunset about a
// year after release). The operator must set LINKEDIN_API_VERSION.
var ErrVersionInactive = errors.New("linkedin api version is no longer active")

// maxDetail caps each LinkedIn error field copied into an error.
const maxDetail = 200

// APIError is a non-2xx answer from a versioned LinkedIn call. Its text is for
// server logs: the status plus LinkedIn's own error code, service error code
// and message, each truncated. It never holds a request body or token.
type APIError struct {
	Status           int
	Code             string
	ServiceErrorCode string
	Message          string
	Err              error // optional sentinel, such as ErrVersionInactive
}

func (e *APIError) Error() string {
	text := fmt.Sprintf("linkedin returned HTTP %d", e.Status)
	if e.Code != "" {
		text += " code=" + e.Code
	}
	if e.ServiceErrorCode != "" {
		text += " serviceErrorCode=" + e.ServiceErrorCode
	}
	if e.Message != "" {
		text += " message=" + e.Message
	}
	return text
}

func (e *APIError) Unwrap() error { return e.Err }

// Visibility is who may see a post.
type Visibility string

const (
	VisibilityPublic      Visibility = "PUBLIC"
	VisibilityConnections Visibility = "CONNECTIONS"
)

// Profile is the OpenID Connect userinfo LinkedIn returns for a member. Name
// fields need the profile scope and Email needs the email scope; a field the
// token was not granted stays empty.
type Profile struct {
	Sub           string `json:"sub"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
	Locale        string `json:"-"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// UnmarshalJSON accepts locale as the documented string ("en-US") or as the
// object LinkedIn has also returned ({"country":"US","language":"en"}).
func (p *Profile) UnmarshalJSON(data []byte) error {
	type plain Profile
	var raw struct {
		plain
		Locale json.RawMessage `json:"locale"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = Profile(raw.plain)
	var text string
	if json.Unmarshal(raw.Locale, &text) == nil {
		p.Locale = text
		return nil
	}
	var object struct{ Language, Country string }
	if json.Unmarshal(raw.Locale, &object) == nil && object.Language != "" {
		p.Locale = object.Language
		if object.Country != "" {
			p.Locale += "-" + object.Country
		}
	}
	return nil
}

// Client calls the LinkedIn API. BaseURL and Version are injectable so tests
// and staging can point at another host or API version.
type Client struct {
	baseURL string
	version string
	http    *http.Client
	upload  *http.Client
	// allowUploadURL decides whether an upload URL from LinkedIn may receive
	// bytes (and, for images and documents, the member token).
	allowUploadURL func(*url.URL) bool
}

// New returns a client for the API at baseURL, sending version as the
// Linkedin-Version header on versioned (/rest) calls.
func New(baseURL, version string) *Client {
	noRedirect := func(*http.Request, []*http.Request) error { return errors.New("linkedin must not redirect") }
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		version: version,
		// A token must never follow a redirect to another host.
		http:           &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect},
		upload:         &http.Client{Timeout: 5 * time.Minute, CheckRedirect: noRedirect},
		allowUploadURL: defaultAllowUploadURL,
	}
}

// defaultAllowUploadURL accepts only https URLs on linkedin.com or a subdomain.
func defaultAllowUploadURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "linkedin.com" || strings.HasSuffix(host, ".linkedin.com")
}

// Profile returns the member the token belongs to.
func (c *Client) Profile(ctx context.Context, token string) (Profile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/userinfo", nil)
	if err != nil {
		return Profile{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return Profile{}, fmt.Errorf("linkedin userinfo: %w", err)
	}
	defer response.Body.Close()
	if err := statusError(response); err != nil {
		return Profile{}, err
	}
	var profile Profile
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("decode linkedin userinfo: %w", err)
	}
	if profile.Sub == "" {
		return Profile{}, errors.New("linkedin userinfo has no member id")
	}
	return profile, nil
}

// PersonURN returns the URN of the member with the given id.
func PersonURN(id string) string { return "urn:li:person:" + id }

// OrganizationURN returns the URN of the organization page with the given id.
func OrganizationURN(id string) string { return "urn:li:organization:" + id }

var (
	authorURN   = regexp.MustCompile(`^urn:li:(person|organization):[A-Za-z0-9_-]+$`)
	postURN     = regexp.MustCompile(`^urn:li:(share|ugcPost):[0-9]+$`)
	littleSpecs = strings.NewReplacer(
		`\`, `\\`, "|", `\|`, "{", `\{`, "}", `\}`, "@", `\@`, "[", `\[`, "]", `\]`,
		"(", `\(`, ")", `\)`, "<", `\<`, ">", `\>`, "*", `\*`, "_", `\_`, "~", `\~`,
	)
)

// ValidPostURN reports whether urn is a share or ugcPost URN.
func ValidPostURN(urn string) bool { return postURN.MatchString(urn) }

// EscapeCommentary escapes the characters LinkedIn's "little" text format
// reserves, so post text shows literally instead of being cut off at a
// parenthesis or turned into a mention. A "#" is left alone so #hashtags work.
func EscapeCommentary(text string) string { return littleSpecs.Replace(text) }

// Post is a LinkedIn post to publish. Text is plain text; it is escaped for
// LinkedIn's little format. Content and ReshareParent are optional.
type Post struct {
	Author         string
	Text           string
	Visibility     Visibility
	DisableReshare bool
	Content        map[string]any
	ReshareParent  string
}

// Image is an uploaded image to attach to a post.
type Image struct{ URN, AltText string }

// PollDuration is how long a poll accepts votes.
type PollDuration string

const (
	PollOneDay       PollDuration = "ONE_DAY"
	PollThreeDays    PollDuration = "THREE_DAYS"
	PollSevenDays    PollDuration = "SEVEN_DAYS"
	PollFourteenDays PollDuration = "FOURTEEN_DAYS"
)

// ArticleContent is a link post. Description and thumbnail (an image URN) are
// optional; LinkedIn does not scrape the URL, so these set the preview card.
func ArticleContent(source, title, description, thumbnail string) map[string]any {
	article := map[string]any{"source": source, "title": title}
	if description != "" {
		article["description"] = description
	}
	if thumbnail != "" {
		article["thumbnail"] = thumbnail
	}
	return map[string]any{"article": article}
}

// ImageContent is one image (media) or two or more images (multiImage).
func ImageContent(images []Image) map[string]any {
	entries := make([]map[string]any, 0, len(images))
	for _, image := range images {
		entry := map[string]any{"id": image.URN}
		if image.AltText != "" {
			entry["altText"] = image.AltText
		}
		entries = append(entries, entry)
	}
	if len(entries) == 1 {
		return map[string]any{"media": entries[0]}
	}
	return map[string]any{"multiImage": map[string]any{"images": entries}}
}

// MediaContent is a video or document post; id is its video or document URN.
func MediaContent(id, title string) map[string]any {
	media := map[string]any{"id": id}
	if title != "" {
		media["title"] = title
	}
	return map[string]any{"media": media}
}

// PollContent is a poll with two to four options.
func PollContent(question string, options []string, duration PollDuration) map[string]any {
	entries := make([]map[string]any, 0, len(options))
	for _, option := range options {
		entries = append(entries, map[string]any{"text": option})
	}
	return map[string]any{"poll": map[string]any{
		"question": question,
		"options":  entries,
		"settings": map[string]any{"duration": string(duration)},
	}}
}

// Publish creates the post and returns its URN.
func (c *Client) Publish(ctx context.Context, token string, post Post) (string, error) {
	if !authorURN.MatchString(post.Author) {
		return "", errors.New("author must be a person or organization URN")
	}
	if post.Visibility != VisibilityPublic && post.Visibility != VisibilityConnections {
		return "", fmt.Errorf("visibility must be %s or %s", VisibilityPublic, VisibilityConnections)
	}
	if post.ReshareParent != "" && !postURN.MatchString(post.ReshareParent) {
		return "", errors.New("the post to reshare must be a share or ugcPost URN")
	}
	body := map[string]any{
		"author":         post.Author,
		"visibility":     string(post.Visibility),
		"lifecycleState": "PUBLISHED",
		"distribution": map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		"isReshareDisabledByAuthor": post.DisableReshare,
	}
	if post.Text != "" || post.ReshareParent == "" {
		body["commentary"] = EscapeCommentary(post.Text)
	}
	if post.Content != nil {
		body["content"] = post.Content
	}
	if post.ReshareParent != "" {
		body["reshareContext"] = map[string]any{"parent": post.ReshareParent}
	}
	response, err := c.versioned(ctx, token, http.MethodPost, "/rest/posts", body, nil)
	if err != nil {
		return "", fmt.Errorf("linkedin create post: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", apiError(response)
	}
	return response.Header.Get("X-Restli-Id"), nil
}

// EditPostCommentary replaces the text of one of the member's posts.
func (c *Client) EditPostCommentary(ctx context.Context, token, urn, text string) error {
	if !postURN.MatchString(urn) {
		return errors.New("the post must be a share or ugcPost URN")
	}
	patch := map[string]any{"patch": map[string]any{"$set": map[string]any{"commentary": EscapeCommentary(text)}}}
	return c.noContent(ctx, token, http.MethodPost, "/rest/posts/"+encodeURN(urn), patch, map[string]string{"X-RestLi-Method": "PARTIAL_UPDATE"})
}

// DeletePost deletes one of the member's posts. Deleting a post that is
// already gone succeeds.
func (c *Client) DeletePost(ctx context.Context, token, urn string) error {
	if !postURN.MatchString(urn) {
		return errors.New("the post must be a share or ugcPost URN")
	}
	return c.noContent(ctx, token, http.MethodDelete, "/rest/posts/"+encodeURN(urn), nil, map[string]string{"X-RestLi-Method": "DELETE"})
}

func encodeURN(urn string) string { return strings.ReplaceAll(url.PathEscape(urn), ":", "%3A") }

func (c *Client) noContent(ctx context.Context, token, method, path string, body any, extra map[string]string) error {
	response, err := c.versioned(ctx, token, method, path, body, extra)
	if err != nil {
		return fmt.Errorf("linkedin %s: %w", strings.ToLower(method), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return nil
}

// versioned sends a /rest call with the required version and protocol headers.
func (c *Client) versioned(ctx context.Context, token, method, path string, body any, extra map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Linkedin-Version", c.version)
	request.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range extra {
		request.Header.Set(name, value)
	}
	return c.http.Do(request)
}

// statusError turns a non-2xx response into an error that carries only the
// status, never LinkedIn's response body, which can echo request details.
func statusError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return fmt.Errorf("linkedin returned HTTP %d", response.StatusCode)
}

// apiError reads LinkedIn's JSON error fields from a failed versioned call.
// A body that is not JSON contributes nothing.
func apiError(response *http.Response) error {
	if response.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return ErrUnauthorized
	}
	apiErr := &APIError{Status: response.StatusCode}
	if response.StatusCode == http.StatusUpgradeRequired {
		apiErr.Err = ErrVersionInactive
	}
	var body struct {
		Code             any    `json:"code"`
		ServiceErrorCode any    `json:"serviceErrorCode"`
		Message          string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&body); err == nil {
		apiErr.Code = detail(body.Code)
		apiErr.ServiceErrorCode = detail(body.ServiceErrorCode)
		apiErr.Message = detail(body.Message)
	}
	return apiErr
}

// detail renders a scalar JSON value on one line, truncated to maxDetail.
func detail(value any) string {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > maxDetail {
		text = text[:maxDetail]
	}
	return text
}

func decodeJSON(r io.Reader, out any) error {
	return json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(out)
}
