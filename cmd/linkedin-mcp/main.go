// Command linkedin-mcp is an MCP server that lets a signed-in person read their
// LinkedIn profile and publish posts. It trusts mcp-auth access tokens and
// exchanges each one for that person's own LinkedIn token.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mcpauth "github.com/Agent-Hellboy/mcp-auth/auth-client/go/mcpauth"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/config"
	"github.com/tavric-labs/linkedin-mcp/internal/exchange"
	"github.com/tavric-labs/linkedin-mcp/internal/identity"
	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
	"github.com/tavric-labs/linkedin-mcp/internal/tools"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	jwksURL := cfg.JWKSURL
	if jwksURL == "" {
		metadata, err := mcpauth.DiscoverAuthorizationServer(&http.Client{Timeout: 10 * time.Second}, cfg.Issuer)
		if err != nil {
			return fmt.Errorf("discover the authorization server: %w", err)
		}
		jwksURL = metadata.JWKSURI
	}
	verifier := &mcpauth.JWTVerifier{
		JWKSURL:        jwksURL,
		Issuer:         cfg.Issuer,
		Audience:       cfg.Resource,
		RequiredScopes: map[string]bool{"tools:read": true},
	}
	tokens, err := exchange.New(exchange.Config{
		TokenEndpoint: cfg.TokenEndpoint, Resource: cfg.Resource, ClientID: cfg.ClientID,
		KeyID: cfg.KeyID, PrivateKey: cfg.PrivateKey, AllowInsecure: cfg.AllowInsecure,
	})
	if err != nil {
		return err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "linkedin-mcp", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Read the signed-in person's LinkedIn profile and publish posts as them. Posts are a dry run unless confirm is true.",
	})
	handlers := &tools.Server{LinkedIn: linkedin.New(cfg.APIBase, cfg.APIVersion), Tokens: tokens}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_get_profile",
		Description: "Return the signed-in person's LinkedIn name and member id. Read only.",
	}, handlers.GetProfile)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_post",
		Description: "Publish a LinkedIn post as the signed-in person. Without confirm: true it only previews the post.",
	}, handlers.CreatePost)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	metadataURL, err := protectedResourceMetadataURL(cfg.Resource)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	})
	protected := mcpauth.RequireToken(verifier, mcpauth.ResourceMetadata{URL: metadataURL}, auth.RequireBearerToken(identity.TokenInfo, nil)(mcpHandler))
	mux.Handle(cfg.MCPPath, protected)
	metadata := mcpauth.ProtectedResourceMetadataHandler(verifier, cfg.Resource, cfg.Issuer)
	mux.Handle("/.well-known/oauth-protected-resource", metadata)
	mux.Handle("/.well-known/oauth-protected-resource"+cfg.MCPPath, metadata)

	httpServer := &http.Server{
		Addr: ":" + cfg.Port, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Printf("linkedin-mcp listening on :%s at %s", cfg.Port, cfg.MCPPath)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func protectedResourceMetadataURL(resource string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(resource))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("LINKEDIN_MCP_RESOURCE must be an absolute URL")
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/.well-known/oauth-protected-resource" + strings.TrimRight(parsed.Path, "/")}).String(), nil
}
