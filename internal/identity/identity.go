// Package identity connects the mcp-auth middleware to the MCP SDK: it turns the
// claims the middleware verified into the TokenInfo the tools read, and carries
// the raw bearer token with them so a tool can exchange it.
package identity

import (
	"context"
	"net/http"
	"time"

	mcpauth "github.com/Agent-Hellboy/mcp-auth/auth-client/go/mcpauth"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/tavric-labs/linkedin-mcp/internal/tools"
)

// TokenInfo is an auth.TokenVerifier for the MCP SDK. The mcp-auth middleware
// has already verified the JWT and put its claims in ctx; this refuses a
// request that has no verified subject or expiry, so a tool never runs
// unauthenticated.
func TokenInfo(ctx context.Context, bearer string, _ *http.Request) (*auth.TokenInfo, error) {
	claims, ok := mcpauth.TokenClaimsFromContext(ctx)
	expires, hasExpiry := claims.Raw["exp"].(float64)
	if !ok || claims.Subject == "" || !hasExpiry {
		return nil, auth.ErrInvalidToken
	}
	scopes := make([]string, 0, len(claims.Scopes))
	for scope, granted := range claims.Scopes {
		if granted {
			scopes = append(scopes, scope)
		}
	}
	// Copy the claims: the map belongs to the verifier.
	extra := make(map[string]any, len(claims.Raw)+1)
	for key, value := range claims.Raw {
		extra[key] = value
	}
	extra[tools.SubjectTokenKey] = bearer
	return &auth.TokenInfo{UserID: claims.Subject, Scopes: scopes, Expiration: time.Unix(int64(expires), 0), Extra: extra}, nil
}
