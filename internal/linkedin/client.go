// Package linkedin is a small client for the two LinkedIn calls this server
// makes on behalf of a signed-in member: reading their OpenID profile and
// publishing a post. It never stores a token; the caller passes one per call.
package linkedin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnauthorized means LinkedIn rejected the member's token: it expired
// (self-serve tokens last about 60 days and cannot be refreshed) or was
// revoked. The member has to sign in again.
var ErrUnauthorized = errors.New("linkedin rejected the member's token")

// Visibility is who may see a post.
type Visibility string

const (
	VisibilityPublic      Visibility = "PUBLIC"
	VisibilityConnections Visibility = "CONNECTIONS"
)

// Profile is the OpenID Connect userinfo LinkedIn returns for a member.
type Profile struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// Client calls the LinkedIn API. BaseURL and Version are injectable so tests
// and staging can point at another host or API version.
type Client struct {
	baseURL string
	version string
	http    *http.Client
}

// New returns a client for the API at baseURL, sending version as the
// Linkedin-Version header on versioned (/rest) calls.
func New(baseURL, version string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		version: version,
		http: &http.Client{
			Timeout: 15 * time.Second,
			// A token must never follow a redirect to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("linkedin api must not redirect") },
		},
	}
}

// Profile returns the member the token belongs to.
func (c *Client) Profile(ctx context.Context, token string) (Profile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/userinfo", nil)
	if err != nil {
		return Profile{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return Profile{}, fmt.Errorf("linkedin userinfo: %w", err)
	}
	defer response.Body.Close()
	if err := statusError(response); err != nil {
		return Profile{}, err
	}
	var profile Profile
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("decode linkedin userinfo: %w", err)
	}
	if profile.Sub == "" {
		return Profile{}, errors.New("linkedin userinfo has no member id")
	}
	return profile, nil
}

// CreatePost publishes text as the member whose id is memberID and returns the
// new post's URN.
func (c *Client) CreatePost(ctx context.Context, token, memberID, text string, visibility Visibility) (string, error) {
	if visibility != VisibilityPublic && visibility != VisibilityConnections {
		return "", fmt.Errorf("visibility must be %s or %s", VisibilityPublic, VisibilityConnections)
	}
	body, err := json.Marshal(map[string]any{
		"author":         "urn:li:person:" + url.PathEscape(memberID),
		"commentary":     text,
		"visibility":     string(visibility),
		"lifecycleState": "PUBLISHED",
		"distribution": map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		"isReshareDisabledByAuthor": false,
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rest/posts", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Linkedin-Version", c.version)
	request.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("linkedin create post: %w", err)
	}
	defer response.Body.Close()
	if err := statusError(response); err != nil {
		return "", err
	}
	return response.Header.Get("X-Restli-Id"), nil
}

// statusError turns a non-2xx response into an error that carries only the
// status, never LinkedIn's response body, which can echo request details.
func statusError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return fmt.Errorf("linkedin returned HTTP %d", response.StatusCode)
}
