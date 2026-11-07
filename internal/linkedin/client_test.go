package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfileSendsTheBearerTokenAndDecodesTheMember(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/userinfo" || r.Header.Get("Authorization") != "Bearer member-token" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"sub":"abc123","name":"Ada Lovelace","picture":"https://img.example/ada.png"}`))
	}))
	defer server.Close()

	profile, err := New(server.URL, "202510").Profile(context.Background(), "member-token")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Sub != "abc123" || profile.Name != "Ada Lovelace" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestAnExpiredOrRevokedTokenIsReportedAsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"EXPIRED_ACCESS_TOKEN secret-detail"}`))
	}))
	defer server.Close()

	_, err := New(server.URL, "202510").Profile(context.Background(), "stale")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if err != nil && containsAny(err.Error(), "secret-detail") {
		t.Fatalf("the upstream body leaked into the error: %v", err)
	}
}

func TestCreatePostPublishesAsTheMemberAndReturnsThePostID(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/posts" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		for header, want := range map[string]string{
			"Authorization":             "Bearer member-token",
			"Linkedin-Version":          "202510",
			"X-Restli-Protocol-Version": "2.0.0",
			"Content-Type":              "application/json",
		} {
			if r.Header.Get(header) != want {
				t.Errorf("%s = %q, want %q", header, r.Header.Get(header), want)
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("X-Restli-Id", "urn:li:share:7001")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	id, err := New(server.URL, "202510").CreatePost(context.Background(), "member-token", "abc123", "Hello LinkedIn", VisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	if id != "urn:li:share:7001" {
		t.Fatalf("id = %q", id)
	}
	if body["author"] != "urn:li:person:abc123" || body["commentary"] != "Hello LinkedIn" || body["visibility"] != "PUBLIC" || body["lifecycleState"] != "PUBLISHED" {
		t.Fatalf("post body = %v", body)
	}
}

func TestCreatePostRejectsAnUnknownVisibility(t *testing.T) {
	if _, err := New("http://127.0.0.1:1", "202510").CreatePost(context.Background(), "t", "abc", "x", Visibility("EVERYONE")); err == nil {
		t.Fatal("an unknown visibility was accepted")
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				return true
			}
		}
	}
	return false
}
