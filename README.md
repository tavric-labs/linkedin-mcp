# linkedin-mcp

A remote MCP server (streamable HTTP) that lets an AI agent read the signed-in person's own
LinkedIn profile and create LinkedIn posts as them. Posts are a **dry run unless
`confirm: true`**.

Each person's LinkedIn token comes from an OAuth token exchange
([RFC 8693](https://www.rfc-editor.org/rfc/rfc8693)) against
[MCP Auth](https://github.com/tavric-labs/mcp-auth), using the access token the person's MCP
client already holds. There is no shared service credential, and this server stores and caches
no LinkedIn token.

It is built to run on [MCP Runtime](https://github.com/tavric-labs/mcp-runtime) behind MCP Auth.
It is a normal OAuth-protected MCP server, so it can also run on its own with Docker (see
[Run standalone](#run-standalone-with-docker)), but it always needs an MCP Auth issuer to
authenticate callers and exchange tokens.

## Why this one

There are several LinkedIn MCP servers. This one is aimed at teams that need to know who did
what, and to decide who may do it:

- **Auditability.** Every tool call arrives with a verified mcp-auth identity. On MCP Runtime
  the gateway in front of the server audits each call (who, which tool, when, allowed or
  denied). The server also logs the subject, tool and outcome, never a token or post text.
- **Policy control.** Access is decided by the platform, not by whoever holds a credential:
  grants and sessions say who may call which tool. Reading needs the `tools:read` scope and
  publishing needs `tools:write`. The write tool previews by default, and the server refuses
  to publish for a token without `tools:write`.
- **Per-user consent.** People sign in with LinkedIn's real OAuth flow. No pasted browser
  cookies, no shared personal access token. Ending a person's MCP Auth session ends their
  access to LinkedIn through this server at once.
- **Remote and deployable.** Run it once for a team instead of a local stdio process on every
  laptop holding personal credentials.

What it does not do: it is deliberately small (two tools, no search, no messaging, no
reading other people's profiles or feeds), and it depends on MCP Auth and, for the audit and
policy benefits above, on MCP Runtime's gateway. Run standalone, you get the identity and the
dry-run default but no platform audit or grants.

## Tools

| Tool | Scope | Notes |
|---|---|---|
| `linkedin_get_profile` | `tools:read` | The signed-in person's name and member id. |
| `linkedin_create_post` | `tools:write` to publish | A **dry run** unless `confirm: true`. Up to 3000 characters; visibility `PUBLIC` (default) or `CONNECTIONS`. |

## How it works

1. The MCP client signs the person in through MCP Auth, which offers LinkedIn as a login
   method, and sends the resulting access token to this server.
2. The server verifies the token (issuer, signature, audience = this server's URL, and the
   `tools:read` scope).
3. For each tool call it exchanges that token for **that person's own LinkedIn token**
   (RFC 8693, authenticating to MCP Auth as a resource client with `private_key_jwt`) and calls
   LinkedIn as them.

MCP Auth, not this server, keeps the LinkedIn token set for people who signed in to this
resource (login `downstream_token: upstream_session`), so protect MCP Auth's database as a
credential store.

## Limits

- LinkedIn's self-serve apps issue a token of about **60 days and no refresh token**. When it
  expires, the tools answer "sign in again" and the person reconnects.
- The LinkedIn app needs the products **Share on LinkedIn** and **Sign In with LinkedIn using
  OpenID Connect**.
- The redirect URL registered in the LinkedIn app must match exactly (see step 1 below).
- LinkedIn's standard apps do not support PKCE with a client secret and publish no ID token
  `nonce`, so the connector turns both off (`pkce: false`, `id_token_nonce_policy: disabled`).
- Only the signed-in person's own profile and posts. It makes two LinkedIn calls: `userinfo`
  and publish a post.

## How we run it

We deploy this server on our own MCP Runtime platform
([platform.mcpruntime.org](https://platform.mcpruntime.org)). We chose that over a local
process because it gives us a platform audit trail of every call, grants and sessions that
decide which agent may call which tool, and one verified identity per person instead of a
shared LinkedIn login. The same MCP Auth that signs people in to the platform signs them in to
LinkedIn, so the audit record names the same person as everywhere else.

## Deploy on MCP Runtime

Read the MCP Runtime docs first: the [repository](https://github.com/tavric-labs/mcp-runtime),
[Publish an MCP Server](https://github.com/tavric-labs/mcp-runtime/blob/main/docs/publish-mcp-server.md),
[MCP authorization](https://github.com/tavric-labs/mcp-runtime/blob/main/docs/mcp-oauth.md) and
the [CLI reference](https://github.com/tavric-labs/mcp-runtime/blob/main/docs/cli-reference.md).
You need a running MCP Runtime with the bundled MCP Auth enabled
([installation](https://github.com/tavric-labs/mcp-runtime/blob/main/docs/self-hosting.md)).

**1. Create the LinkedIn app** at <https://developer.linkedin.com>. Add the products
**Share on LinkedIn** and **Sign In with LinkedIn using OpenID Connect**. Under Auth, register
the redirect URL `<MCP Auth issuer URL>/identity/callback` exactly. Note the client ID and
secret.

**2. Generate the resource client key.** This key lets the server authenticate to MCP Auth
for the token exchange.

```bash
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out linkedin-mcp-client.pem
openssl pkey -in linkedin-mcp-client.pem -pubout   # goes into .mcp/auth-resource.yaml
```

Keep the private key out of git (`*.pem` is already ignored). Store it as a Kubernetes Secret
in the server's namespace, with the name and key that `.mcp/servers.yaml` references:

```bash
kubectl -n mcp-servers create secret generic linkedin-mcp-client-key \
  --from-file=LINKEDIN_MCP_CLIENT_KEY=linkedin-mcp-client.pem
```

**3. Edit the metadata.** `.mcp/servers.yaml` is the tenant-deployed server definition. Replace
the example `mcp.mcpruntime.org` / `auth.mcpruntime.org` / `registry.mcpruntime.org` values with
your own: image, `ingressHost`, `LINKEDIN_MCP_RESOURCE` and `auth.audience` (the same public MCP
URL), and `LINKEDIN_MCP_ISSUER` and `auth.issuerURL` (your MCP Auth issuer URL).

**4. Build, push and deploy** with the MCP Runtime CLI (log in first with `mcp-runtime auth login`):

```bash
mcp-runtime server validate --metadata-dir .mcp
mcp-runtime server build image linkedin --tag v0.1.0
mcp-runtime server push --image <registry>/<team>/linkedin-mcp/linkedin:v0.1.0 --scope tenant
mcp-runtime server deploy linkedin --scope tenant --metadata-dir .mcp
```

Use the exact image ref that `server build image` prints, and set `image` and `imageTag` in
`servers.yaml` to match. `server init` can scaffold metadata for a new server, but this repo
already ships it. You can also use the published image
`ghcr.io/tavric-labs/linkedin-mcp` once a release is tagged.

**5. Wire sign-in (cluster administrator).** `.mcp/auth-resource.yaml` holds everything
LinkedIn-specific for MCP Auth. Fill every `<...>` value (the public key from step 2, the
LinkedIn client ID, the callback URL), add the LinkedIn client secret to the
`mcp-auth-connector-secrets` Secret under the key `LINKEDIN_CLIENT_SECRET`, and deploy the
server first so MCP Auth already serves its resource. Then preview and apply:

```bash
mcp-runtime cluster mcp-auth resource apply --file .mcp/auth-resource.yaml \
  --kubeconfig <kubeconfig> --context <context> --dry-run
mcp-runtime cluster mcp-auth resource apply --file .mcp/auth-resource.yaml \
  --kubeconfig <kubeconfig> --context <context> --yes
```

The command checks the Secret key name only and never reads its value. The file holds no
secret; it is kept out of `servers.yaml` because it registers an identity provider, which an
administrator applies.

**6. Grant access and connect.** Create access grants and sessions for the people and agents
that may call the tools ([teams and access](https://github.com/tavric-labs/mcp-runtime/blob/main/docs/teams-and-access.md)),
point an MCP client at your `LINKEDIN_MCP_RESOURCE` URL, and sign in with LinkedIn when asked.
The tool metadata in `servers.yaml` marks `linkedin_create_post` as `high` trust and
`write`, so a grant can allow reading without allowing posting.

## Configuration

| Variable | Purpose |
|---|---|
| `LINKEDIN_MCP_RESOURCE` | This server's public MCP URL (the token audience). Required. |
| `LINKEDIN_MCP_ISSUER` | The MCP Auth issuer URL. Required. `LINKEDIN_JWKS_URL` overrides discovery. |
| `LINKEDIN_MCP_CLIENT_ID`, `LINKEDIN_MCP_CLIENT_KEY_ID` | The resource client registered in MCP Auth. Required. |
| `LINKEDIN_MCP_CLIENT_KEY` or `LINKEDIN_MCP_CLIENT_KEY_FILE` | The client's RSA private key (PEM), inline or as a file path; set exactly one. |
| `LINKEDIN_MCP_TOKEN_DIAL_ADDR` | Optional `host:port` the token exchange connects to instead of the issuer host, for clusters where the public issuer is not reachable. |
| `LINKEDIN_API_BASE`, `LINKEDIN_API_VERSION` | Defaults `https://api.linkedin.com` and the `Linkedin-Version` header. |
| `PORT`, `MCP_PATH` | Defaults `8080` and `/mcp`. |
| `LINKEDIN_MCP_ALLOW_INSECURE=true` | Plain http issuer, local development only. |

The server also serves `/health` and the OAuth protected-resource metadata under
`/.well-known/oauth-protected-resource`.

## Run standalone with Docker

You still need an MCP Auth issuer with this server registered as a resource and a resource
client (steps 1, 2 and 5 above).

```bash
docker build -t linkedin-mcp .
docker run --rm -p 8080:8080 \
  -e LINKEDIN_MCP_RESOURCE=https://mcp.example.com/linkedin/mcp \
  -e LINKEDIN_MCP_ISSUER=https://auth.example.com/mcp-auth \
  -e LINKEDIN_MCP_CLIENT_ID=linkedin-mcp \
  -e LINKEDIN_MCP_CLIENT_KEY_ID=linkedin-mcp-1 \
  -v "$PWD/linkedin-mcp-client.pem:/run/key.pem:ro" \
  -e LINKEDIN_MCP_CLIENT_KEY_FILE=/run/key.pem \
  linkedin-mcp
```

The image runs as a non-root user. Put it behind a TLS-terminating reverse proxy that serves
the public URL you set in `LINKEDIN_MCP_RESOURCE`. The image's key file must be readable by
UID 65532.

## Security notes

- Callers are authenticated by an MCP Auth access token whose audience is this server's URL;
  tokens for other resources are rejected.
- The caller's token is used only to perform the exchange. It is never logged or returned.
- LinkedIn tokens are held in memory for one tool call. The LinkedIn and token-exchange HTTP
  clients refuse redirects, so a token never follows a redirect to another host.
- The exchange refuses plain-http endpoints unless `LINKEDIN_MCP_ALLOW_INSECURE=true`.
- The private resource key is a secret: keep it in a Kubernetes Secret or a mounted file, never
  in git or an image.
- Posts publish only with `confirm: true` and the `tools:write` scope; treat a grant that
  allows `linkedin_create_post` as permission to post publicly as that person.
- To report a vulnerability, open a private security advisory on this repository.

## Develop

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

## CI and releases

`.github/workflows/ci.yaml` runs on pull requests and pushes to `main`: gofmt, `go vet`,
`go build`, `go test -race`, staticcheck, govulncheck, a Docker build and Trivy scans.

Pushing a `v*` tag from a commit on `main` runs `.github/workflows/release.yaml`. It re-runs
the tests, publishes a multi-arch (`linux/amd64`, `linux/arm64`) image with provenance and an
SBOM to `ghcr.io/tavric-labs/linkedin-mcp:<tag>` (and `:latest` for stable tags), and creates a
GitHub release with generated notes. Run the workflow manually with `dry_run` enabled to build
both platforms without pushing or releasing. Record user-visible changes in `CHANGELOG.md`.

## License

No license has been chosen yet. MCP Runtime is Apache-2.0.
