// Package tools implements the LinkedIn MCP tools. A tool call carries the
// caller's mcp-auth access token; the server exchanges it for that person's own
// LinkedIn token and calls LinkedIn as them. No LinkedIn token is stored here.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
)

// SubjectTokenKey is the TokenInfo.Extra key under which the request's verified
// mcp-auth access token is carried to the tools. It is read only to exchange it
// and is never logged or returned.
const SubjectTokenKey = "mcp_subject_token"

// WriteScope is the OAuth scope a token needs to change anything on LinkedIn.
// A token with only the read scope can preview a write but not send it.
const WriteScope = "tools:write"

// Limits on tool inputs.
const (
	// MaxPostRunes is LinkedIn's limit for a post's text.
	MaxPostRunes = 3000
	// MaxImages is LinkedIn's limit for a multi-image post.
	MaxImages = 20
	// MaxTotalImageBytes bounds the images of one post held in memory.
	MaxTotalImageBytes = 40 << 20
	maxTitleRunes      = 400
	maxDescriptionRune = 4000
	maxAltTextRunes    = 4086
	maxPollQuestion    = 140
	maxPollOption      = 30
)

// LinkedIn is the part of the LinkedIn API the tools use.
type LinkedIn interface {
	Profile(ctx context.Context, token string) (linkedin.Profile, error)
	Publish(ctx context.Context, token string, post linkedin.Post) (string, error)
	UploadImage(ctx context.Context, token, memberID string, data []byte) (string, error)
	UploadDocument(ctx context.Context, token, memberID string, data []byte) (string, error)
	UploadVideo(ctx context.Context, token, memberID string, data []byte) (string, error)
	EditPostCommentary(ctx context.Context, token, urn, text string) error
	DeletePost(ctx context.Context, token, urn string) error
}

// TokenSource turns the caller's mcp-auth access token into that person's
// LinkedIn access token.
type TokenSource interface {
	MemberToken(ctx context.Context, subjectToken string) (string, error)
}

// Fetcher downloads a file from a public https URL, at most max bytes.
type Fetcher interface {
	Fetch(ctx context.Context, url string, max int64) ([]byte, error)
}

// Server holds the tool dependencies.
type Server struct {
	LinkedIn LinkedIn
	Tokens   TokenSource
	Fetcher  Fetcher
}

// ProfileArgs has no inputs.
type ProfileArgs struct{}

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
	lines := []string{fmt.Sprintf("Signed in to LinkedIn as %s (member %s).", profile.Name, profile.Sub)}
	if profile.GivenName != "" || profile.FamilyName != "" {
		lines = append(lines, fmt.Sprintf("Given name: %s. Family name: %s.", profile.GivenName, profile.FamilyName))
	}
	if profile.Email != "" {
		verified := "not verified"
		if profile.EmailVerified {
			verified = "verified"
		}
		lines = append(lines, fmt.Sprintf("Email: %s (%s).", profile.Email, verified))
	}
	if profile.Locale != "" {
		lines = append(lines, "Locale: "+profile.Locale+".")
	}
	if profile.Picture != "" {
		lines = append(lines, "Picture: "+profile.Picture)
	}
	return result(strings.Join(lines, "\n"), false), nil, nil
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

// gate is the safety check every write tool runs after validating its input and
// before any outbound call. It returns a result to send back (the dry run, or a
// scope error) or nil when the write may go ahead.
func gate(request *mcp.CallToolRequest, confirm bool, preview string) *mcp.CallToolResult {
	if !confirm {
		return result("Dry run, nothing was changed on LinkedIn. "+preview+"\n\nCall again with confirm: true to do it.", false)
	}
	if !hasScope(request, WriteScope) {
		return result("This needs the "+WriteScope+" scope. Reconnect with write access.", true)
	}
	return nil
}

func (s *Server) linkedInFailure(tool, subject string, err error) *mcp.CallToolResult {
	slog.Warn("tool call failed", "tool", tool, "subject", subject, "error", err.Error())
	var apiErr *linkedin.APIError
	switch {
	case errors.Is(err, linkedin.ErrUnauthorized):
		return result("Your LinkedIn session has expired. Sign in again from your MCP client.", true)
	case errors.Is(err, linkedin.ErrVersionInactive):
		return result("LinkedIn API version is no longer active; the operator must set LINKEDIN_API_VERSION to a current version.", true)
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden:
		return result("LinkedIn refused this: the signed-in person's LinkedIn access does not allow it (missing permission or not the owner). See the README for what each tool needs.", true)
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests:
		return result("LinkedIn's rate limit was reached. Try again later.", true)
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound:
		return result("LinkedIn could not find that post. Check the post URN, and that it belongs to the signed-in person.", true)
	}
	return result("LinkedIn could not complete the request.", true)
}

func result(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func fail(text string) (*mcp.CallToolResult, any, error) { return result(text, true), nil, nil }

// text trims and checks a text field. A required field must not be empty.
func checkText(name, value string, required bool, maxRunes int) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" && required {
		return "", "The " + name + " is empty."
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return "", fmt.Sprintf("The %s is longer than %d characters.", name, maxRunes)
	}
	return value, ""
}

func parseVisibility(raw string) (linkedin.Visibility, string) {
	visibility := linkedin.Visibility(strings.ToUpper(strings.TrimSpace(raw)))
	if visibility == "" {
		return linkedin.VisibilityPublic, ""
	}
	if visibility != linkedin.VisibilityPublic && visibility != linkedin.VisibilityConnections {
		return "", "Visibility must be PUBLIC or CONNECTIONS."
	}
	return visibility, ""
}

func postLink(id string) string {
	if id == "" {
		return "(LinkedIn returned no post id)"
	}
	return id + " (https://www.linkedin.com/feed/update/" + id + "/)"
}

func excerpt(text string) string {
	if text == "" {
		return "(no text)"
	}
	return text
}
