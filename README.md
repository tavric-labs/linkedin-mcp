# linkedin-mcp

A remote MCP server (streamable HTTP) that lets an AI agent read the signed-in person's own
LinkedIn profile and publish, edit and delete LinkedIn posts as them: text, link, image,
multi-image, video, document, poll and reshare. Every write is a **dry run unless
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
  changing anything needs `tools:write`. Every write tool previews by default, and the server
  refuses to confirm one for a token without `tools:write`.
- **Per-user consent.** People sign in with LinkedIn's real OAuth flow. No pasted browser
  cookies, no shared personal access token. Ending a person's MCP Auth session ends their
  access to LinkedIn through this server at once.
- **Remote and deployable.** Run it once for a team instead of a local stdio process on every
  laptop holding personal credentials.

What it does not do: it only writes the signed-in person's own posts. There is no search, no
messaging, no reading of posts, comments, reactions, analytics or other people's profiles
(see [What is not possible](#what-is-not-possible-and-why)), and it depends on MCP Auth and, for the audit and
policy benefits above, on MCP Runtime's gateway. Run standalone, you get the identity and the
dry-run default but no platform audit or grants.

## Tools

Every write tool is a **dry run unless `confirm: true`**: a dry run validates the input and
returns a preview, makes no LinkedIn call, downloads nothing and needs only `tools:read`.
Confirming needs `tools:write`. LinkedIn product: **Sign In** is "Sign In with LinkedIn using
OpenID Connect" (`openid`, `profile`, `email`); **Share** is "Share on LinkedIn"
(`w_member_social`), which is self-serve.

| Tool | Scope | LinkedIn product and endpoint | Notes |
|---|---|---|---|
| `linkedin_get_profile` | `tools:read` | Sign In: `GET /v2/userinfo` | Name and member id; also given and family name, email, locale and picture when LinkedIn shares them. |
| `linkedin_create_post` | `tools:write` to confirm | Share: `POST /rest/posts` | Text up to 3000 characters; visibility `PUBLIC` (default) or `CONNECTIONS`; `disable_reshare`. |
| `linkedin_create_article_post` | `tools:write` to confirm | Share: `POST /rest/posts` (+ Images API for the card image) | A link post. LinkedIn does not read the page, so you give the title, optional description and optional thumbnail (URL or base64). `url` must be https. |
| `linkedin_create_image_post` | `tools:write` to confirm | Share: Images API `initializeUpload` + upload, then `POST /rest/posts` | 1 to 20 images (JPEG, PNG, GIF; 10 MB each, 40 MB total), URL or base64, optional alt text. Two or more make a multi-image post. |
| `linkedin_create_video_post` | `tools:write` to confirm | Share: Videos API `initializeUpload`, part uploads, `finalizeUpload`, then `POST /rest/posts` | One MP4, 75 KB to 100 MB from a URL (10 MB as base64). LinkedIn processes it afterwards, so it may take minutes to appear. |
| `linkedin_create_document_post` | `tools:write` to confirm | Share: Documents API `initializeUpload` + upload, then `POST /rest/posts` | PDF, PPT, PPTX, DOC or DOCX, up to 50 MB from a URL (10 MB as base64); a title is required. |
| `linkedin_create_poll_post` | `tools:write` to confirm | Share: `POST /rest/posts` with poll content | Question up to 140 characters, 2 to 4 options of up to 30, duration `ONE_DAY`, `THREE_DAYS` (default), `SEVEN_DAYS` or `FOURTEEN_DAYS`. |
| `linkedin_reshare_post` | `tools:write` to confirm | Share: `POST /rest/posts` with `reshareContext.parent` | `post_urn` is `urn:li:share:<id>` or `urn:li:ugcPost:<id>`; optional commentary. |
| `linkedin_edit_post` | `tools:write` to confirm | Share: `POST /rest/posts/{urn}` partial update | Replaces the text of your own post. Only the text can be edited. |
| `linkedin_delete_post` | `tools:write` to confirm | Share: `DELETE /rest/posts/{urn}` | Permanently deletes your own post. |
| `linkedin_create_organization_post` | `tools:write` to confirm | Community Management API: `POST /rest/posts` as `urn:li:organization:<id>` | **Registered only when `LINKEDIN_MCP_ENABLE_ORG_TOOLS=true`.** Needs `w_organization_social` and a page role; see below. |

Post text is plain text. Characters LinkedIn's "little" format reserves (`( ) [ ] { } < > @ | * _ ~ \`)
are escaped so they show literally; `#hashtags` still work. Mentions are not supported.

Examples (what a model sends, shown as arguments):

```json
{"tool": "linkedin_create_post", "arguments": {"text": "Shipped v0.2.0 today.", "visibility": "PUBLIC"}}
```

The same call with `"confirm": true` publishes it. A multi-image post:

```json
{"tool": "linkedin_create_image_post", "arguments": {
  "text": "Conference notes",
  "images": [
    {"url": "https://example.com/slide1.png", "alt_text": "Opening keynote"},
    {"base64": "iVBORw0KGgo...", "alt_text": "Whiteboard"}
  ],
  "confirm": true}}
```

### Files from URLs and base64

An image, document or video comes as exactly one of `url` or `base64`. A URL must be `https`
on port 443 with no credentials, and the server refuses loopback, private, link-local,
carrier-grade NAT and other reserved addresses. It checks the address it actually connects to,
so a hostname that resolves to a private address, or a redirect to one, fails too. It follows
at most three redirects, uses no proxy and sends no credentials, and stops reading at the
size cap. Files are identified by their bytes (JPEG, PNG, GIF, PDF, OOXML, MP4), never by
the name or `Content-Type` a server claims, and images over LinkedIn's pixel limit are
refused. A URL is downloaded only when `confirm` is `true`; a dry run only checks its syntax.

## What is not possible and why

LinkedIn gates these behind products and permissions that a self-serve app does not have.
This server does not fake them.

### Requires LinkedIn Community Management API access

The Community Management API is a vetted product with an application and review. LinkedIn
asks that it be requested on an app that has no other products, so it means a separate app
and its own sign-in wiring. Until you have it, these are **not available**:

- Reading your own posts or anyone's (`GET /rest/posts`): `r_member_social`, a closed permission
  LinkedIn is not granting.
- Comments and reactions (likes) on posts, creating, reading or deleting them: LinkedIn's
  current Comments and Reactions API pages require `w_member_social_feed` and
  `r_member_social_feed`, which belong to the Community Management API. `w_member_social`
  alone is not documented to allow them, so the tools are left out rather than guessed.
- Post, follower, page and video analytics, follower statistics.
- Organization pages: reading them, managing them, and their analytics
  (`r_organization_social`, `rw_organization_admin`). Posting to a page is the optional tool
  above, which needs `w_organization_social`.
- Reading other people's profiles, searching, messaging and connections: no self-serve API.

### Optional: organization posting

Set `LINKEDIN_MCP_ENABLE_ORG_TOOLS=true` to register `linkedin_create_organization_post`. It is
only useful when the LinkedIn app has the Community Management API **and** the connector in
`.mcp/auth-resource.yaml` requests `w_organization_social`, **and** the signed-in person is an
administrator, content admin or direct sponsored content poster of that page. Without the
permission LinkedIn answers 403 and the tool reports that the person's LinkedIn access does
not allow it. Uncomment the tool in `.mcp/servers.yaml` too, since its policy denies tools
that are not listed.

### Not verified against live LinkedIn

All requests are tested against fakes built from LinkedIn's documentation, never the live API.
Two behaviors depend on LinkedIn accepting a plain member token and are worth a manual check
after your first deploy: `linkedin_edit_post` and `linkedin_delete_post` (the documentation
does not list a separate permission for them), and video processing after upload.

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
- Only the signed-in person's own profile and posts. Share on LinkedIn allows about 150
  requests per member per day, and a multi-image post uses one request per image plus two.
- LinkedIn's Images API cannot be read back with `w_member_social` alone, so the server does
  not check an image's processing status; a bad image fails when LinkedIn publishes the post.

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
mcp-runtime server build image linkedin --tag v0.2.0
mcp-runtime server push --image <registry>/<team>/linkedin-mcp/linkedin:v0.2.0 --scope tenant
mcp-runtime server deploy linkedin --scope tenant --metadata-dir .mcp
```

Use the exact image ref that `server build image` prints, and set `image` and `imageTag` in
`servers.yaml` to match.

**Deploying with `--scope org`** (a server shared by the whole platform instead of one team).
We hit both of these on the live deployment:

- The image path must be `registry.<host>/org/<name>`, for example
  `registry.mcpruntime.org/org/linkedin-mcp/linkedin`, and you push with `--scope org`. A
  tenant path such as `<registry>/<team>/...` is not accepted for an org-scope server.
- The server runs in the namespace `mcp-servers-org`, not `mcp-servers`, so the
  `linkedin-mcp-client-key` Secret from step 2 must be created there
  (`kubectl -n mcp-servers-org create secret generic ...`). A Secret in `mcp-servers` leaves the
  pod stuck waiting for it.

```bash
mcp-runtime server push --image registry.<host>/org/linkedin-mcp/linkedin:v0.2.0 --scope org
mcp-runtime server deploy linkedin --scope org --metadata-dir .mcp
```
 `server init` can scaffold metadata for a new server, but this repo
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
The tool metadata in `servers.yaml` marks every posting tool as `high` trust and `write`, so a
grant can allow reading without allowing posting. The server's policy is `allow-list` with
`defaultDecision: deny`: a tool that is not listed in `servers.yaml` is denied, so add any
tool you add to the code there too (a test fails if they drift). Grants that predate v0.2.0
cover only the tools they named.

## Configuration

| Variable | Purpose |
|---|---|
| `LINKEDIN_MCP_RESOURCE` | This server's public MCP URL (the token audience). Required. |
| `LINKEDIN_MCP_ISSUER` | The MCP Auth issuer URL. Required. `LINKEDIN_JWKS_URL` overrides discovery. |
| `LINKEDIN_MCP_CLIENT_ID`, `LINKEDIN_MCP_CLIENT_KEY_ID` | The resource client registered in MCP Auth. Required. |
| `LINKEDIN_MCP_CLIENT_KEY` or `LINKEDIN_MCP_CLIENT_KEY_FILE` | The client's RSA private key (PEM), inline or as a file path; set exactly one. |
| `LINKEDIN_MCP_TOKEN_DIAL_ADDR` | Optional `host:port` the token exchange connects to instead of the issuer host, for clusters where the public issuer is not reachable. |
| `LINKEDIN_API_BASE`, `LINKEDIN_API_VERSION` | Defaults `https://api.linkedin.com` and the `Linkedin-Version` header (`202610`). LinkedIn retires each version about a year after release; HTTP 426 `NONEXISTENT_VERSION` means set a newer one. |
| `LINKEDIN_MCP_ENABLE_ORG_TOOLS=true` | Registers `linkedin_create_organization_post`. Off by default; needs the Community Management API (see above). |
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
- Writes happen only with `confirm: true` and the `tools:write` scope; treat a grant that
  allows any posting tool as permission to act publicly as that person, including editing and
  deleting their posts.
- URLs the caller supplies are fetched by the server, so they are restricted as described under
  [Files from URLs and base64](#files-from-urls-and-base64). Upload URLs LinkedIn returns must be
  `https` on a `linkedin.com` host before any byte or token is sent to them.
- Post text and file contents are never logged; the log records the subject, tool, outcome and
  counts, plus LinkedIn's status and error code on a failure.
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

Licensed under the [Apache License 2.0](LICENSE), the same license as [MCP Runtime](https://github.com/tavric-labs/mcp-runtime).
