# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
