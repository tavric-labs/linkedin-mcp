package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, key)})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func baseEnv(t *testing.T) map[string]string {
	return map[string]string{
		"LINKEDIN_MCP_RESOURCE":        "https://mcp.example.org/linkedin/mcp",
		"LINKEDIN_MCP_ISSUER":          "https://auth.example.org/mcp-auth",
		"LINKEDIN_MCP_CLIENT_ID":       "linkedin-mcp",
		"LINKEDIN_MCP_CLIENT_KEY_ID":   "k1",
		"LINKEDIN_MCP_CLIENT_KEY_FILE": writeKey(t),
	}
}

func TestLoadAppliesDefaultsAndDerivesTheEndpoints(t *testing.T) {
	config, err := Load(env(baseEnv(t)))
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != "8080" || config.MCPPath != "/mcp" || config.APIBase != "https://api.linkedin.com" || config.APIVersion == "" {
		t.Fatalf("defaults = %+v", config)
	}
	if config.TokenEndpoint != "https://auth.example.org/mcp-auth/token" {
		t.Fatalf("token endpoint = %q", config.TokenEndpoint)
	}
	if config.PrivateKey == nil {
		t.Fatal("the client key was not loaded")
	}
	if config.APIVersion != "202610" {
		t.Fatalf("default API version = %q", config.APIVersion)
	}
}

func TestLoadRejectsMissingOrInvalidSettings(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"resource":      func(e map[string]string) { delete(e, "LINKEDIN_MCP_RESOURCE") },
		"issuer":        func(e map[string]string) { delete(e, "LINKEDIN_MCP_ISSUER") },
		"client id":     func(e map[string]string) { delete(e, "LINKEDIN_MCP_CLIENT_ID") },
		"key file":      func(e map[string]string) { delete(e, "LINKEDIN_MCP_CLIENT_KEY_FILE") },
		"unreadable":    func(e map[string]string) { e["LINKEDIN_MCP_CLIENT_KEY_FILE"] = "/no/such/key.pem" },
		"relative":      func(e map[string]string) { e["LINKEDIN_MCP_RESOURCE"] = "mcp/linkedin" },
		"http issuer":   func(e map[string]string) { e["LINKEDIN_MCP_ISSUER"] = "http://auth.example.org/mcp-auth" },
		"http api base": func(e map[string]string) { e["LINKEDIN_API_BASE"] = "http://api.linkedin.com" },
	} {
		values := baseEnv(t)
		mutate(values)
		if _, err := Load(env(values)); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
}

func TestLoadAllowsPlainHTTPOnlyForLocalDevelopment(t *testing.T) {
	values := baseEnv(t)
	values["LINKEDIN_MCP_ISSUER"] = "http://localhost:8080"
	values["LINKEDIN_MCP_ALLOW_INSECURE"] = "true"
	config, err := Load(env(values))
	if err != nil || !config.AllowInsecure {
		t.Fatalf("local development was refused: %v", err)
	}
	if !strings.HasPrefix(config.TokenEndpoint, "http://localhost:8080") {
		t.Fatalf("token endpoint = %q", config.TokenEndpoint)
	}
}

func TestLoadReadsTheClientKeyFromTheEnvironment(t *testing.T) {
	values := baseEnv(t)
	pemBytes, err := os.ReadFile(values["LINKEDIN_MCP_CLIENT_KEY_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	delete(values, "LINKEDIN_MCP_CLIENT_KEY_FILE")
	values["LINKEDIN_MCP_CLIENT_KEY"] = string(pemBytes)
	config, err := Load(env(values))
	if err != nil || config.PrivateKey == nil {
		t.Fatalf("an inline key was refused: %v", err)
	}
	// Both at once is ambiguous.
	values["LINKEDIN_MCP_CLIENT_KEY_FILE"] = writeKey(t)
	if _, err := Load(env(values)); err == nil {
		t.Fatal("a key file and an inline key were both accepted")
	}
}

func TestOrganizationToolsAreOffUnlessEnabled(t *testing.T) {
	values := baseEnv(t)
	config, err := Load(env(values))
	if err != nil || config.EnableOrgTools {
		t.Fatalf("default: err=%v enabled=%v", err, config.EnableOrgTools)
	}
	for value, want := range map[string]bool{"true": true, "false": false, "1": false, "yes": false} {
		values["LINKEDIN_MCP_ENABLE_ORG_TOOLS"] = value
		config, err := Load(env(values))
		if err != nil || config.EnableOrgTools != want {
			t.Errorf("%q: err=%v enabled=%v, want %v", value, err, config.EnableOrgTools, want)
		}
	}
}
