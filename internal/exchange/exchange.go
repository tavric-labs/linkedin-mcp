// Package exchange turns the caller's mcp-auth access token into that person's
// LinkedIn access token with the mcp-auth SDK's RFC 8693 token exchange. The
// server authenticates to the exchange endpoint as a resource client with
// private_key_jwt, so only this server can ask.
package exchange

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	mcpauth "github.com/Agent-Hellboy/mcp-auth/auth-client/go/mcpauth"
)

// Config describes the resource client this server is registered as.
type Config struct {
	// TokenEndpoint is the mcp-auth server's /token URL.
	TokenEndpoint string
	// Resource is this server's canonical MCP URL, the audience of the tokens it
	// accepts and the resource the client is bound to.
	Resource   string
	ClientID   string
	KeyID      string
	PrivateKey *rsa.PrivateKey
	// AllowInsecure permits an http token endpoint, for local development only.
	AllowInsecure bool
	// DialAddr, when set, is the host:port to connect to instead of the token
	// endpoint's own host. The request keeps the endpoint's URL, Host header and
	// assertion audience, which mcp-auth checks against its issuer. Use it when
	// the public issuer is not reachable from the pod, as on a local cluster.
	DialAddr string
}

// Source exchanges tokens. It holds no cache: every call is authorised afresh,
// so revoking a person's mcp-auth session stops their LinkedIn calls at once.
type Source struct {
	client *mcpauth.TokenExchangeClient
	config Config
}

// New validates config and returns a Source.
func New(config Config) (*Source, error) {
	switch {
	case config.TokenEndpoint == "":
		return nil, errors.New("token endpoint is required")
	case config.Resource == "":
		return nil, errors.New("resource is required")
	case config.ClientID == "":
		return nil, errors.New("client id is required")
	case config.KeyID == "":
		return nil, errors.New("client key id is required")
	case config.PrivateKey == nil:
		return nil, errors.New("client private key is required")
	}
	exchange := &mcpauth.TokenExchangeClient{
		Endpoint:      config.TokenEndpoint,
		AllowInsecure: config.AllowInsecure,
		ClientAuth:    &mcpauth.PrivateKeyJWTClientAuth{ClientID: config.ClientID, PrivateKey: config.PrivateKey, KeyID: config.KeyID},
	}
	if config.DialAddr != "" {
		exchange.HTTPClient = dialingClient(config.DialAddr)
	}
	return &Source{config: config, client: exchange}, nil
}

// dialingClient connects every request to addr, whatever host the URL names. It
// refuses redirects, as the SDK's own client does.
func dialingClient(addr string) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("token endpoint must not redirect") },
	}
}

// MemberToken returns the LinkedIn access token for the person who holds
// subjectToken.
func (s *Source) MemberToken(ctx context.Context, subjectToken string) (string, error) {
	token, err := s.client.ExchangeContext(ctx, subjectToken, s.config.Resource, nil)
	if err != nil {
		return "", fmt.Errorf("exchange for the member's LinkedIn token: %w", err)
	}
	if token.AccessToken == "" {
		return "", errors.New("exchange returned no access token")
	}
	return token.AccessToken, nil
}
