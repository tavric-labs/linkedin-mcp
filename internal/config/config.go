// Package config reads the server's settings from the environment.
package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config is everything the server needs to start.
type Config struct {
	Port, MCPPath string
	// Resource is the canonical public MCP URL (the audience of mcp-auth tokens).
	Resource string
	// Issuer is the mcp-auth issuer URL. JWKSURL is discovered from it unless set.
	Issuer, JWKSURL string
	TokenEndpoint   string
	// TokenDialAddr optionally makes the token exchange connect to this host:port
	// instead of the issuer host, for clusters where the public issuer is not reachable.
	TokenDialAddr string
	// TokenDialStripPrefix is the issuer URL's path, removed from the exchange request
	// when TokenDialAddr is used, the way a public ingress would before the auth server.
	TokenDialStripPrefix string
	ClientID, KeyID      string
	PrivateKey           *rsa.PrivateKey
	APIBase              string
	APIVersion           string
	// AllowInsecure permits http issuer and token endpoints, for local development.
	AllowInsecure bool
}

// Load reads and validates the configuration. getenv is os.Getenv in production.
func Load(getenv func(string) string) (Config, error) {
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}
	config := Config{
		Port:          value("PORT", "8080"),
		MCPPath:       value("MCP_PATH", "/mcp"),
		Resource:      value("LINKEDIN_MCP_RESOURCE", ""),
		Issuer:        strings.TrimRight(value("LINKEDIN_MCP_ISSUER", ""), "/"),
		JWKSURL:       value("LINKEDIN_JWKS_URL", ""),
		TokenDialAddr: value("LINKEDIN_MCP_TOKEN_DIAL_ADDR", ""),
		ClientID:      value("LINKEDIN_MCP_CLIENT_ID", ""),
		KeyID:         value("LINKEDIN_MCP_CLIENT_KEY_ID", ""),
		APIBase:       strings.TrimRight(value("LINKEDIN_API_BASE", "https://api.linkedin.com"), "/"),
		APIVersion:    value("LINKEDIN_API_VERSION", "202510"),
		AllowInsecure: value("LINKEDIN_MCP_ALLOW_INSECURE", "") == "true",
	}
	for name, v := range map[string]string{
		"LINKEDIN_MCP_RESOURCE": config.Resource, "LINKEDIN_MCP_ISSUER": config.Issuer,
		"LINKEDIN_MCP_CLIENT_ID": config.ClientID, "LINKEDIN_MCP_CLIENT_KEY_ID": config.KeyID,
	} {
		if strings.TrimSpace(v) == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	if err := absoluteURL("LINKEDIN_MCP_RESOURCE", config.Resource, config.AllowInsecure); err != nil {
		return Config{}, err
	}
	if err := absoluteURL("LINKEDIN_MCP_ISSUER", config.Issuer, config.AllowInsecure); err != nil {
		return Config{}, err
	}
	// The LinkedIn API itself is always https.
	if err := absoluteURL("LINKEDIN_API_BASE", config.APIBase, false); err != nil {
		return Config{}, err
	}
	config.TokenEndpoint = config.Issuer + "/token"
	if config.TokenDialAddr != "" {
		if issuerURL, err := url.Parse(config.Issuer); err == nil {
			config.TokenDialStripPrefix = strings.TrimRight(issuerURL.Path, "/")
		}
	}
	key, err := clientKey(getenv("LINKEDIN_MCP_CLIENT_KEY"), getenv("LINKEDIN_MCP_CLIENT_KEY_FILE"))
	if err != nil {
		return Config{}, err
	}
	config.PrivateKey = key
	return config, nil
}

// clientKey reads the resource client's RSA key from exactly one source: the
// PEM itself (LINKEDIN_MCP_CLIENT_KEY, how a Kubernetes secret reaches an
// MCP server) or a file (LINKEDIN_MCP_CLIENT_KEY_FILE, for local runs).
func clientKey(inline, path string) (*rsa.PrivateKey, error) {
	inline, path = strings.TrimSpace(inline), strings.TrimSpace(path)
	switch {
	case inline != "" && path != "":
		return nil, errors.New("set LINKEDIN_MCP_CLIENT_KEY or LINKEDIN_MCP_CLIENT_KEY_FILE, not both")
	case inline != "":
		return parseKey([]byte(inline))
	case path != "":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read the client key file: %w", err)
		}
		return parseKey(data)
	}
	return nil, errors.New("LINKEDIN_MCP_CLIENT_KEY or LINKEDIN_MCP_CLIENT_KEY_FILE is required")
}

func absoluteURL(name, raw string, allowHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return fmt.Errorf("%s must be an absolute https URL", name)
	}
	return nil
}

func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("the client key is not PEM")
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if key, ok := parsed.(*rsa.PrivateKey); ok {
			return key, nil
		}
		return nil, errors.New("the client key must be an RSA key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("the client key is not a PKCS#8 or PKCS#1 RSA key")
	}
	return key, nil
}
