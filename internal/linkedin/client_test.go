package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPublishPublishesAsTheMemberAndReturnsThePostID(t *testing.T) {
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

	id, err := New(server.URL, "202510").Publish(context.Background(), "member-token", Post{Author: PersonURN("abc123"), Text: "Hello LinkedIn", Visibility: VisibilityPublic})
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

func TestPublishRejectsAnUnknownVisibility(t *testing.T) {
	if _, err := New("http://127.0.0.1:1", "202510").Publish(context.Background(), "t", Post{Author: PersonURN("abc"), Text: "x", Visibility: Visibility("EVERYONE")}); err == nil {
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

func postError(t *testing.T, status int, body string) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	_, err := New(server.URL, "202610").Publish(context.Background(), "tok-secret", Post{Author: PersonURN("abc"), Text: "private post text", Visibility: VisibilityPublic})
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func TestPublishErrorCarriesLinkedInsStatusAndErrorFields(t *testing.T) {
	err := postError(t, http.StatusUnprocessableEntity,
		`{"status":422,"code":"INVALID_FIELD","serviceErrorCode":65600,"message":"bad field","echo":"private post text tok-secret"}`)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 422 {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"422", "INVALID_FIELD", "65600", "bad field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing from %q", want, err.Error())
		}
	}
	for _, leak := range []string{"private post text", "tok-secret"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("%q leaked into %q", leak, err.Error())
		}
	}
}

func TestPublishErrorTruncatesLinkedInsMessage(t *testing.T) {
	err := postError(t, http.StatusBadRequest, `{"message":"`+strings.Repeat("x", 1000)+`"}`)
	if len(err.Error()) > 400 {
		t.Fatalf("error is %d bytes", len(err.Error()))
	}
}

func TestPublishErrorToleratesANonJSONBody(t *testing.T) {
	err := postError(t, http.StatusBadGateway, "<html>oops</html>")
	if !strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "oops") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTP426MeansTheAPIVersionIsNoLongerActive(t *testing.T) {
	err := postError(t, http.StatusUpgradeRequired, `{"status":426,"code":"NONEXISTENT_VERSION","message":"Requested version 20251001 is not active"}`)
	if !errors.Is(err, ErrVersionInactive) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "NONEXISTENT_VERSION") {
		t.Fatalf("LinkedIn's code is missing from %q", err.Error())
	}
}
