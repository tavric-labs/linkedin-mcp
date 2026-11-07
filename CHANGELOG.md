# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-11-07

### Added

- First public release of the LinkedIn MCP server, with a deployment guide and
  security notes in the README.

- GitHub Actions CI (gofmt, vet, build, race tests, staticcheck, govulncheck,
  Docker build and Trivy scans) and a tag-driven release workflow that publishes
  a multi-arch image to `ghcr.io/tavric-labs/linkedin-mcp` and creates a GitHub
  release.
- Dependabot updates for Go modules, Docker and GitHub Actions.

[Unreleased]: https://github.com/tavric-labs/linkedin-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tavric-labs/linkedin-mcp/releases/tag/v0.1.0
