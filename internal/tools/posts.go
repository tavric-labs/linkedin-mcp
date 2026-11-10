package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tavric-labs/linkedin-mcp/internal/linkedin"
	"github.com/tavric-labs/linkedin-mcp/internal/media"
)

// PostArgs are the inputs of linkedin_create_post.
type PostArgs struct {
	Text           string `json:"text" jsonschema:"the post text, up to 3000 characters. #hashtags work; other special characters show literally"`
	Visibility     string `json:"visibility,omitempty" jsonschema:"PUBLIC (default, anyone on LinkedIn) or CONNECTIONS (first-degree connections only)"`
	DisableReshare bool   `json:"disable_reshare,omitempty" jsonschema:"set true to stop other members resharing this post"`
	Confirm        bool   `json:"confirm,omitempty" jsonschema:"set true to publish; without it the call is a dry run that publishes nothing"`
}

// MediaInput is a file given as a public https URL or as base64, never both.
type MediaInput struct {
	URL    string `json:"url,omitempty" jsonschema:"public https URL on port 443 of the file; the server downloads it only when confirm is true. Private, loopback and link-local addresses are refused"`
	Base64 string `json:"base64,omitempty" jsonschema:"the file's bytes, base64 encoded, up to 10 MB decoded; use instead of url"`
}

// ImageInput is an image given as a URL or base64, with optional alt text.
type ImageInput struct {
	URL     string `json:"url,omitempty" jsonschema:"public https URL on port 443 of a JPEG, PNG or GIF image, up to 10 MB; downloaded only when confirm is true"`
	Base64  string `json:"base64,omitempty" jsonschema:"base64 of a JPEG, PNG or GIF image, up to 10 MB decoded; use instead of url"`
	AltText string `json:"alt_text,omitempty" jsonschema:"alternative text for screen readers, under 120 characters recommended, 4086 at most"`
}

// ArticleArgs are the inputs of linkedin_create_article_post.
type ArticleArgs struct {
	URL         string      `json:"url" jsonschema:"the https link to share"`
	Title       string      `json:"title" jsonschema:"the link card title, up to 400 characters; LinkedIn does not read it from the page"`
	Description string      `json:"description,omitempty" jsonschema:"the link card description"`
	Text        string      `json:"text,omitempty" jsonschema:"optional post text above the link card, up to 3000 characters"`
	Thumbnail   *MediaInput `json:"thumbnail,omitempty" jsonschema:"optional card image (JPEG, PNG or GIF): give url or base64"`
	Visibility  string      `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm     bool        `json:"confirm,omitempty" jsonschema:"set true to publish; without it the call is a dry run"`
}

// ImagePostArgs are the inputs of linkedin_create_image_post.
type ImagePostArgs struct {
	Text       string       `json:"text,omitempty" jsonschema:"optional post text, up to 3000 characters"`
	Images     []ImageInput `json:"images" jsonschema:"1 to 20 images; one makes an image post, two or more a multi-image post. All images together up to 40 MB"`
	Visibility string       `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm    bool         `json:"confirm,omitempty" jsonschema:"set true to upload and publish; without it the call is a dry run"`
}

// VideoPostArgs are the inputs of linkedin_create_video_post.
type VideoPostArgs struct {
	Text        string `json:"text,omitempty" jsonschema:"optional post text, up to 3000 characters"`
	Title       string `json:"title,omitempty" jsonschema:"optional video title, up to 400 characters"`
	VideoURL    string `json:"video_url,omitempty" jsonschema:"public https URL on port 443 of an MP4 video, 75 KB to 100 MB; downloaded only when confirm is true"`
	VideoBase64 string `json:"video_base64,omitempty" jsonschema:"base64 of an MP4 video, up to 10 MB decoded; use instead of video_url"`
	Visibility  string `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"set true to upload and publish; without it the call is a dry run"`
}

// DocumentPostArgs are the inputs of linkedin_create_document_post.
type DocumentPostArgs struct {
	Text           string `json:"text,omitempty" jsonschema:"optional post text, up to 3000 characters"`
	Title          string `json:"title" jsonschema:"the document title shown on the post, up to 400 characters"`
	DocumentURL    string `json:"document_url,omitempty" jsonschema:"public https URL on port 443 of a PDF, PPT, PPTX, DOC or DOCX file, up to 50 MB; downloaded only when confirm is true"`
	DocumentBase64 string `json:"document_base64,omitempty" jsonschema:"base64 of a PDF, PPT, PPTX, DOC or DOCX file, up to 10 MB decoded; use instead of document_url"`
	Visibility     string `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm        bool   `json:"confirm,omitempty" jsonschema:"set true to upload and publish; without it the call is a dry run"`
}

// PollPostArgs are the inputs of linkedin_create_poll_post.
type PollPostArgs struct {
	Question   string   `json:"question" jsonschema:"the poll question, up to 140 characters"`
	Options    []string `json:"options" jsonschema:"2 to 4 answer options, each up to 30 characters"`
	Duration   string   `json:"duration,omitempty" jsonschema:"how long voting stays open: ONE_DAY, THREE_DAYS (default), SEVEN_DAYS or FOURTEEN_DAYS"`
	Text       string   `json:"text,omitempty" jsonschema:"optional post text, up to 3000 characters"`
	Visibility string   `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm    bool     `json:"confirm,omitempty" jsonschema:"set true to publish; without it the call is a dry run"`
}

// ReshareArgs are the inputs of linkedin_reshare_post.
type ReshareArgs struct {
	PostURN    string `json:"post_urn" jsonschema:"the URN of the post to reshare, urn:li:share:<id> or urn:li:ugcPost:<id>"`
	Text       string `json:"text,omitempty" jsonschema:"optional commentary to add, up to 3000 characters"`
	Visibility string `json:"visibility,omitempty" jsonschema:"PUBLIC (default) or CONNECTIONS"`
	Confirm    bool   `json:"confirm,omitempty" jsonschema:"set true to reshare; without it the call is a dry run"`
}

// EditPostArgs are the inputs of linkedin_edit_post.
type EditPostArgs struct {
	PostURN string `json:"post_urn" jsonschema:"the URN of the signed-in person's own post, urn:li:share:<id> or urn:li:ugcPost:<id>"`
	Text    string `json:"text" jsonschema:"the new post text, up to 3000 characters; it replaces the old text. Only the text can be edited"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"set true to edit; without it the call is a dry run"`
}

// DeletePostArgs are the inputs of linkedin_delete_post.
type DeletePostArgs struct {
	PostURN string `json:"post_urn" jsonschema:"the URN of the signed-in person's own post, urn:li:share:<id> or urn:li:ugcPost:<id>"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"set true to permanently delete the post; without it the call is a dry run"`
}

// OrganizationPostArgs are the inputs of linkedin_create_organization_post.
type OrganizationPostArgs struct {
	OrganizationID string `json:"organization_id" jsonschema:"the numeric id of the LinkedIn organization page, from its admin URL"`
	Text           string `json:"text" jsonschema:"the post text, up to 3000 characters"`
	Confirm        bool   `json:"confirm,omitempty" jsonschema:"set true to publish; without it the call is a dry run"`
}

// CreatePost publishes a text post as the signed-in member, or previews it.
func (s *Server) CreatePost(ctx context.Context, request *mcp.CallToolRequest, args PostArgs) (*mcp.CallToolResult, any, error) {
	text, problem := checkText("post text", args.Text, true, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would publish a text post to LinkedIn (%s):\n\n%s", visibility, text)); r != nil {
		return r, nil, nil
	}
	return s.memberPost(ctx, request, "linkedin_create_post", func(context.Context, string, linkedin.Profile) (linkedin.Post, error) {
		return linkedin.Post{Text: text, Visibility: visibility, DisableReshare: args.DisableReshare}, nil
	}, "characters", utf8.RuneCountInString(text))
}

// CreateArticlePost shares a link with a title, description and thumbnail.
func (s *Server) CreateArticlePost(ctx context.Context, request *mcp.CallToolRequest, args ArticleArgs) (*mcp.CallToolResult, any, error) {
	link := strings.TrimSpace(args.URL)
	if parsed, err := url.Parse(link); err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fail("The url must be an absolute https link.")
	}
	title, problem := checkText("title", args.Title, true, maxTitleRunes)
	if problem != "" {
		return fail(problem)
	}
	description, problem := checkText("description", args.Description, false, maxDescriptionRune)
	if problem != "" {
		return fail(problem)
	}
	text, problem := checkText("post text", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	var thumbnail *source
	if args.Thumbnail != nil && (args.Thumbnail.URL != "" || args.Thumbnail.Base64 != "") {
		src, problem := newSource(media.Image, "thumbnail", args.Thumbnail.URL, args.Thumbnail.Base64)
		if problem != "" {
			return fail(problem)
		}
		thumbnail = &src
	}
	preview := fmt.Sprintf("This would publish a link post (%s) to %s titled %q.\n\n%s", visibility, link, title, excerpt(text))
	if thumbnail != nil {
		preview += "\n\nThe card image would be uploaded to LinkedIn."
	}
	if r := gate(request, args.Confirm, preview); r != nil {
		return r, nil, nil
	}
	var thumbnailData []byte
	if thumbnail != nil {
		data, problem := s.load(ctx, media.Image, "thumbnail", *thumbnail)
		if problem != "" {
			return fail(problem)
		}
		thumbnailData = data
	}
	return s.memberPost(ctx, request, "linkedin_create_article_post", func(ctx context.Context, token string, profile linkedin.Profile) (linkedin.Post, error) {
		thumbnailURN := ""
		if thumbnailData != nil {
			urn, err := s.LinkedIn.UploadImage(ctx, token, profile.Sub, thumbnailData)
			if err != nil {
				return linkedin.Post{}, err
			}
			thumbnailURN = urn
		}
		return linkedin.Post{Text: text, Visibility: visibility, Content: linkedin.ArticleContent(link, title, description, thumbnailURN)}, nil
	})
}

// CreateImagePost publishes one image, or a multi-image post.
func (s *Server) CreateImagePost(ctx context.Context, request *mcp.CallToolRequest, args ImagePostArgs) (*mcp.CallToolResult, any, error) {
	if len(args.Images) == 0 || len(args.Images) > MaxImages {
		return fail(fmt.Sprintf("Give between 1 and %d images.", MaxImages))
	}
	text, problem := checkText("post text", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	sources := make([]source, len(args.Images))
	alts := make([]string, len(args.Images))
	inline := 0
	for i, image := range args.Images {
		label := fmt.Sprintf("image %d", i+1)
		src, problem := newSource(media.Image, label, image.URL, image.Base64)
		if problem != "" {
			return fail(problem)
		}
		alt, problem := checkText("alt text of "+label, image.AltText, false, maxAltTextRunes)
		if problem != "" {
			return fail(problem)
		}
		sources[i], alts[i] = src, alt
		inline += len(src.data)
	}
	if inline > MaxTotalImageBytes {
		return fail("The images are too large together (40 MB at most).")
	}
	kind := "an image post"
	if len(sources) > 1 {
		kind = fmt.Sprintf("a multi-image post with %d images", len(sources))
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would upload the image(s) and publish %s (%s):\n\n%s", kind, visibility, excerpt(text))); r != nil {
		return r, nil, nil
	}
	loaded := make([][]byte, len(sources))
	total := 0
	for i, src := range sources {
		data, problem := s.load(ctx, media.Image, fmt.Sprintf("image %d", i+1), src)
		if problem != "" {
			return fail(problem)
		}
		if total += len(data); total > MaxTotalImageBytes {
			return fail("The images are too large together (40 MB at most).")
		}
		loaded[i] = data
	}
	return s.memberPost(ctx, request, "linkedin_create_image_post", func(ctx context.Context, token string, profile linkedin.Profile) (linkedin.Post, error) {
		images := make([]linkedin.Image, len(loaded))
		for i, data := range loaded {
			urn, err := s.LinkedIn.UploadImage(ctx, token, profile.Sub, data)
			if err != nil {
				return linkedin.Post{}, err
			}
			images[i] = linkedin.Image{URN: urn, AltText: alts[i]}
		}
		return linkedin.Post{Text: text, Visibility: visibility, Content: linkedin.ImageContent(images)}, nil
	}, "images", len(loaded))
}

// CreateVideoPost uploads an MP4 and publishes it.
func (s *Server) CreateVideoPost(ctx context.Context, request *mcp.CallToolRequest, args VideoPostArgs) (*mcp.CallToolResult, any, error) {
	text, problem := checkText("post text", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	title, problem := checkText("title", args.Title, false, maxTitleRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	src, problem := newSource(media.Video, "video", args.VideoURL, args.VideoBase64)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would upload the video and publish a video post (%s):\n\n%s\n\nLinkedIn may take a few minutes to process the video before it appears.", visibility, excerpt(text))); r != nil {
		return r, nil, nil
	}
	data, problem := s.load(ctx, media.Video, "video", src)
	if problem != "" {
		return fail(problem)
	}
	return s.memberPost(ctx, request, "linkedin_create_video_post", func(ctx context.Context, token string, profile linkedin.Profile) (linkedin.Post, error) {
		urn, err := s.LinkedIn.UploadVideo(ctx, token, profile.Sub, data)
		if err != nil {
			return linkedin.Post{}, err
		}
		return linkedin.Post{Text: text, Visibility: visibility, Content: linkedin.MediaContent(urn, title)}, nil
	}, "bytes", len(data))
}

// CreateDocumentPost uploads a document and publishes it.
func (s *Server) CreateDocumentPost(ctx context.Context, request *mcp.CallToolRequest, args DocumentPostArgs) (*mcp.CallToolResult, any, error) {
	text, problem := checkText("post text", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	title, problem := checkText("title", args.Title, true, maxTitleRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	src, problem := newSource(media.Document, "document", args.DocumentURL, args.DocumentBase64)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would upload the document %q and publish a document post (%s):\n\n%s", title, visibility, excerpt(text))); r != nil {
		return r, nil, nil
	}
	data, problem := s.load(ctx, media.Document, "document", src)
	if problem != "" {
		return fail(problem)
	}
	return s.memberPost(ctx, request, "linkedin_create_document_post", func(ctx context.Context, token string, profile linkedin.Profile) (linkedin.Post, error) {
		urn, err := s.LinkedIn.UploadDocument(ctx, token, profile.Sub, data)
		if err != nil {
			return linkedin.Post{}, err
		}
		return linkedin.Post{Text: text, Visibility: visibility, Content: linkedin.MediaContent(urn, title)}, nil
	}, "bytes", len(data))
}

// CreatePollPost publishes a poll.
func (s *Server) CreatePollPost(ctx context.Context, request *mcp.CallToolRequest, args PollPostArgs) (*mcp.CallToolResult, any, error) {
	question, problem := checkText("poll question", args.Question, true, maxPollQuestion)
	if problem != "" {
		return fail(problem)
	}
	if len(args.Options) < 2 || len(args.Options) > 4 {
		return fail("A poll needs 2 to 4 options.")
	}
	options := make([]string, len(args.Options))
	seen := map[string]bool{}
	for i, raw := range args.Options {
		option, problem := checkText(fmt.Sprintf("poll option %d", i+1), raw, true, maxPollOption)
		if problem != "" {
			return fail(problem)
		}
		if seen[strings.ToLower(option)] {
			return fail("Poll options must be different from each other.")
		}
		seen[strings.ToLower(option)] = true
		options[i] = option
	}
	duration := linkedin.PollDuration(strings.ToUpper(strings.TrimSpace(args.Duration)))
	switch duration {
	case "":
		duration = linkedin.PollThreeDays
	case linkedin.PollOneDay, linkedin.PollThreeDays, linkedin.PollSevenDays, linkedin.PollFourteenDays:
	default:
		return fail("Duration must be ONE_DAY, THREE_DAYS, SEVEN_DAYS or FOURTEEN_DAYS.")
	}
	text, problem := checkText("post text", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	preview := fmt.Sprintf("This would publish a poll (%s, open %s):\n\n%s\n- %s", visibility, duration, question, strings.Join(options, "\n- "))
	if r := gate(request, args.Confirm, preview); r != nil {
		return r, nil, nil
	}
	return s.memberPost(ctx, request, "linkedin_create_poll_post", func(context.Context, string, linkedin.Profile) (linkedin.Post, error) {
		return linkedin.Post{Text: text, Visibility: visibility, Content: linkedin.PollContent(question, options, duration)}, nil
	}, "options", len(options))
}

// ResharePost reshares an existing post, optionally with commentary.
func (s *Server) ResharePost(ctx context.Context, request *mcp.CallToolRequest, args ReshareArgs) (*mcp.CallToolResult, any, error) {
	urn := strings.TrimSpace(args.PostURN)
	if !linkedin.ValidPostURN(urn) {
		return fail("The post_urn must look like urn:li:share:<digits> or urn:li:ugcPost:<digits>.")
	}
	text, problem := checkText("commentary", args.Text, false, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	visibility, problem := parseVisibility(args.Visibility)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would reshare %s (%s) with the commentary:\n\n%s", urn, visibility, excerpt(text))); r != nil {
		return r, nil, nil
	}
	return s.memberPost(ctx, request, "linkedin_reshare_post", func(context.Context, string, linkedin.Profile) (linkedin.Post, error) {
		return linkedin.Post{Text: text, Visibility: visibility, ReshareParent: urn}, nil
	})
}

// EditPost replaces the text of one of the member's posts.
func (s *Server) EditPost(ctx context.Context, request *mcp.CallToolRequest, args EditPostArgs) (*mcp.CallToolResult, any, error) {
	urn := strings.TrimSpace(args.PostURN)
	if !linkedin.ValidPostURN(urn) {
		return fail("The post_urn must look like urn:li:share:<digits> or urn:li:ugcPost:<digits>.")
	}
	text, problem := checkText("new post text", args.Text, true, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would replace the text of %s with:\n\n%s", urn, text)); r != nil {
		return r, nil, nil
	}
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	if err := s.LinkedIn.EditPostCommentary(ctx, token, urn, text); err != nil {
		return s.linkedInFailure("linkedin_edit_post", subject, err), nil, nil
	}
	slog.Info("tool call", "tool", "linkedin_edit_post", "subject", subject, "outcome", "success", "post", urn)
	return result("Edited the text of "+urn+".", false), nil, nil
}

// DeletePost deletes one of the member's posts.
func (s *Server) DeletePost(ctx context.Context, request *mcp.CallToolRequest, args DeletePostArgs) (*mcp.CallToolResult, any, error) {
	urn := strings.TrimSpace(args.PostURN)
	if !linkedin.ValidPostURN(urn) {
		return fail("The post_urn must look like urn:li:share:<digits> or urn:li:ugcPost:<digits>.")
	}
	if r := gate(request, args.Confirm, "This would permanently delete "+urn+" from LinkedIn."); r != nil {
		return r, nil, nil
	}
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	if err := s.LinkedIn.DeletePost(ctx, token, urn); err != nil {
		return s.linkedInFailure("linkedin_delete_post", subject, err), nil, nil
	}
	slog.Info("tool call", "tool", "linkedin_delete_post", "subject", subject, "outcome", "success", "post", urn)
	return result("Deleted "+urn+".", false), nil, nil
}

var organizationID = regexp.MustCompile(`^[0-9]{1,20}$`)

// CreateOrganizationPost publishes a text post as an organization page the
// signed-in person administers. It is registered only when organization tools
// are enabled, and needs the w_organization_social permission.
func (s *Server) CreateOrganizationPost(ctx context.Context, request *mcp.CallToolRequest, args OrganizationPostArgs) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.OrganizationID)
	if !organizationID.MatchString(id) {
		return fail("The organization_id must be the numeric id of the page.")
	}
	text, problem := checkText("post text", args.Text, true, MaxPostRunes)
	if problem != "" {
		return fail(problem)
	}
	if r := gate(request, args.Confirm, fmt.Sprintf("This would publish a public post as organization %s:\n\n%s", id, text)); r != nil {
		return r, nil, nil
	}
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	post := linkedin.Post{Author: linkedin.OrganizationURN(id), Text: text, Visibility: linkedin.VisibilityPublic}
	postID, err := s.LinkedIn.Publish(ctx, token, post)
	if err != nil {
		return s.linkedInFailure("linkedin_create_organization_post", subject, err), nil, nil
	}
	slog.Info("tool call", "tool", "linkedin_create_organization_post", "subject", subject, "outcome", "success", "post", postID, "organization", id)
	return result("Posted to LinkedIn as organization "+id+": "+postLink(postID), false), nil, nil
}

// memberPost runs the shared tail of every member post: exchange the token,
// look up the member, let build upload media and shape the post, publish it.
// Extra key/value pairs are added to the success log line, never text.
func (s *Server) memberPost(ctx context.Context, request *mcp.CallToolRequest, tool string,
	build func(ctx context.Context, token string, profile linkedin.Profile) (linkedin.Post, error), logAttrs ...any) (*mcp.CallToolResult, any, error) {
	subject, token, failure := s.memberToken(ctx, request)
	if failure != nil {
		return failure, nil, nil
	}
	profile, err := s.LinkedIn.Profile(ctx, token)
	if err != nil {
		return s.linkedInFailure(tool, subject, err), nil, nil
	}
	post, err := build(ctx, token, profile)
	if err != nil {
		return s.linkedInFailure(tool, subject, err), nil, nil
	}
	post.Author = linkedin.PersonURN(profile.Sub)
	id, err := s.LinkedIn.Publish(ctx, token, post)
	if err != nil {
		return s.linkedInFailure(tool, subject, err), nil, nil
	}
	slog.Info("tool call", append([]any{"tool", tool, "subject", subject, "outcome", "success", "post", id}, logAttrs...)...)
	return result(fmt.Sprintf("Posted to LinkedIn as %s: %s", profile.Name, postLink(id)), false), nil, nil
}

// source is a file to upload: inline bytes (already validated) or a URL that is
// fetched only once the call is confirmed.
type source struct {
	data []byte
	url  string
}

// newSource checks that exactly one of rawURL and encoded is set and validates
// it as far as is possible without a network call.
func newSource(kind media.Kind, label, rawURL, encoded string) (source, string) {
	rawURL, encoded = strings.TrimSpace(rawURL), strings.TrimSpace(encoded)
	if (rawURL == "") == (encoded == "") {
		return source{}, fmt.Sprintf("Give exactly one of a URL or base64 data for the %s.", label)
	}
	if rawURL != "" {
		if err := media.ValidateURL(rawURL); err != nil {
			return source{}, fmt.Sprintf("The %s URL is not allowed: %s.", label, err)
		}
		return source{url: rawURL}, ""
	}
	data, err := media.DecodeBase64(encoded)
	if err == nil {
		err = media.Validate(kind, data)
	}
	if err != nil {
		return source{}, fmt.Sprintf("The %s is not usable: %s.", label, err)
	}
	return source{data: data}, ""
}

// load returns the bytes of a source, downloading a URL source now.
func (s *Server) load(ctx context.Context, kind media.Kind, label string, src source) ([]byte, string) {
	if src.data != nil {
		return src.data, ""
	}
	data, err := s.Fetcher.Fetch(ctx, src.url, media.FetchLimit(kind))
	if err == nil {
		err = media.Validate(kind, data)
	}
	if err != nil {
		slog.Warn("media download failed", "label", label, "error", err.Error())
		if errors.Is(err, media.ErrTooLarge) || errors.Is(err, media.ErrType) || errors.Is(err, media.ErrBlocked) {
			return nil, fmt.Sprintf("The %s could not be used: %s.", label, err)
		}
		return nil, fmt.Sprintf("The %s could not be downloaded.", label)
	}
	return data, ""
}
