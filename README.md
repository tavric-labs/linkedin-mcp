# linkedin-mcp

An MCP server that lets a signed-in person read their LinkedIn profile and publish
posts as themselves. It is built to run on [MCP Runtime](https://github.com/tavric-labs/mcp-runtime)
behind [mcp-auth](https://github.com/tavric-labs/mcp-auth), the way the Cully MCP server is.

## How it works

1. The MCP client signs the person in through mcp-auth (a LinkedIn login method) and
   sends the resulting access token to this server.
2. The server verifies the token (`mcpauth.RequireToken`, audience = this server's URL).
3. For each tool call it exchanges that token for **that person's own LinkedIn token**
   (RFC 8693, authenticating as a resource client with `private_key_jwt`) and calls
   LinkedIn as them. It stores no LinkedIn token and caches nothing, so ending the
   person's mcp-auth session ends their LinkedIn access at once.

Audit (who called which tool, when, allowed or denied) comes from the MCP Runtime
gateway in front of the server; the server also logs the subject, tool and outcome,
never a token or post text.

## Tools

| Tool | Scope | Notes |
|---|---|---|
| `linkedin_get_profile` | `tools:read` | Name and member id. |
| `linkedin_create_post` | `tools:write` to publish | A **dry run** unless `confirm: true`. Up to 3000 characters; `PUBLIC` or `CONNECTIONS`. |

LinkedIn's self-serve apps issue a token of about 60 days and no refresh token. When it
expires the tools answer "sign in again" and the person reconnects.

## Configuration

| Variable | Purpose |
|---|---|
| `LINKEDIN_MCP_RESOURCE` | This server's public MCP URL (the token audience). |
| `LINKEDIN_MCP_ISSUER` | The mcp-auth issuer URL. `LINKEDIN_JWKS_URL` overrides discovery. |
| `LINKEDIN_MCP_CLIENT_ID`, `LINKEDIN_MCP_CLIENT_KEY_ID` | The resource client registered in mcp-auth. |
| `LINKEDIN_MCP_CLIENT_KEY` or `LINKEDIN_MCP_CLIENT_KEY_FILE` | The client's RSA private key (PEM). |
| `LINKEDIN_API_BASE`, `LINKEDIN_API_VERSION` | Defaults `https://api.linkedin.com` and the `Linkedin-Version` header. |
| `LINKEDIN_MCP_ALLOW_INSECURE=true` | Plain http issuer, local development only. |

## Develop

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

Deploy with the MCP Runtime workflow: `server validate`, `server build image`,
`server push`, `server deploy` using `.mcp/servers.yaml`.

## CI and releases

`.github/workflows/ci.yaml` runs on pull requests and pushes to `main`: gofmt, `go vet`,
`go build`, `go test -race`, staticcheck, govulncheck, a Docker build and Trivy scans.

Pushing a `v*` tag from a commit on `main` runs `.github/workflows/release.yaml`. It re-runs
the tests, publishes a multi-arch (`linux/amd64`, `linux/arm64`) image with provenance and an
SBOM to `ghcr.io/tavric-labs/linkedin-mcp:<tag>` (and `:latest` for stable tags), and creates a
GitHub release with generated notes. Run the workflow manually with `dry_run` enabled to build
both platforms without pushing or releasing. Record user-visible changes in `CHANGELOG.md`.
