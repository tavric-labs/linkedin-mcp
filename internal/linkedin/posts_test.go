package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// recorded is one request a fake LinkedIn saw.
type recorded struct {
	method, path, rawURI, query string
	header                      http.Header
	body                        []byte
}

func fakeLinkedIn(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, rec recorded)) (*Client, *[]recorded) {
	t.Helper()
	var seen []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recorded{r.Method, r.URL.Path, r.RequestURI, r.URL.RawQuery, r.Header.Clone(), body}
		seen = append(seen, rec)
		handler(w, r, rec)
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, "202610")
	client.allowUploadURL = func(*url.URL) bool { return true } // the fake is plain http on loopback
	return client, &seen
}

func created(id string) func(http.ResponseWriter, *http.Request, recorded) {
	return func(w http.ResponseWriter, _ *http.Request, _ recorded) {
		w.Header().Set("X-Restli-Id", id)
		w.WriteHeader(http.StatusCreated)
	}
}

func decode(t *testing.T, rec recorded) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.body, &out); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.body)
	}
	return out
}

func requireVersionedHeaders(t *testing.T, rec recorded) {
	t.Helper()
	if rec.header.Get("Authorization") != "Bearer member-token" || rec.header.Get("Linkedin-Version") != "202610" || rec.header.Get("X-Restli-Protocol-Version") != "2.0.0" {
		t.Errorf("headers = %v", rec.header)
	}
}

func TestPublishContentShapes(t *testing.T) {
	cases := map[string]struct {
		post  Post
		check func(t *testing.T, body map[string]any)
	}{
		"article": {
			Post{Content: ArticleContent("https://example.com/a", "Title", "Desc", "urn:li:image:T1")},
			func(t *testing.T, b map[string]any) {
				a := b["content"].(map[string]any)["article"].(map[string]any)
				if a["source"] != "https://example.com/a" || a["title"] != "Title" || a["description"] != "Desc" || a["thumbnail"] != "urn:li:image:T1" {
					t.Fatalf("article = %v", a)
				}
			},
		},
		"article without optional fields": {
			Post{Content: ArticleContent("https://example.com/a", "Title", "", "")},
			func(t *testing.T, b map[string]any) {
				a := b["content"].(map[string]any)["article"].(map[string]any)
				if _, ok := a["description"]; ok {
					t.Fatalf("empty description was sent: %v", a)
				}
				if _, ok := a["thumbnail"]; ok {
					t.Fatalf("empty thumbnail was sent: %v", a)
				}
			},
		},
		"single image": {
			Post{Content: ImageContent([]Image{{URN: "urn:li:image:I1", AltText: "alt"}})},
			func(t *testing.T, b map[string]any) {
				m := b["content"].(map[string]any)["media"].(map[string]any)
				if m["id"] != "urn:li:image:I1" || m["altText"] != "alt" {
					t.Fatalf("media = %v", m)
				}
			},
		},
		"multi image": {
			Post{Content: ImageContent([]Image{{URN: "urn:li:image:I1"}, {URN: "urn:li:image:I2", AltText: "two"}})},
			func(t *testing.T, b map[string]any) {
				images := b["content"].(map[string]any)["multiImage"].(map[string]any)["images"].([]any)
				if len(images) != 2 || images[1].(map[string]any)["id"] != "urn:li:image:I2" || images[1].(map[string]any)["altText"] != "two" {
					t.Fatalf("images = %v", images)
				}
			},
		},
		"video": {
			Post{Content: MediaContent("urn:li:video:V1", "Demo")},
			func(t *testing.T, b map[string]any) {
				m := b["content"].(map[string]any)["media"].(map[string]any)
				if m["id"] != "urn:li:video:V1" || m["title"] != "Demo" {
					t.Fatalf("media = %v", m)
				}
			},
		},
		"poll": {
			Post{Content: PollContent("Tabs or spaces?", []string{"Tabs", "Spaces"}, PollThreeDays)},
			func(t *testing.T, b map[string]any) {
				p := b["content"].(map[string]any)["poll"].(map[string]any)
				options := p["options"].([]any)
				if p["question"] != "Tabs or spaces?" || len(options) != 2 || options[0].(map[string]any)["text"] != "Tabs" || p["settings"].(map[string]any)["duration"] != "THREE_DAYS" {
					t.Fatalf("poll = %v", p)
				}
			},
		},
		"reshare": {
			Post{ReshareParent: "urn:li:share:123"},
			func(t *testing.T, b map[string]any) {
				if b["reshareContext"].(map[string]any)["parent"] != "urn:li:share:123" {
					t.Fatalf("reshareContext = %v", b["reshareContext"])
				}
				if _, ok := b["content"]; ok {
					t.Fatal("a reshare carried content")
				}
			},
		},
		"reshare disabled": {
			Post{DisableReshare: true},
			func(t *testing.T, b map[string]any) {
				if b["isReshareDisabledByAuthor"] != true {
					t.Fatalf("isReshareDisabledByAuthor = %v", b["isReshareDisabledByAuthor"])
				}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := fakeLinkedIn(t, created("urn:li:share:7001"))
			post := tc.post
			post.Author = PersonURN("abc123")
			post.Text = "caption"
			post.Visibility = VisibilityConnections
			id, err := client.Publish(context.Background(), "member-token", post)
			if err != nil || id != "urn:li:share:7001" {
				t.Fatalf("id=%q err=%v", id, err)
			}
			rec := (*seen)[0]
			if rec.method != http.MethodPost || rec.path != "/rest/posts" {
				t.Fatalf("request = %s %s", rec.method, rec.path)
			}
			requireVersionedHeaders(t, rec)
			body := decode(t, rec)
			if body["author"] != "urn:li:person:abc123" || body["commentary"] != "caption" || body["visibility"] != "CONNECTIONS" || body["lifecycleState"] != "PUBLISHED" {
				t.Fatalf("body = %v", body)
			}
			tc.check(t, body)
		})
	}
}

func TestPublishEscapesLittleTextReservedCharacters(t *testing.T) {
	client, seen := fakeLinkedIn(t, created("urn:li:share:1"))
	if _, err := client.Publish(context.Background(), "member-token", Post{Author: PersonURN("a"), Text: "Ship it (now) #golang [v2] a_b *c*", Visibility: VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	want := `Ship it \(now\) #golang \[v2\] a\_b \*c\*`
	if got := decode(t, (*seen)[0])["commentary"]; got != want {
		t.Fatalf("commentary = %q, want %q", got, want)
	}
}

func TestEscapeCommentary(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "plain",
		`a|b{c}d@e`:   `a\|b\{c\}d\@e`,
		`<x> ~ \ end`: `\<x\> \~ \\ end`,
		"line\nbreak": "line\nbreak",
		"#hash":       "#hash",
	} {
		if got := EscapeCommentary(in); got != want {
			t.Errorf("EscapeCommentary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublishRejectsABadAuthorBeforeCalling(t *testing.T) {
	client, seen := fakeLinkedIn(t, created("x"))
	for _, author := range []string{"", "abc", "urn:li:person:", "urn:li:company:1", "urn:li:person:a/b"} {
		if _, err := client.Publish(context.Background(), "t", Post{Author: author, Text: "x", Visibility: VisibilityPublic}); err == nil {
			t.Errorf("author %q was accepted", author)
		}
	}
	if len(*seen) != 0 {
		t.Fatal("LinkedIn was called")
	}
}

func TestPublishRejectsABadReshareParent(t *testing.T) {
	client, seen := fakeLinkedIn(t, created("x"))
	if _, err := client.Publish(context.Background(), "t", Post{Author: PersonURN("a"), Visibility: VisibilityPublic, ReshareParent: "urn:li:share:../x"}); err == nil {
		t.Fatal("a bad parent URN was accepted")
	}
	if len(*seen) != 0 {
		t.Fatal("LinkedIn was called")
	}
}

func TestOrganizationAuthorIsAccepted(t *testing.T) {
	client, seen := fakeLinkedIn(t, created("urn:li:share:9"))
	if _, err := client.Publish(context.Background(), "member-token", Post{Author: "urn:li:organization:2414183", Text: "hi", Visibility: VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	if decode(t, (*seen)[0])["author"] != "urn:li:organization:2414183" {
		t.Fatalf("body = %s", (*seen)[0].body)
	}
}

func TestProfileDecodesTheOptionalFields(t *testing.T) {
	for name, locale := range map[string]string{"string": `"en-US"`, "object": `{"country":"US","language":"en"}`} {
		t.Run(name, func(t *testing.T) {
			client, _ := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
				_, _ = w.Write([]byte(`{"sub":"abc","name":"Ada Lovelace","given_name":"Ada","family_name":"Lovelace","picture":"https://img/x.png","locale":` + locale + `,"email":"ada@example.com","email_verified":true}`))
			})
			profile, err := client.Profile(context.Background(), "member-token")
			if err != nil {
				t.Fatal(err)
			}
			if profile.GivenName != "Ada" || profile.FamilyName != "Lovelace" || profile.Email != "ada@example.com" || !profile.EmailVerified || profile.Picture != "https://img/x.png" || profile.Locale != "en-US" {
				t.Fatalf("profile = %+v", profile)
			}
		})
	}
}

func TestProfileWithoutEmailLeavesItEmpty(t *testing.T) {
	client, _ := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
		_, _ = w.Write([]byte(`{"sub":"abc","name":"Ada"}`))
	})
	profile, err := client.Profile(context.Background(), "member-token")
	if err != nil || profile.Email != "" || profile.Locale != "" {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
}

func TestUploadImageInitializesThenUploadsTheBytes(t *testing.T) {
	var uploadURL string
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		switch rec.path {
		case "/rest/images":
			_, _ = w.Write([]byte(`{"value":{"uploadUrl":"` + uploadURL + `","image":"urn:li:image:I1","uploadUrlExpiresAt":1}}`))
		case "/upload/img":
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s", rec.path)
		}
	})
	uploadURL = client.baseURL + "/upload/img"
	urn, err := client.UploadImage(context.Background(), "member-token", "abc123", []byte("PNGDATA"))
	if err != nil || urn != "urn:li:image:I1" {
		t.Fatalf("urn=%q err=%v", urn, err)
	}
	init, upload := (*seen)[0], (*seen)[1]
	if init.method != http.MethodPost || init.query != "action=initializeUpload" {
		t.Fatalf("init = %s ?%s", init.method, init.query)
	}
	requireVersionedHeaders(t, init)
	if decode(t, init)["initializeUploadRequest"].(map[string]any)["owner"] != "urn:li:person:abc123" {
		t.Fatalf("init body = %s", init.body)
	}
	if upload.method != http.MethodPut || string(upload.body) != "PNGDATA" || upload.header.Get("Authorization") != "Bearer member-token" {
		t.Fatalf("upload = %s body=%q auth=%q", upload.method, upload.body, upload.header.Get("Authorization"))
	}
}

func TestUploadDocumentUsesTheDocumentsAPI(t *testing.T) {
	var uploadURL string
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, rec recorded) {
		if rec.path == "/rest/documents" {
			_, _ = w.Write([]byte(`{"value":{"uploadUrl":"` + uploadURL + `","document":"urn:li:document:D1"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	uploadURL = client.baseURL + "/upload/doc"
	urn, err := client.UploadDocument(context.Background(), "member-token", "abc123", []byte("%PDF-1.7"))
	if err != nil || urn != "urn:li:document:D1" {
		t.Fatalf("urn=%q err=%v", urn, err)
	}
	if (*seen)[0].query != "action=initializeUpload" || (*seen)[1].method != http.MethodPut {
		t.Fatalf("requests = %+v", *seen)
	}
}

func TestUploadNeverSendsTheTokenToANonLinkedInHost(t *testing.T) {
	var uploadURL string
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, rec recorded) {
		_, _ = w.Write([]byte(`{"value":{"uploadUrl":"` + uploadURL + `","image":"urn:li:image:I1"}}`))
	})
	client.allowUploadURL = defaultAllowUploadURL
	for _, bad := range []string{"http://www.linkedin.com/dms-uploads/x", "https://evil.example/dms-uploads/x", "https://linkedin.com.evil.example/x", "https://127.0.0.1/x"} {
		uploadURL = bad
		*seen = nil
		if _, err := client.UploadImage(context.Background(), "member-token", "abc123", []byte("x")); err == nil {
			t.Errorf("upload URL %q was accepted", bad)
		}
		if len(*seen) != 1 {
			t.Errorf("%q: %d requests, want only the initialize call", bad, len(*seen))
		}
	}
}

func TestDefaultUploadHostCheck(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://www.linkedin.com/dms-uploads/a": true,
		"https://api.linkedin.com/mediaUpload/a": true,
		"https://linkedin.com/a":                 true,
		"http://www.linkedin.com/a":              false,
		"https://evil.com/a":                     false,
		"https://notlinkedin.com/a":              false,
		"https://www.linkedin.com.evil.com/a":    false,
	} {
		u, _ := url.Parse(raw)
		if got := defaultAllowUploadURL(u); got != want {
			t.Errorf("%s = %v, want %v", raw, got, want)
		}
	}
}

func TestUploadVideoSendsEachPartAndFinalizesWithTheETags(t *testing.T) {
	var base string
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, rec recorded) {
		switch {
		case rec.path == "/rest/videos" && rec.query == "action=initializeUpload":
			_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:V1","uploadToken":"tok","uploadInstructions":[` +
				`{"uploadUrl":"` + base + `/p0","firstByte":0,"lastByte":4},` +
				`{"uploadUrl":"` + base + `/p1","firstByte":5,"lastByte":9}]}}`))
		case rec.path == "/p0":
			w.Header().Set("ETag", `"etag-0"`)
		case rec.path == "/p1":
			w.Header().Set("ETag", `"etag-1"`)
		case rec.path == "/rest/videos" && rec.query == "action=finalizeUpload":
		default:
			t.Errorf("unexpected %s %s", rec.method, rec.path)
		}
	})
	base = client.baseURL
	urn, err := client.UploadVideo(context.Background(), "member-token", "abc123", []byte("0123456789"))
	if err != nil || urn != "urn:li:video:V1" {
		t.Fatalf("urn=%q err=%v", urn, err)
	}
	init := decode(t, (*seen)[0])["initializeUploadRequest"].(map[string]any)
	if init["owner"] != "urn:li:person:abc123" || init["fileSizeBytes"] != float64(10) || init["uploadCaptions"] != false || init["uploadThumbnail"] != false {
		t.Fatalf("init = %v", init)
	}
	p0, p1 := (*seen)[1], (*seen)[2]
	if p0.method != http.MethodPut || string(p0.body) != "01234" || string(p1.body) != "56789" {
		t.Fatalf("parts = %q %q", p0.body, p1.body)
	}
	if p0.header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("part content type = %q", p0.header.Get("Content-Type"))
	}
	if p0.header.Get("Authorization") != "" || p1.header.Get("Authorization") != "" {
		t.Error("the member token was sent to a part upload URL")
	}
	finalize := decode(t, (*seen)[3])["finalizeUploadRequest"].(map[string]any)
	ids := finalize["uploadedPartIds"].([]any)
	if finalize["video"] != "urn:li:video:V1" || finalize["uploadToken"] != "tok" || len(ids) != 2 || ids[0] != "etag-0" || ids[1] != "etag-1" {
		t.Fatalf("finalize = %v", finalize)
	}
	requireVersionedHeaders(t, (*seen)[3])
}

func TestUploadVideoFailsWhenAPartHasNoETag(t *testing.T) {
	var base string
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, rec recorded) {
		if rec.path == "/rest/videos" {
			_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:V1","uploadInstructions":[{"uploadUrl":"` + base + `/p0","firstByte":0,"lastByte":3}]}}`))
		}
	})
	base = client.baseURL
	if _, err := client.UploadVideo(context.Background(), "member-token", "abc123", []byte("abcd")); err == nil {
		t.Fatal("a part without an ETag was accepted")
	}
	for _, rec := range *seen {
		if rec.query == "action=finalizeUpload" {
			t.Fatal("finalize was called after a failed part")
		}
	}
}

func TestUploadVideoRejectsInstructionsThatDoNotFitTheFile(t *testing.T) {
	var base string
	client, _ := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
		_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:V1","uploadInstructions":[{"uploadUrl":"` + base + `/p0","firstByte":0,"lastByte":99}]}}`))
	})
	base = client.baseURL
	if _, err := client.UploadVideo(context.Background(), "member-token", "abc123", []byte("abcd")); err == nil {
		t.Fatal("an out-of-range part was accepted")
	}
}

func TestEditPostCommentarySendsAPartialUpdate(t *testing.T) {
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) { w.WriteHeader(http.StatusNoContent) })
	if err := client.EditPostCommentary(context.Background(), "member-token", "urn:li:share:6844785523593134080", "New (text)"); err != nil {
		t.Fatal(err)
	}
	rec := (*seen)[0]
	if rec.method != http.MethodPost || rec.rawURI != "/rest/posts/urn%3Ali%3Ashare%3A6844785523593134080" {
		t.Fatalf("request = %s %s", rec.method, rec.rawURI)
	}
	requireVersionedHeaders(t, rec)
	if rec.header.Get("X-Restli-Method") != "PARTIAL_UPDATE" || rec.header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", rec.header)
	}
	set := decode(t, rec)["patch"].(map[string]any)["$set"].(map[string]any)
	if set["commentary"] != `New \(text\)` {
		t.Fatalf("patch = %s", rec.body)
	}
}

func TestDeletePostSendsDELETEForTheEncodedURN(t *testing.T) {
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) { w.WriteHeader(http.StatusNoContent) })
	if err := client.DeletePost(context.Background(), "member-token", "urn:li:ugcPost:123"); err != nil {
		t.Fatal(err)
	}
	rec := (*seen)[0]
	if rec.method != http.MethodDelete || rec.rawURI != "/rest/posts/urn%3Ali%3AugcPost%3A123" || rec.header.Get("X-Restli-Method") != "DELETE" {
		t.Fatalf("request = %s %s %v", rec.method, rec.rawURI, rec.header)
	}
	requireVersionedHeaders(t, rec)
}

func TestPostURNsAreValidatedBeforeAnyCall(t *testing.T) {
	client, seen := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) { w.WriteHeader(http.StatusNoContent) })
	for _, bad := range []string{"", "123", "urn:li:person:1", "urn:li:share:", "urn:li:share:abc", "urn:li:share:1/../x", "urn:li:share:1?x=1", "https://x"} {
		if err := client.DeletePost(context.Background(), "t", bad); err == nil {
			t.Errorf("delete accepted %q", bad)
		}
		if err := client.EditPostCommentary(context.Background(), "t", bad, "x"); err == nil {
			t.Errorf("edit accepted %q", bad)
		}
	}
	if len(*seen) != 0 {
		t.Fatal("LinkedIn was called")
	}
}

func TestWriteCallsMapStatusesToTypedErrors(t *testing.T) {
	for status, check := range map[int]func(error) bool{
		http.StatusUnauthorized:    func(err error) bool { return errors.Is(err, ErrUnauthorized) },
		http.StatusForbidden:       func(err error) bool { var e *APIError; return errors.As(err, &e) && e.Status == 403 },
		http.StatusTooManyRequests: func(err error) bool { var e *APIError; return errors.As(err, &e) && e.Status == 429 },
		http.StatusUpgradeRequired: func(err error) bool { return errors.Is(err, ErrVersionInactive) },
		http.StatusNotFound:        func(err error) bool { var e *APIError; return errors.As(err, &e) && e.Status == 404 },
	} {
		client, _ := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"detail"}`))
		})
		if err := client.DeletePost(context.Background(), "t", "urn:li:share:1"); !check(err) {
			t.Errorf("delete status %d: err = %v", status, err)
		}
		if err := client.EditPostCommentary(context.Background(), "t", "urn:li:share:1", "x"); !check(err) {
			t.Errorf("edit status %d: err = %v", status, err)
		}
		if _, err := client.UploadImage(context.Background(), "t", "abc", []byte("x")); !check(err) {
			t.Errorf("upload status %d: err = %v", status, err)
		}
	}
}

func TestUploadErrorsDoNotLeakTheBody(t *testing.T) {
	client, _ := fakeLinkedIn(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"ok","echo":"secret-bytes"}`))
	})
	_, err := client.UploadImage(context.Background(), "t", "abc", []byte("secret-bytes"))
	if err == nil || strings.Contains(err.Error(), "secret-bytes") {
		t.Fatalf("err = %v", err)
	}
}
