package exchange

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestMemberTokenExchangesTheCallersTokenForTheLinkedInToken(t *testing.T) {
	var form map[string]string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = map[string]string{}
		for key := range r.PostForm {
			form[key] = r.PostForm.Get(key)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "linkedin-member-token", "token_type": "Bearer", "expires_in": 3600, "issued_token_type": "urn:ietf:params:oauth:token-type:access_token"})
	}))
	defer endpoint.Close()

	source, err := New(Config{TokenEndpoint: endpoint.URL + "/token", Resource: "https://mcp.example/linkedin/mcp", ClientID: "linkedin-mcp", KeyID: "k1", PrivateKey: testKey(t), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	token, err := source.MemberToken(context.Background(), "mcp-bearer")
	if err != nil {
		t.Fatal(err)
	}
	if token != "linkedin-member-token" {
		t.Fatalf("token = %q", token)
	}
	if form["subject_token"] != "mcp-bearer" || form["audience"] != "https://mcp.example/linkedin/mcp" || form["client_id"] != "linkedin-mcp" {
		t.Fatalf("exchange form = %v", form)
	}
	if !strings.HasPrefix(form["client_assertion"], "ey") || form["client_assertion_type"] != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
		t.Fatalf("the exchange was not authenticated with private_key_jwt: %v", form)
	}
}

func TestEveryCallIsAuthorisedAfresh(t *testing.T) {
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer endpoint.Close()
	source, err := New(Config{TokenEndpoint: endpoint.URL, Resource: "https://mcp.example/linkedin/mcp", ClientID: "c", KeyID: "k", PrivateKey: testKey(t), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := source.MemberToken(context.Background(), "same-bearer"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("token endpoint calls = %d, want 2: a cache would outlive a revoked session", calls)
	}
}

func TestExchangeRefusesAnInsecureEndpointByDefault(t *testing.T) {
	source, err := New(Config{TokenEndpoint: "http://auth.example/token", Resource: "https://mcp.example/linkedin/mcp", ClientID: "c", KeyID: "k", PrivateKey: testKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.MemberToken(context.Background(), "bearer"); err == nil {
		t.Fatal("an http token endpoint was used")
	}
}

func TestNewRequiresEveryField(t *testing.T) {
	good := Config{TokenEndpoint: "https://auth.example/token", Resource: "https://mcp.example/linkedin/mcp", ClientID: "c", KeyID: "k", PrivateKey: testKey(t)}
	for name, mutate := range map[string]func(*Config){
		"endpoint": func(c *Config) { c.TokenEndpoint = "" },
		"resource": func(c *Config) { c.Resource = "" },
		"client":   func(c *Config) { c.ClientID = "" },
		"key id":   func(c *Config) { c.KeyID = "" },
		"key":      func(c *Config) { c.PrivateKey = nil },
	} {
		config := good
		mutate(&config)
		if _, err := New(config); err == nil {
			t.Errorf("accepted a config without %s", name)
		}
	}
}
