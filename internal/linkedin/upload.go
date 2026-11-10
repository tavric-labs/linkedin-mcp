package linkedin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// UploadImage registers an image upload for the member, sends the bytes and
// returns the image URN to attach to a post.
func (c *Client) UploadImage(ctx context.Context, token, memberID string, data []byte) (string, error) {
	return c.uploadSingle(ctx, token, memberID, data, "images", "image")
}

// UploadDocument registers a document upload for the member, sends the bytes
// and returns the document URN to attach to a post.
func (c *Client) UploadDocument(ctx context.Context, token, memberID string, data []byte) (string, error) {
	return c.uploadSingle(ctx, token, memberID, data, "documents", "document")
}

func (c *Client) uploadSingle(ctx context.Context, token, memberID string, data []byte, resource, field string) (string, error) {
	init := map[string]any{"initializeUploadRequest": map[string]any{"owner": PersonURN(memberID)}}
	var out struct {
		Value map[string]any `json:"value"`
	}
	if err := c.initialize(ctx, token, "/rest/"+resource+"?action=initializeUpload", init, &out); err != nil {
		return "", err
	}
	uploadURL, _ := out.Value["uploadUrl"].(string)
	urn, _ := out.Value[field].(string)
	if uploadURL == "" || urn == "" {
		return "", fmt.Errorf("linkedin %s upload response is incomplete", field)
	}
	// Images and documents are uploaded with the member token, so the URL must
	// be a LinkedIn host.
	if err := c.put(ctx, uploadURL, data, token, "application/octet-stream", nil); err != nil {
		return "", fmt.Errorf("linkedin %s upload: %w", field, err)
	}
	return urn, nil
}

// UploadVideo registers a multi-part video upload, sends each part, finalizes
// it and returns the video URN to attach to a post. LinkedIn may still be
// processing the video when this returns.
func (c *Client) UploadVideo(ctx context.Context, token, memberID string, data []byte) (string, error) {
	init := map[string]any{"initializeUploadRequest": map[string]any{
		"owner": PersonURN(memberID), "fileSizeBytes": len(data), "uploadCaptions": false, "uploadThumbnail": false,
	}}
	var out struct {
		Value struct {
			Video        string `json:"video"`
			UploadToken  string `json:"uploadToken"`
			Instructions []struct {
				UploadURL string `json:"uploadUrl"`
				FirstByte int    `json:"firstByte"`
				LastByte  int    `json:"lastByte"`
			} `json:"uploadInstructions"`
		} `json:"value"`
	}
	if err := c.initialize(ctx, token, "/rest/videos?action=initializeUpload", init, &out); err != nil {
		return "", err
	}
	if out.Value.Video == "" || len(out.Value.Instructions) == 0 {
		return "", errors.New("linkedin video upload response is incomplete")
	}
	next := 0
	for _, part := range out.Value.Instructions {
		if part.FirstByte != next || part.LastByte < part.FirstByte || part.LastByte >= len(data) {
			return "", errors.New("linkedin video upload instructions do not fit the file")
		}
		next = part.LastByte + 1
	}
	if next != len(data) {
		return "", errors.New("linkedin video upload instructions do not cover the file")
	}
	ids := make([]string, 0, len(out.Value.Instructions))
	for _, part := range out.Value.Instructions {
		// Part URLs are signed; the member token is not sent to them.
		var etag string
		err := c.put(ctx, part.UploadURL, data[part.FirstByte:part.LastByte+1], "", "application/octet-stream", &etag)
		if err != nil {
			return "", fmt.Errorf("linkedin video part upload: %w", err)
		}
		if etag == "" {
			return "", errors.New("linkedin video part upload returned no ETag")
		}
		ids = append(ids, etag)
	}
	finalize := map[string]any{"finalizeUploadRequest": map[string]any{
		"video": out.Value.Video, "uploadToken": out.Value.UploadToken, "uploadedPartIds": ids,
	}}
	if err := c.initialize(ctx, token, "/rest/videos?action=finalizeUpload", finalize, nil); err != nil {
		return "", err
	}
	return out.Value.Video, nil
}

// initialize posts a Rest.li action and decodes its JSON answer into out, if any.
func (c *Client) initialize(ctx context.Context, token, path string, body any, out any) error {
	response, err := c.versioned(ctx, token, http.MethodPost, path, body, nil)
	if err != nil {
		return fmt.Errorf("linkedin upload: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return nil
	}
	if err := decodeJSON(response.Body, out); err != nil {
		return fmt.Errorf("decode linkedin upload response: %w", err)
	}
	return nil
}

// put sends data to an upload URL LinkedIn handed out. It refuses URLs that
// are not LinkedIn hosts, so neither bytes nor a token go anywhere else. When
// etag is not nil it receives the response's ETag with its quotes removed.
func (c *Client) put(ctx context.Context, rawURL string, data []byte, token, contentType string, etag *string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || !c.allowUploadURL(parsed) {
		return errors.New("linkedin returned an upload URL that is not a LinkedIn host")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.upload.Do(request)
	if err != nil {
		return errors.New("upload request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response)
	}
	if etag != nil {
		*etag = strings.Trim(response.Header.Get("ETag"), `"`)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return nil
}
