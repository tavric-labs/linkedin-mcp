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

## Sign-in wiring (administrator)

Signing in with LinkedIn is configured in MCP Auth, not in this server. `.mcp/auth-resource.yaml`
holds everything LinkedIn-specific (endpoints, scopes, `pkce: false`, `id_token_nonce_policy:
disabled`, and `downstream_token: upstream_session`); fill its `<...>` values and apply it with
`mcp-runtime cluster mcp-auth resource apply --file .mcp/auth-resource.yaml`. It is applied by an
administrator and kept out of `servers.yaml`, which a tenant deploys.

In the LinkedIn developer app, add the products **Share on LinkedIn** and **Sign In with LinkedIn
using OpenID Connect**, and register `<MCP Auth issuer URL>/identity/callback` as a redirect URL.
LinkedIn's standard apps issue a token of about 60 days and no refresh token, so a person signs in
again when it expires.

## Develop

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

Deploy with the MCP Runtime workflow: `server validate`, `server build image`,
`server push`, `server deploy` using `.mcp/servers.yaml`.
