package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
	"github.com/tavric-labs/linkedin-mcp/internal/media"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func bigMP4() []byte {
	return append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, media.MinVideoBytes)...)
}

type harness struct {
	api     *fakeLinkedIn
	tokens  *fakeTokens
	fetcher *fakeFetcher
	server  *Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{api: &fakeLinkedIn{}, tokens: &fakeTokens{}, fetcher: &fakeFetcher{data: tinyPNG(t)}}
	h.server = &Server{LinkedIn: h.api, Tokens: h.tokens, Fetcher: h.fetcher}
	return h
}

// untouched fails the test when a call reached the token exchange, LinkedIn or the network.
func (h *harness) untouched(t *testing.T, label string) {
	t.Helper()
	if h.tokens.subjectSeen != "" || len(h.api.calls) != 0 || len(h.fetcher.urls) != 0 {
		t.Errorf("%s: exchange=%q linkedin=%v fetched=%v", label, h.tokens.subjectSeen, h.api.calls, h.fetcher.urls)
	}
}

// writeCall runs one write tool with the given confirm and scopes.
type writeCall func(h *harness, req *mcp.CallToolRequest, confirm bool) (*mcp.CallToolResult, error)

func writeTools(t *testing.T) map[string]writeCall {
	png := b64(tinyPNG(t))
	return map[string]writeCall{
		"create_post": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreatePost(context.Background(), req, PostArgs{Text: "hello", Confirm: c})
			return r, err
		},
		"article": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreateArticlePost(context.Background(), req, ArticleArgs{URL: "https://example.com/a", Title: "T", Thumbnail: &MediaInput{Base64: png}, Confirm: c})
			return r, err
		},
		"image": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreateImagePost(context.Background(), req, ImagePostArgs{Images: []ImageInput{{Base64: png}}, Confirm: c})
			return r, err
		},
		"video": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreateVideoPost(context.Background(), req, VideoPostArgs{VideoBase64: b64(bigMP4()), Confirm: c})
			return r, err
		},
		"document": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreateDocumentPost(context.Background(), req, DocumentPostArgs{DocumentBase64: b64([]byte("%PDF-1.7 x")), Title: "Deck", Confirm: c})
			return r, err
		},
		"poll": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreatePollPost(context.Background(), req, PollPostArgs{Question: "Q?", Options: []string{"a", "b"}, Confirm: c})
			return r, err
		},
		"reshare": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.ResharePost(context.Background(), req, ReshareArgs{PostURN: "urn:li:share:1", Confirm: c})
			return r, err
		},
		"edit": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.EditPost(context.Background(), req, EditPostArgs{PostURN: "urn:li:share:1", Text: "new", Confirm: c})
			return r, err
		},
		"delete": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.DeletePost(context.Background(), req, DeletePostArgs{PostURN: "urn:li:share:1", Confirm: c})
			return r, err
		},
		"org post": func(h *harness, req *mcp.CallToolRequest, c bool) (*mcp.CallToolResult, error) {
			r, _, err := h.server.CreateOrganizationPost(context.Background(), req, OrganizationPostArgs{OrganizationID: "2414183", Text: "hi", Confirm: c})
			return r, err
		},
	}
}

func TestEveryWriteToolIsADryRunUnlessConfirmed(t *testing.T) {
	for name, call := range writeTools(t) {
		h := newHarness(t)
		result, err := call(h, request("mcp-bearer"), false)
		if err != nil || result.IsError {
			t.Errorf("%s: err=%v result=%+v", name, err, result)
			continue
		}
		if !strings.Contains(strings.ToLower(text(t, result)), "dry run") || !strings.Contains(text(t, result), "confirm: true") {
			t.Errorf("%s: dry run text = %q", name, text(t, result))
		}
		h.untouched(t, name)
	}
}

func TestEveryWriteToolNeedsTheWriteScopeToConfirm(t *testing.T) {
	for name, call := range writeTools(t) {
		h := newHarness(t)
		result, err := call(h, requestWithScopes("mcp-bearer", "tools:read"), true)
		if err != nil || !result.IsError || !strings.Contains(text(t, result), WriteScope) {
			t.Errorf("%s: err=%v result=%+v", name, err, result)
		}
		h.untouched(t, name)
		// A read-only token may still preview.
		if preview, err := call(h, requestWithScopes("mcp-bearer", "tools:read"), false); err != nil || preview.IsError {
			t.Errorf("%s: read-only preview failed: err=%v result=%+v", name, err, preview)
		}
	}
}

func TestEveryWriteToolRefusesARequestWithoutTheSubjectToken(t *testing.T) {
	for name, call := range writeTools(t) {
		h := newHarness(t)
		result, err := call(h, request(""), true)
		if err != nil || !result.IsError {
			t.Errorf("%s: err=%v result=%+v", name, err, result)
		}
		if len(h.api.calls) != 0 {
			t.Errorf("%s: LinkedIn was called: %v", name, h.api.calls)
		}
	}
}

func TestEveryWriteToolMapsLinkedInFailuresToGenericMessages(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{linkedin.ErrUnauthorized, "sign in again"},
		{&linkedin.APIError{Status: 426, Err: linkedin.ErrVersionInactive}, "LINKEDIN_API_VERSION"},
		{&linkedin.APIError{Status: http.StatusForbidden, Code: "ACCESS_DENIED", Message: "secret-detail"}, "does not allow it"},
		{&linkedin.APIError{Status: http.StatusTooManyRequests, Message: "secret-detail"}, "rate limit"},
		{&linkedin.APIError{Status: http.StatusNotFound, Message: "secret-detail"}, "could not find that post"},
		{&linkedin.APIError{Status: 500, Message: "secret-detail"}, "could not complete"},
	}
	for name, call := range writeTools(t) {
		for _, tc := range cases {
			logs := captureLogs(t)
			h := newHarness(t)
			h.api.err = tc.err
			result, err := call(h, request("mcp-bearer"), true)
			if err != nil || !result.IsError {
				t.Errorf("%s/%v: err=%v result=%+v", name, tc.err, err, result)
				continue
			}
			got := text(t, result)
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) || strings.Contains(got, "secret-detail") {
				t.Errorf("%s/%v: message = %q, want %q and no detail", name, tc.err, got, tc.want)
			}
			if apiErr := (*linkedin.APIError)(nil); errors.As(tc.err, &apiErr) && apiErr.Message != "" && !strings.Contains(logs.String(), "secret-detail") {
				t.Errorf("%s: the log lacks LinkedIn's detail: %s", name, logs)
			}
		}
	}
}

func TestPostTextIsNeverLogged(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	if _, _, err := h.server.CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "private-post-text", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "private-post-text") || strings.Contains(logs.String(), "member-token") || strings.Contains(logs.String(), "mcp-bearer") {
		t.Fatalf("the log leaked: %s", logs)
	}
}

func TestProfileReportsTheOptionalFields(t *testing.T) {
	h := newHarness(t)
	result, _, _ := h.server.GetProfile(context.Background(), request("mcp-bearer"), ProfileArgs{})
	got := text(t, result)
	for _, want := range []string{"Ada Lovelace", "abc123", "ada@example.com", "verified", "en-US", "https://img/ada.png"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
}

func TestArticlePostUploadsTheThumbnailAndPublishesTheLink(t *testing.T) {
	h := newHarness(t)
	h.fetcher.data = tinyPNG(t)
	result, _, err := h.server.CreateArticlePost(context.Background(), request("mcp-bearer"), ArticleArgs{
		URL: "https://example.com/post", Title: "A title", Description: "A description", Text: "Read this (now)",
		Thumbnail: &MediaInput{URL: "https://cdn.example.com/t.png"}, Visibility: "connections", Confirm: true,
	})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	if len(h.fetcher.urls) != 1 || h.fetcher.urls[0] != "https://cdn.example.com/t.png" {
		t.Fatalf("fetched %v", h.fetcher.urls)
	}
	post := h.api.published[0]
	article := post.Content["article"].(map[string]any)
	if post.Visibility != linkedin.VisibilityConnections || post.Text != "Read this (now)" || article["source"] != "https://example.com/post" ||
		article["title"] != "A title" || article["description"] != "A description" || article["thumbnail"] != "urn:li:image:"+"1" {
		t.Fatalf("post = %+v", post)
	}
}

func TestArticlePostWithoutAThumbnailUploadsNothing(t *testing.T) {
	h := newHarness(t)
	result, _, _ := h.server.CreateArticlePost(context.Background(), request("mcp-bearer"), ArticleArgs{URL: "https://example.com/post", Title: "T", Confirm: true})
	if result.IsError || len(h.api.uploads) != 0 {
		t.Fatalf("result=%s uploads=%v", text(t, result), h.api.uploads)
	}
}

func TestArticleInputValidation(t *testing.T) {
	for name, args := range map[string]ArticleArgs{
		"no url":         {Title: "T"},
		"http url":       {URL: "http://example.com", Title: "T"},
		"javascript url": {URL: "javascript:alert(1)", Title: "T"},
		"no title":       {URL: "https://example.com"},
		"long title":     {URL: "https://example.com", Title: strings.Repeat("x", 401)},
		"bad visibility": {URL: "https://example.com", Title: "T", Visibility: "x"},
		"two sources":    {URL: "https://example.com", Title: "T", Thumbnail: &MediaInput{URL: "https://a.example/t.png", Base64: "AAAA"}},
		"private thumb":  {URL: "https://example.com", Title: "T", Thumbnail: &MediaInput{URL: "https://127.0.0.1/t.png"}},
		"svg thumb":      {URL: "https://example.com", Title: "T", Thumbnail: &MediaInput{Base64: b64([]byte("<svg/>"))}},
	} {
		for _, confirm := range []bool{false, true} {
			args.Confirm = confirm
			h := newHarness(t)
			result, _, err := h.server.CreateArticlePost(context.Background(), request("mcp-bearer"), args)
			if err != nil || !result.IsError {
				t.Errorf("%s (confirm=%v): err=%v result=%s", name, confirm, err, text(t, result))
			}
			h.untouched(t, name)
		}
	}
}

func TestImagePostSingleImageUsesInlineBytes(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), ImagePostArgs{
		Text: "Look", Images: []ImageInput{{Base64: b64(tinyPNG(t)), AltText: "a square"}}, Confirm: true,
	})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	if len(h.fetcher.urls) != 0 {
		t.Fatalf("inline bytes were fetched: %v", h.fetcher.urls)
	}
	if !bytes.Equal(h.api.uploads["Image"], tinyPNG(t)) {
		t.Fatal("the image bytes were not uploaded")
	}
	media := h.api.published[0].Content["media"].(map[string]any)
	if media["id"] != "urn:li:image:1" || media["altText"] != "a square" {
		t.Fatalf("media = %v", media)
	}
}

func TestImagePostMultipleImagesMixesURLAndInline(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), ImagePostArgs{
		Images:  []ImageInput{{URL: "https://cdn.example.com/1.png"}, {Base64: b64(tinyPNG(t)), AltText: "two"}},
		Confirm: true,
	})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	if _, ok := h.api.published[0].Content["multiImage"]; !ok {
		t.Fatalf("content = %v", h.api.published[0].Content)
	}
	uploads := 0
	for _, call := range h.api.calls {
		if call == "UploadImage" {
			uploads++
		}
	}
	if uploads != 2 || len(h.fetcher.urls) != 1 {
		t.Fatalf("calls=%v fetched=%v", h.api.calls, h.fetcher.urls)
	}
}

func TestImagePostValidation(t *testing.T) {
	valid := ImageInput{Base64: b64(tinyPNG(t))}
	tooMany := make([]ImageInput, MaxImages+1)
	for i := range tooMany {
		tooMany[i] = valid
	}
	for name, args := range map[string]ImagePostArgs{
		"no images":        {},
		"too many images":  {Images: tooMany},
		"neither source":   {Images: []ImageInput{{}}},
		"both sources":     {Images: []ImageInput{{URL: "https://a.example/x.png", Base64: b64(tinyPNG(t))}}},
		"not an image":     {Images: []ImageInput{{Base64: b64([]byte("%PDF-1.7"))}}},
		"bad base64":       {Images: []ImageInput{{Base64: "***"}}},
		"http url":         {Images: []ImageInput{{URL: "http://a.example/x.png"}}},
		"metadata address": {Images: []ImageInput{{URL: "https://169.254.169.254/latest/meta-data"}}},
		"long alt text":    {Images: []ImageInput{{Base64: valid.Base64, AltText: strings.Repeat("x", 4087)}}},
		"long text":        {Text: strings.Repeat("x", MaxPostRunes+1), Images: []ImageInput{valid}},
	} {
		for _, confirm := range []bool{false, true} {
			args.Confirm = confirm
			h := newHarness(t)
			result, _, err := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), args)
			if err != nil || !result.IsError {
				t.Errorf("%s (confirm=%v): err=%v result=%s", name, confirm, err, text(t, result))
			}
			h.untouched(t, name)
		}
	}
}

func TestMediaDownloadFailureStopsBeforeAnyLinkedInCall(t *testing.T) {
	h := newHarness(t)
	h.fetcher.err = media.ErrTooLarge
	result, _, _ := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), ImagePostArgs{Images: []ImageInput{{URL: "https://cdn.example.com/huge.png"}}, Confirm: true})
	if !result.IsError || !strings.Contains(text(t, result), "too large") {
		t.Fatalf("result = %s", text(t, result))
	}
	if h.tokens.subjectSeen != "" || len(h.api.calls) != 0 {
		t.Fatalf("exchange=%q calls=%v", h.tokens.subjectSeen, h.api.calls)
	}
}

func TestDownloadedBytesAreCheckedForTheirRealType(t *testing.T) {
	h := newHarness(t)
	h.fetcher.data = []byte("<html>not an image</html>")
	result, _, _ := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), ImagePostArgs{Images: []ImageInput{{URL: "https://cdn.example.com/x.png"}}, Confirm: true})
	if !result.IsError || len(h.api.calls) != 0 {
		t.Fatalf("result=%s calls=%v", text(t, result), h.api.calls)
	}
}

func TestTheTotalImageSizeIsBounded(t *testing.T) {
	h := newHarness(t)
	big := bytes.Repeat([]byte{0}, media.MaxInlineBytes-100)
	big = append(tinyPNG(t), big...) // a valid PNG header followed by padding stays a valid image to DecodeConfig
	args := ImagePostArgs{Confirm: true}
	for i := 0; i < 5; i++ {
		args.Images = append(args.Images, ImageInput{Base64: b64(big)})
	}
	result, _, _ := h.server.CreateImagePost(context.Background(), request("mcp-bearer"), args)
	if !result.IsError || !strings.Contains(text(t, result), "too large") || len(h.api.calls) != 0 {
		t.Fatalf("result=%s calls=%v", text(t, result), h.api.calls)
	}
}

func TestVideoPostUploadsThenPublishes(t *testing.T) {
	h := newHarness(t)
	h.fetcher.data = bigMP4()
	result, _, err := h.server.CreateVideoPost(context.Background(), request("mcp-bearer"), VideoPostArgs{Text: "Demo", Title: "Demo video", VideoURL: "https://cdn.example.com/v.mp4", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	if strings.Join(h.api.calls, ",") != "Profile,UploadVideo,Publish" {
		t.Fatalf("calls = %v", h.api.calls)
	}
	m := h.api.published[0].Content["media"].(map[string]any)
	if m["id"] != "urn:li:video:1" || m["title"] != "Demo video" {
		t.Fatalf("media = %v", m)
	}
}

func TestVideoPostValidation(t *testing.T) {
	for name, args := range map[string]VideoPostArgs{
		"no source":   {},
		"both":        {VideoURL: "https://a.example/v.mp4", VideoBase64: "AAAA"},
		"not mp4":     {VideoBase64: b64(tinyPNG(t))},
		"too small":   {VideoBase64: b64([]byte("\x00\x00\x00\x18ftypmp42"))},
		"private url": {VideoURL: "https://10.0.0.1/v.mp4"},
	} {
		h := newHarness(t)
		args.Confirm = true
		result, _, err := h.server.CreateVideoPost(context.Background(), request("mcp-bearer"), args)
		if err != nil || !result.IsError {
			t.Errorf("%s: err=%v result=%s", name, err, text(t, result))
		}
		h.untouched(t, name)
	}
}

func TestDocumentPostUploadsThenPublishesWithATitle(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.CreateDocumentPost(context.Background(), request("mcp-bearer"), DocumentPostArgs{DocumentBase64: b64([]byte("%PDF-1.7 body")), Title: "Q3 deck", Text: "Slides", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	m := h.api.published[0].Content["media"].(map[string]any)
	if !strings.HasPrefix(m["id"].(string), "urn:li:document:") || m["title"] != "Q3 deck" {
		t.Fatalf("media = %v", m)
	}
	for name, args := range map[string]DocumentPostArgs{
		"no title":  {DocumentBase64: b64([]byte("%PDF-1.7"))},
		"not a doc": {DocumentBase64: b64(tinyPNG(t)), Title: "x"},
		"no source": {Title: "x"},
	} {
		h := newHarness(t)
		args.Confirm = true
		if r, _, _ := h.server.CreateDocumentPost(context.Background(), request("mcp-bearer"), args); !r.IsError {
			t.Errorf("%s: accepted", name)
		}
		h.untouched(t, name)
	}
}

func TestPollPost(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.CreatePollPost(context.Background(), request("mcp-bearer"), PollPostArgs{Question: "Tabs or spaces?", Options: []string{"Tabs", "Spaces", "Both"}, Duration: "seven_days", Text: "Vote", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	poll := h.api.published[0].Content["poll"].(map[string]any)
	if poll["question"] != "Tabs or spaces?" || poll["settings"].(map[string]any)["duration"] != "SEVEN_DAYS" || len(poll["options"].([]map[string]any)) != 3 {
		t.Fatalf("poll = %v", poll)
	}
	h = newHarness(t)
	h.server.CreatePollPost(context.Background(), request("mcp-bearer"), PollPostArgs{Question: "Q?", Options: []string{"a", "b"}, Confirm: true})
	if h.api.published[0].Content["poll"].(map[string]any)["settings"].(map[string]any)["duration"] != "THREE_DAYS" {
		t.Fatal("the default duration is not THREE_DAYS")
	}
}

func TestPollValidation(t *testing.T) {
	for name, args := range map[string]PollPostArgs{
		"no question":   {Options: []string{"a", "b"}},
		"long question": {Question: strings.Repeat("q", 141), Options: []string{"a", "b"}},
		"one option":    {Question: "Q?", Options: []string{"a"}},
		"five options":  {Question: "Q?", Options: []string{"a", "b", "c", "d", "e"}},
		"empty option":  {Question: "Q?", Options: []string{"a", " "}},
		"long option":   {Question: "Q?", Options: []string{"a", strings.Repeat("o", 31)}},
		"bad duration":  {Question: "Q?", Options: []string{"a", "b"}, Duration: "FOREVER"},
		"duplicate":     {Question: "Q?", Options: []string{"a", "A"}},
	} {
		h := newHarness(t)
		args.Confirm = true
		if r, _, _ := h.server.CreatePollPost(context.Background(), request("mcp-bearer"), args); !r.IsError {
			t.Errorf("%s: accepted", name)
		}
		h.untouched(t, name)
	}
}

func TestResharePost(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.ResharePost(context.Background(), request("mcp-bearer"), ReshareArgs{PostURN: "urn:li:ugcPost:99", Text: "Worth a read", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	post := h.api.published[0]
	if post.ReshareParent != "urn:li:ugcPost:99" || post.Text != "Worth a read" || post.Author != "urn:li:person:abc123" {
		t.Fatalf("post = %+v", post)
	}
	for _, bad := range []string{"", "99", "urn:li:person:1", "urn:li:share:x", "https://www.linkedin.com/feed/update/urn:li:share:1/"} {
		h := newHarness(t)
		if r, _, _ := h.server.ResharePost(context.Background(), request("mcp-bearer"), ReshareArgs{PostURN: bad, Confirm: true}); !r.IsError {
			t.Errorf("%q accepted", bad)
		}
		h.untouched(t, bad)
	}
}

func TestEditPost(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.EditPost(context.Background(), request("mcp-bearer"), EditPostArgs{PostURN: "urn:li:share:5", Text: "  Fixed typo  ", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	if h.api.edited.urn != "urn:li:share:5" || h.api.edited.text != "Fixed typo" || h.api.writeToken != "member-token" {
		t.Fatalf("edited = %+v", h.api.edited)
	}
	for name, args := range map[string]EditPostArgs{
		"empty":    {PostURN: "urn:li:share:5"},
		"too long": {PostURN: "urn:li:share:5", Text: strings.Repeat("x", MaxPostRunes+1)},
		"bad urn":  {PostURN: "nope", Text: "x"},
	} {
		h := newHarness(t)
		args.Confirm = true
		if r, _, _ := h.server.EditPost(context.Background(), request("mcp-bearer"), args); !r.IsError {
			t.Errorf("%s accepted", name)
		}
		h.untouched(t, name)
	}
}

func TestDeletePost(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.DeletePost(context.Background(), request("mcp-bearer"), DeletePostArgs{PostURN: "urn:li:share:5", Confirm: true})
	if err != nil || result.IsError || h.api.deleted != "urn:li:share:5" {
		t.Fatalf("err=%v result=%s deleted=%q", err, text(t, result), h.api.deleted)
	}
	h = newHarness(t)
	if r, _, _ := h.server.DeletePost(context.Background(), request("mcp-bearer"), DeletePostArgs{PostURN: "urn:li:person:5", Confirm: true}); !r.IsError {
		t.Fatal("a person URN was accepted")
	}
	h.untouched(t, "bad urn")
}

func TestOrganizationPostUsesTheOrganizationAsAuthor(t *testing.T) {
	h := newHarness(t)
	result, _, err := h.server.CreateOrganizationPost(context.Background(), request("mcp-bearer"), OrganizationPostArgs{OrganizationID: "2414183", Text: "News", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%s", err, text(t, result))
	}
	post := h.api.published[0]
	if post.Author != "urn:li:organization:2414183" || post.Visibility != linkedin.VisibilityPublic || post.Text != "News" {
		t.Fatalf("post = %+v", post)
	}
	if len(h.api.calls) != 1 {
		t.Fatalf("calls = %v; an organization post needs no member profile call", h.api.calls)
	}
	for _, bad := range []string{"", "abc", "24/14", "urn:li:organization:1", "-1"} {
		h := newHarness(t)
		if r, _, _ := h.server.CreateOrganizationPost(context.Background(), request("mcp-bearer"), OrganizationPostArgs{OrganizationID: bad, Text: "x", Confirm: true}); !r.IsError {
			t.Errorf("organization id %q accepted", bad)
		}
		h.untouched(t, bad)
	}
}

func listTools(t *testing.T, enableOrg bool) map[string]bool {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	(&Server{}).Register(server, enableOrg)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestOrganizationToolIsRegisteredOnlyWhenEnabled(t *testing.T) {
	if listTools(t, false)["linkedin_create_organization_post"] {
		t.Fatal("the organization tool is registered by default")
	}
	if !listTools(t, true)["linkedin_create_organization_post"] {
		t.Fatal("the organization tool is missing when enabled")
	}
}

func TestEveryRegisteredToolIsInTheServerAllowList(t *testing.T) {
	data, err := os.ReadFile("../../.mcp/servers.yaml")
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	for name := range listTools(t, true) {
		marker := "- name: " + name + "\n"
		at := strings.Index(yaml, marker)
		if at < 0 {
			t.Errorf("%s is registered but missing from .mcp/servers.yaml, whose policy denies unlisted tools", name)
			continue
		}
		entry := yaml[at+len(marker):]
		if next := strings.Index(entry, "- name:"); next >= 0 {
			entry = entry[:next]
		}
		wantEffect := "write"
		if name == "linkedin_get_profile" {
			wantEffect = "read"
		}
		if !strings.Contains(entry, "sideEffect: "+wantEffect) {
			t.Errorf("%s should be sideEffect: %s in servers.yaml", name, wantEffect)
		}
	}
	for _, listed := range strings.Split(yaml, "- name: linkedin_")[1:] {
		name := "linkedin_" + strings.SplitN(listed, "\n", 2)[0]
		if !listTools(t, true)[name] {
			t.Errorf("%s is in servers.yaml but not registered", name)
		}
	}
}
