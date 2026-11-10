package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Register adds the tools to server. The organization tool is added only when
// enableOrg is true, because it needs a LinkedIn app approved for the
// Community Management API (w_organization_social).
func (s *Server) Register(server *mcp.Server, enableOrg bool) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_get_profile",
		Description: "Return the signed-in person's LinkedIn name, member id and, when LinkedIn shares them, given and family name, email, locale and picture. Read only.",
	}, s.GetProfile)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_post",
		Description: "Publish a text post on LinkedIn as the signed-in person. Without confirm: true it only previews the post.",
	}, s.CreatePost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_article_post",
		Description: "Share a link on LinkedIn with a title, optional description and optional card image (URL or base64). Without confirm: true it only previews and downloads nothing.",
	}, s.CreateArticlePost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_image_post",
		Description: "Publish a post with one image, or a multi-image post with up to 20 images, given as public https URLs or base64. Without confirm: true it only previews and downloads nothing.",
	}, s.CreateImagePost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_video_post",
		Description: "Publish a post with an MP4 video (75 KB to 100 MB) given as a public https URL or base64. Without confirm: true it only previews and downloads nothing.",
	}, s.CreateVideoPost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_document_post",
		Description: "Publish a post with a PDF, PPT, PPTX, DOC or DOCX document given as a public https URL or base64. Without confirm: true it only previews and downloads nothing.",
	}, s.CreateDocumentPost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_create_poll_post",
		Description: "Publish a LinkedIn poll with 2 to 4 options. Without confirm: true it only previews the poll.",
	}, s.CreatePollPost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_reshare_post",
		Description: "Reshare an existing LinkedIn post as the signed-in person, optionally with commentary. Without confirm: true it only previews.",
	}, s.ResharePost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_edit_post",
		Description: "Replace the text of one of the signed-in person's own LinkedIn posts. Without confirm: true it only previews.",
	}, s.EditPost)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "linkedin_delete_post",
		Description: "Permanently delete one of the signed-in person's own LinkedIn posts. Without confirm: true it only previews.",
	}, s.DeletePost)
	if enableOrg {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "linkedin_create_organization_post",
			Description: "Publish a text post as a LinkedIn organization page the signed-in person administers. Needs the w_organization_social permission. Without confirm: true it only previews.",
		}, s.CreateOrganizationPost)
	}
}
