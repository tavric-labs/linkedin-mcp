# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Tools for the rest of what a member token can do with the Share on LinkedIn product
  (`w_member_social`): `linkedin_create_article_post` (link card with title,
  description and thumbnail), `linkedin_create_image_post` (one image or up to 20),
  `linkedin_create_video_post` (MP4), `linkedin_create_document_post` (PDF, PPT, PPTX,
  DOC, DOCX), `linkedin_create_poll_post`, `linkedin_reshare_post`, `linkedin_edit_post`
  and `linkedin_delete_post`. Every one is a dry run unless `confirm: true` and needs
  the `tools:write` scope to confirm.
- Images, documents and videos can be given as base64 or as a public https URL. URLs
  are fetched only on port 443, never from loopback, private, link-local or reserved
  addresses (checked when connecting, including after redirects), with no proxy and a
  size cap. Files are checked by their real type, not their name.
- `linkedin_create_post` takes `disable_reshare`.
- `linkedin_get_profile` also returns the given and family name, email, locale and
  picture that LinkedIn shares.
- Optional `linkedin_create_organization_post`, registered only when
  `LINKEDIN_MCP_ENABLE_ORG_TOOLS=true`. It needs a LinkedIn app with the Community
  Management API (`w_organization_social`); without that permission LinkedIn answers
  403 and the tool says so.
- The README lists what is not possible with self-serve LinkedIn access (reading posts,
  comments, reactions, analytics, followers, organization pages) and why, and notes the
  `--scope org` image path and namespace for MCP Runtime deployments.

### Changed

- `.mcp/servers.yaml` allow-lists the new tools. Existing grants that allow
  `linkedin_create_post` do not cover them; grant each new tool deliberately.
- The HTTP server's read and write timeouts are longer (60 s and 6 min) so uploads of
  large media finish.

### Fixed

- Post text is escaped for LinkedIn's `little` text format. Before, text containing
  parentheses, brackets, `@`, `*`, `_` or similar was cut off or rendered as a mention
  or formatting. `#hashtags` still work.
- A LinkedIn 403, 429 or 404 now tells the caller what happened (missing permission, rate
  limit, post not found) instead of a generic failure; LinkedIn's own detail stays in the
  server log.

## [0.1.2] - 2026-11-07

### Fixed

- Posting failed with `linkedin returned HTTP 426` because the default
  `LINKEDIN_API_VERSION` (`202510`) is past LinkedIn's sunset. The default is
  now `202610`. A 426 now tells the caller to set `LINKEDIN_API_VERSION`, and
  the server log records LinkedIn's status, `code`, `serviceErrorCode` and
  `message` (truncated; never request bodies, tokens or post text).

## [0.1.1] - 2026-11-07

### Added

- Apache-2.0 `LICENSE`.


## [0.1.0] - 2026-11-07

### Added

- First public release of the LinkedIn MCP server, with a deployment guide and
  security notes in the README.

- GitHub Actions CI (gofmt, vet, build, race tests, staticcheck, govulncheck,
  Docker build and Trivy scans) and a tag-driven release workflow that publishes
  a multi-arch image to `ghcr.io/tavric-labs/linkedin-mcp` and creates a GitHub
  release.
- Dependabot updates for Go modules, Docker and GitHub Actions.

[Unreleased]: https://github.com/tavric-labs/linkedin-mcp/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/tavric-labs/linkedin-mcp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/tavric-labs/linkedin-mcp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/tavric-labs/linkedin-mcp/releases/tag/v0.1.0
