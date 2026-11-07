package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpauth "github.com/Agent-Hellboy/mcp-auth/auth-client/go/mcpauth"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/tavric-labs/linkedin-mcp/internal/tools"
)

func TestTokenInfoCarriesTheSubjectScopesAndTheRawBearer(t *testing.T) {
	expires := float64(time.Now().Add(time.Hour).Unix())
	ctx := mcpauth.WithTokenClaims(context.Background(), mcpauth.TokenClaims{
		Subject: "linkedin:abc123", Scopes: map[string]bool{"tools:read": true, "tools:write": true},
		Raw: map[string]any{"exp": expires, "sid": "session-1"},
	})
	info, err := TokenInfo(ctx, "raw-bearer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.UserID != "linkedin:abc123" || len(info.Scopes) != 2 || info.Expiration.Unix() != int64(expires) {
		t.Fatalf("info = %+v", info)
	}
	if info.Extra[tools.SubjectTokenKey] != "raw-bearer" || info.Extra["sid"] != "session-1" {
		t.Fatalf("extra = %v", info.Extra)
	}
}

func TestTokenInfoRefusesAnUnverifiedRequest(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no claims":  context.Background(),
		"no subject": mcpauth.WithTokenClaims(context.Background(), mcpauth.TokenClaims{Raw: map[string]any{"exp": float64(1)}}),
		"no expiry":  mcpauth.WithTokenClaims(context.Background(), mcpauth.TokenClaims{Subject: "u"}),
	} {
		if _, err := TokenInfo(ctx, "raw", nil); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestTokenInfoDoesNotMutateTheVerifiedClaims(t *testing.T) {
	raw := map[string]any{"exp": float64(time.Now().Add(time.Hour).Unix())}
	ctx := mcpauth.WithTokenClaims(context.Background(), mcpauth.TokenClaims{Subject: "u", Raw: raw})
	if _, err := TokenInfo(ctx, "raw-bearer", nil); err != nil {
		t.Fatal(err)
	}
	if _, leaked := raw[tools.SubjectTokenKey]; leaked {
		t.Fatal("the raw bearer was written into the shared claims map")
	}
}
