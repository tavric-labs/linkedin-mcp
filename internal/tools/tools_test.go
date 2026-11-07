package tools

import (
	"context"
	"errors"
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
	postCalls    int
	post         struct {
		token, member, text string
		visibility          linkedin.Visibility
	}
}

func (f *fakeLinkedIn) Profile(_ context.Context, token string) (linkedin.Profile, error) {
	f.profileToken = token
	if f.profileErr != nil {
		return linkedin.Profile{}, f.profileErr
	}
	return linkedin.Profile{Sub: "abc123", Name: "Ada Lovelace"}, nil
}

func (f *fakeLinkedIn) CreatePost(_ context.Context, token, member, text string, visibility linkedin.Visibility) (string, error) {
	f.postCalls++
	f.post.token, f.post.member, f.post.text, f.post.visibility = token, member, text, visibility
	return "urn:li:share:7001", nil
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
	if api.postCalls != 0 || tokens.subjectSeen != "" {
		t.Fatalf("a dry run reached LinkedIn: posts=%d exchange=%q", api.postCalls, tokens.subjectSeen)
	}
	if !strings.Contains(strings.ToLower(text(t, result)), "dry run") || !strings.Contains(text(t, result), "Hello LinkedIn") {
		t.Fatalf("dry run text = %q", text(t, result))
	}
}

func TestCreatePostPublishesAsTheTokenOwnerWhenConfirmed(t *testing.T) {
	tokens, api := &fakeTokens{}, &fakeLinkedIn{}
	result, _, err := (&Server{LinkedIn: api, Tokens: tokens}).CreatePost(context.Background(), request("mcp-bearer"), PostArgs{Text: "Hello LinkedIn", Confirm: true})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if api.postCalls != 1 || api.post.token != "member-token" || api.post.member != "abc123" || api.post.text != "Hello LinkedIn" || api.post.visibility != linkedin.VisibilityPublic {
		t.Fatalf("post = %+v calls=%d", api.post, api.postCalls)
	}
	if !strings.Contains(text(t, result), "urn:li:share:7001") {
		t.Fatalf("result = %q", text(t, result))
	}
}

func TestCreatePostValidatesBeforeAnythingElse(t *testing.T) {
	long := strings.Repeat("x", MaxPostRunes+1)
	for name, args := range map[string]PostArgs{
		"empty":          {Text: "  \n", Confirm: true},
		"too long":       {Text: long, Confirm: true},
		"bad visibility": {Text: "hi", Visibility: "EVERYONE", Confirm: true},
	} {
		tokens, api := &fakeTokens{}, &fakeLinkedIn{}
		result, _, err := (&Server{LinkedIn: api, Tokens: tokens}).CreatePost(context.Background(), request("mcp-bearer"), args)
		if err != nil || !result.IsError {
			t.Errorf("%s: err=%v result=%+v, want an error result", name, err, result)
		}
		if tokens.subjectSeen != "" || api.postCalls != 0 {
			t.Errorf("%s: invalid input reached the exchange or LinkedIn", name)
		}
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
	if tokens.subjectSeen != "" || api.postCalls != 0 {
		t.Fatal("a read-only token reached the exchange or LinkedIn")
	}
	// A dry run changes nothing, so a read-only token may preview.
	if preview, _, err := server.CreatePost(context.Background(), requestWithScopes("mcp-bearer", "tools:read"), PostArgs{Text: "hi"}); err != nil || preview.IsError {
		t.Fatalf("a read-only token could not preview: err=%v result=%+v", err, preview)
	}
}
