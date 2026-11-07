// Package tools implements the LinkedIn MCP tools. A tool call carries the
// caller's mcp-auth access token; the server exchanges it for that person's own
// LinkedIn token and calls LinkedIn as them. No LinkedIn token is stored here.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
)

// SubjectTokenKey is the TokenInfo.Extra key under which the request's verified
// mcp-auth access token is carried to the tools. It is read only to exchange it
// and is never logged or returned.
const SubjectTokenKey = "mcp_subject_token"

// WriteScope is the OAuth scope a token needs to publish. A token with only the
// read scope can preview a post but not send it.
const WriteScope = "tools:write"

// MaxPostRunes is LinkedIn's limit for a post's text.
const MaxPostRunes = 3000

// LinkedIn is the part of the LinkedIn API the tools use.
type LinkedIn interface {
	Profile(ctx context.Context, token string) (linkedin.Profile, error)
	CreatePost(ctx context.Context, token, memberID, text string, visibility linkedin.Visibility) (string, error)
}

// TokenSource turns the caller's mcp-auth access token into that person's
// LinkedIn access token.
type TokenSource interface {
	MemberToken(ctx context.Context, subjectToken string) (string, error)
}

// Server holds the tool dependencies.
type Server struct {
	LinkedIn LinkedIn
	Tokens   TokenSource
}

// ProfileArgs has no inputs.
type ProfileArgs struct{}

// PostArgs are the inputs of linkedin_create_post.
type PostArgs struct {
	Text       string `json:"text" jsonschema:"the post text, up to 3000 characters"`
	Visibility string `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm    bool   `json:"confirm,omitempty" jsonschema:"set true to publish; without it the call is a dry run that publishes nothing"`
}

// GetProfile returns the signed-in member's LinkedIn profile.
func (s *Server) GetProfile(ctx context.Context, request *mcp.CallToolRequest, _ ProfileArgs) (*mcp.CallToolResult, any, error) {
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	profile, err := s.LinkedIn.Profile(ctx, token)
	if err != nil {
		return s.linkedInFailure("linkedin_get_profile", subject, err), nil, nil
	}
	slog.Info("tool call", "tool", "linkedin_get_profile", "subject", subject, "outcome", "success")
	return result(fmt.Sprintf("Signed in to LinkedIn as %s (member %s).", profile.Name, profile.Sub), false), nil, nil
}

// CreatePost publishes a post as the signed-in member, or previews it.
func (s *Server) CreatePost(ctx context.Context, request *mcp.CallToolRequest, args PostArgs) (*mcp.CallToolResult, any, error) {
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return result("The post text is empty.", true), nil, nil
	}
	if utf8.RuneCountInString(text) > MaxPostRunes {
		return result(fmt.Sprintf("The post is longer than LinkedIn's %d characters.", MaxPostRunes), true), nil, nil
	}
	visibility := linkedin.Visibility(strings.ToUpper(strings.TrimSpace(args.Visibility)))
	if visibility == "" {
		visibility = linkedin.VisibilityPublic
	}
	if visibility != linkedin.VisibilityPublic && visibility != linkedin.VisibilityConnections {
		return result("Visibility must be PUBLIC or CONNECTIONS.", true), nil, nil
	}
	if !args.Confirm {
		return result(fmt.Sprintf("Dry run, nothing was posted. This would publish to LinkedIn (%s):\n\n%s\n\nCall again with confirm: true to publish.", visibility, text), false), nil, nil
	}
	if !hasScope(request, WriteScope) {
		return result("Publishing needs the "+WriteScope+" scope. Reconnect with write access.", true), nil, nil
	}
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	profile, err := s.LinkedIn.Profile(ctx, token)
	if err != nil {
		return s.linkedInFailure("linkedin_create_post", subject, err), nil, nil
	}
	id, err := s.LinkedIn.CreatePost(ctx, token, profile.Sub, text, visibility)
	if err != nil {
		return s.linkedInFailure("linkedin_create_post", subject, err), nil, nil
	}
	slog.Info("tool call", "tool", "linkedin_create_post", "subject", subject, "outcome", "success", "post", id, "characters", utf8.RuneCountInString(text))
	return result(fmt.Sprintf("Posted to LinkedIn as %s: %s", profile.Name, id), false), nil, nil
}

// memberToken exchanges the request's mcp-auth token for the member's LinkedIn
// token. On failure it returns a ready tool error result.
func (s *Server) memberToken(ctx context.Context, request *mcp.CallToolRequest) (subject, token string, failure *mcp.CallToolResult) {
	var info map[string]any
	if request != nil && request.Extra != nil && request.Extra.TokenInfo != nil {
		subject = request.Extra.TokenInfo.UserID
		info = request.Extra.TokenInfo.Extra
	}
	subjectToken, _ := info[SubjectTokenKey].(string)
	if subjectToken == "" {
		return subject, "", result("This call is not authenticated.", true)
	}
	token, err := s.Tokens.MemberToken(ctx, subjectToken)
	if err != nil {
		slog.Warn("linkedin token exchange failed", "subject", subject, "error", err.Error())
		return subject, "", result("Could not get your LinkedIn session. Sign in again from your MCP client.", true)
	}
	return subject, token, nil
}

func hasScope(request *mcp.CallToolRequest, scope string) bool {
	if request == nil || request.Extra == nil || request.Extra.TokenInfo == nil {
		return false
	}
	for _, have := range request.Extra.TokenInfo.Scopes {
		if have == scope {
			return true
		}
	}
	return false
}

func (s *Server) linkedInFailure(tool, subject string, err error) *mcp.CallToolResult {
	slog.Warn("tool call failed", "tool", tool, "subject", subject, "error", err.Error())
	if errors.Is(err, linkedin.ErrUnauthorized) {
		return result("Your LinkedIn session has expired. Sign in again from your MCP client.", true)
	}
	return result("LinkedIn could not complete the request.", true)
}

func result(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
