package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
)

type fakeTokens struct {
	subjectSeen string
	err         error
}

func (f *fakeTokens) MemberToken(_ context.Context, subjectToken string) (string, error) {
	f.subjectSeen = subjectToken
	if f.err != nil {
		return "", f.err
	}
	return "member-token", nil
}

type fakeLinkedIn struct {
	profileToken string
	profileErr   error
	calls        []string // names of the LinkedIn methods called, in order
	err          error    // returned by every write method
	published    []linkedin.Post
	uploads      map[string][]byte // kind -> bytes uploaded
	counts       map[string]int
	edited       struct{ urn, text string }
	deleted      string
	writeToken   string
}

func (f *fakeLinkedIn) Profile(_ context.Context, token string) (linkedin.Profile, error) {
	f.profileToken = token
	f.calls = append(f.calls, "Profile")
	if f.profileErr != nil {
		return linkedin.Profile{}, f.profileErr
	}
	return linkedin.Profile{Sub: "abc123", Name: "Ada Lovelace", GivenName: "Ada", FamilyName: "Lovelace", Email: "ada@example.com", EmailVerified: true, Locale: "en-US", Picture: "https://img/ada.png"}, nil
}

func (f *fakeLinkedIn) Publish(_ context.Context, token string, post linkedin.Post) (string, error) {
	f.calls = append(f.calls, "Publish")
	f.writeToken = token
	f.published = append(f.published, post)
	if f.err != nil {
		return "", f.err
	}
	return "urn:li:share:7001", nil
}

func (f *fakeLinkedIn) upload(kind string, data []byte) (string, error) {
	f.calls = append(f.calls, "Upload"+kind)
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
		f.counts = map[string]int{}
	}
	f.uploads[kind] = append(f.uploads[kind], data...)
	if f.err != nil {
		return "", f.err
	}
	f.counts[kind]++
	return "urn:li:" + strings.ToLower(kind) + ":" + fmt.Sprint(f.counts[kind]), nil
}

func (f *fakeLinkedIn) UploadImage(_ context.Context, _, _ string, data []byte) (string, error) {
	return f.upload("Image", data)
}
func (f *fakeLinkedIn) UploadDocument(_ context.Context, _, _ string, data []byte) (string, error) {
	return f.upload("Document", data)
}
func (f *fakeLinkedIn) UploadVideo(_ context.Context, _, _ string, data []byte) (string, error) {
	return f.upload("Video", data)
}

func (f *fakeLinkedIn) EditPostCommentary(_ context.Context, token, urn, text string) error {
	f.calls = append(f.calls, "Edit")
	f.writeToken = token
	f.edited.urn, f.edited.text = urn, text
	return f.err
}

func (f *fakeLinkedIn) DeletePost(_ context.Context, token, urn string) error {
	f.calls = append(f.calls, "Delete")
	f.writeToken = token
	f.deleted = urn
	return f.err
}

type fakeFetcher struct {
	urls []string
	data []byte
	err  error
}

func (f *fakeFetcher) Fetch(_ context.Context, url string, _ int64) ([]byte, error) {
	f.urls = append(f.urls, url)
	return f.data, f.err
}

func request(subjectToken string) *mcp.CallToolRequest {
	return requestWithScopes(subjectToken, WriteScope)
}

func requestWithScopes(subjectToken string, scopes ...string) *mcp.CallToolRequest {
	extra := map[string]any{}
	if subjectToken != "" {
		extra[SubjectTokenKey] = subjectToken
	}
	return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "user-1", Scopes: scopes, Extra: extra}}}
}

func text(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("empty result")
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", result.Content[0])
	}
	return content.Text
}

func TestGetProfileUsesTheExchangedMemberToken(t *testing.T) {
	tokens, api := &fakeTokens{}, &fakeLinkedIn{}
	result, _, err := (&Server{LinkedIn: api, Tokens: tokens}).GetProfile(context.Background(), request("mcp-bearer"), ProfileArgs{})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if tokens.subjectSeen != "mcp-bearer" || api.profileToken != "member-token" {
		t.Fatalf("exchange saw %q, linkedin saw %q", tokens.subjectSeen, api.profileToken)
	}
	if !strings.Contains(text(t, result), "Ada Lovelace") {
		t.Fatalf("profile text = %q", text(t, result))
	}
}

func TestToolsRefuseARequestWithoutTheSubjectToken(t *testing.T) {
	api := &fakeLinkedIn{}
	server := &Server{LinkedIn: api, Tokens: &fakeTokens{}}
	for name, req := range map[string]*mcp.CallToolRequest{"no extra": {}, "no token": request("")} {
		result, _, err := server.GetProfile(context.Background(), req, ProfileArgs{})
		if err != nil || !result.IsError {
			t.Errorf("%s: err=%v result=%+v, want an error result", name, err, result)
		}
	}
	if api.profileToken != "" {
		t.Fatal("linkedin was called without a member token")
	}
}

func TestCreatePostIsADryRunUnlessConfirmed(t *testing.T) {
	tokens, api := &fakeTokens{}, &fakeLinkedIn{}
	result, _, err := (&Server{LinkedIn: api, Tokens: tokens}).CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "Hello LinkedIn"})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if len(api.calls) != 0 || tokens.subjectSeen != "" {
		t.Fatalf("a dry run reached LinkedIn: calls=%v exchange=%q", api.calls, tokens.subjectSeen)
	}
	if !strings.Contains(strings.ToLower(text(t, result)), "dry run") || !strings.Contains(text(t, result), "Hello LinkedIn") {
		t.Fatalf("dry run text = %q", text(t, result))
	}
}

func TestCreatePostPublishesAsTheTokenOwnerWhenConfirmed(t *testing.T) {
	tokens, api := &fakeTokens{}, &fakeLinkedIn{}
	result, _, err := (&Server{LinkedIn: api, Tokens: tokens}).CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "Hello LinkedIn", Confirm: true, DisableReshare: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if len(api.published) != 1 || api.writeToken != "member-token" {
		t.Fatalf("calls=%v", api.calls)
	}
	post := api.published[0]
	if post.Author != "urn:li:person:abc123" || post.Text != "Hello LinkedIn" || post.Visibility != linkedin.VisibilityPublic || !post.DisableReshare || post.Content != nil {
		t.Fatalf("post = %+v", post)
	}
	if !strings.Contains(text(t, result), "urn:li:share:7001") {
		t.Fatalf("result = %q", text(t, result))
	}
}

func TestAnExpiredLinkedInSessionAsksThePersonToSignInAgain(t *testing.T) {
	api := &fakeLinkedIn{profileErr: linkedin.ErrUnauthorized}
	result, _, err := (&Server{LinkedIn: api, Tokens: &fakeTokens{}}).GetProfile(context.Background(), request("mcp-bearer"), ProfileArgs{})
	if err != nil || !result.IsError || !strings.Contains(strings.ToLower(text(t, result)), "sign in again") {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestExchangeFailuresDoNotLeakDetail(t *testing.T) {
	tokens := &fakeTokens{err: errors.New("token exchange returned 500: secret-upstream-detail")}
	result, _, err := (&Server{LinkedIn: &fakeLinkedIn{}, Tokens: tokens}).GetProfile(context.Background(), request("mcp-bearer"), ProfileArgs{})
	if err != nil || !result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if strings.Contains(text(t, result), "secret-upstream-detail") {
		t.Fatalf("the exchange error leaked: %q", text(t, result))
	}
}

func TestPublishingNeedsTheWriteScope(t *testing.T) {
	tokens, api := &fakeTokens{}, &fakeLinkedIn{}
	server := &Server{LinkedIn: api, Tokens: tokens}
	result, _, err := server.CreatePost(context.Background(), requestWithScopes("mcp-bearer", "tools:read"), PostArgs{Text: "hi", Confirm: true})
	if err != nil || !result.IsError {
		t.Fatalf("err=%v result=%+v, want an error result", err, result)
	}
	if tokens.subjectSeen != "" || len(api.calls) != 0 {
		t.Fatal("a read-only token reached the exchange or LinkedIn")
	}
	// A dry run changes nothing, so a read-only token may preview.
	if preview, _, err := server.CreatePost(context.Background(), requestWithScopes("mcp-bearer", "tools:read"), PostArgs{Text: "hi"}); err != nil || preview.IsError {
		t.Fatalf("a read-only token could not preview: err=%v result=%+v", err, preview)
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

func TestAnInactiveAPIVersionTellsTheOperatorToSetTheVersion(t *testing.T) {
	logs := captureLogs(t)
	api := &fakeLinkedIn{err: &linkedin.APIError{Status: 426, Code: "NONEXISTENT_VERSION", Message: "not active", Err: linkedin.ErrVersionInactive}}
	result, _, err := (&Server{LinkedIn: api, Tokens: &fakeTokens{}}).CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "hello", Confirm: true})
	if err != nil || !result.IsError || !strings.Contains(text(t, result), "LINKEDIN_API_VERSION") {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if !strings.Contains(logs.String(), "426") || !strings.Contains(logs.String(), "NONEXISTENT_VERSION") {
		t.Fatalf("log lacks LinkedIn's status and code: %s", logs)
	}
}

func TestOtherLinkedInFailuresStayGenericButTheLogHasLinkedInsDetail(t *testing.T) {
	logs := captureLogs(t)
	api := &fakeLinkedIn{err: &linkedin.APIError{Status: 422, Code: "INVALID_FIELD", ServiceErrorCode: "65600", Message: "bad field"}}
	result, _, _ := (&Server{LinkedIn: api, Tokens: &fakeTokens{}}).CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "hello", Confirm: true})
	got := text(t, result)
	if !result.IsError || got != "LinkedIn could not complete the request." {
		t.Fatalf("result = %q", got)
	}
	for _, want := range []string{"422", "INVALID_FIELD", "65600", "bad field"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("%q missing from log: %s", want, logs)
		}
	}
}
