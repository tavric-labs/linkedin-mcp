# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- GitHub Actions CI (gofmt, vet, build, race tests, staticcheck, govulncheck,
  Docker build and Trivy scans) and a tag-driven release workflow that publishes
  a multi-arch image to `ghcr.io/tavric-labs/linkedin-mcp` and creates a GitHub
  release.
- Dependabot updates for Go modules, Docker and GitHub Actions.
