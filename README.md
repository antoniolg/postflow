# postflow

> ⚠️ **Disclaimer**: postflow is in active development. It is not tested enough yet to guarantee correct behavior in all scenarios. Use at your own risk.

postflow is a lightweight social publishing service with:
- Web UI
- HTTP API
- MCP endpoint (Streamable HTTP)
- CLI (`postflow`)

This README is a **basic setup guide**.

## 1) Local Setup (5 minutes)

### Requirements
- Go 1.26.1+
- Optional: Homebrew (for installing CLI binary)

### Start locally

```bash
git clone https://github.com/antoniolg/postflow.git
cd postflow
cp .env.example .env
```

Generate required secrets:

```bash
# 32-byte base64 key (required)
openssl rand -base64 32

# API token (recommended)
openssl rand -hex 32
```

Put those values in `.env`:

```dotenv
POSTFLOW_MASTER_KEY=<base64-from-openssl>
API_TOKEN=<hex-token>
PUBLIC_BASE_URL=http://localhost:8080
OWNER_EMAIL=owner@example.com
OWNER_PASSWORD_HASH=<bcrypt-hash>
POSTFLOW_DRIVER=mock
```

Generate `OWNER_PASSWORD_HASH` with the helper script in this repo:

```bash
go run ./scripts/hash-password.go 'replace-with-your-password'
```

If you store it in a local `.env`, quote the value because bcrypt hashes contain `$`:

```dotenv
OWNER_PASSWORD_HASH='$2a$10$...'
```

Run:

```bash
go run ./cmd/postflow-server
```

Open:
- UI: `http://localhost:8080`
- MCP: `http://localhost:8080/mcp`

---

## 2) Environment Variables (and where to get them)

Use `.env.example` as template. These are the key ones:

### Core (recommended in all setups)

| Variable | Required | Where it comes from |
|---|---:|---|
| `POSTFLOW_MASTER_KEY` | Yes | Generate locally: `openssl rand -base64 32` |
| `API_TOKEN` | Recommended | Generate locally (random token), kept for API/MCP auth for CLI, Codex, Claude, and other legacy clients |
| `OWNER_EMAIL` | Recommended for UI/ChatGPT | Owner email for the single-user local login |
| `OWNER_PASSWORD_HASH` | Recommended for UI/ChatGPT | Bcrypt hash for the owner password |
| `PUBLIC_BASE_URL` | Yes for OAuth and Instagram media URLs | Your app URL (`http://localhost:8080` locally, your public HTTPS domain in prod) |
| `UI_BASIC_USER` / `UI_BASIC_PASS` | Temporary compatibility only | Optional legacy Basic Auth fallback for the UI |

### Storage/runtime

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DATABASE_PATH` | `postflow.db` | SQLite DB path |
| `DATA_DIR` | `data` | Uploaded media path |

### Network credentials (only if you use that network)

| Network | Variables | Where to get them |
|---|---|---|
| X | `X_CLIENT_ID`, `X_CLIENT_SECRET` | X Developer Portal OAuth 2.0 app credentials for account connection |
| LinkedIn | `LINKEDIN_CLIENT_ID`, `LINKEDIN_CLIENT_SECRET` | LinkedIn Developer app with member posting enabled. To connect company pages, the app must also have organization posting/admin scopes approved. |
| Facebook/Instagram | `META_APP_ID`, `META_APP_SECRET` | Meta Developers app |

Important:
- If you want real publishing, set `POSTFLOW_DRIVER=live`.
- For local testing without real publishing, keep `POSTFLOW_DRIVER=mock`.
- OAuth account connection is available for X, LinkedIn, Facebook, and Instagram.
- LinkedIn OAuth connects the personal profile and, when available, any organization pages the user administers.
- LinkedIn root posts with a first `http(s)` link and no attached media are published as article posts at publish time so PostFlow can send explicit unfurl metadata. If media is attached, media wins and link unfurl is skipped.
- In the web UI, if an OAuth provider returns multiple accounts, PostFlow shows a selection step before saving them.
- In production (Coolify), set secrets in the platform UI, not in committed files.
- In Coolify, mark `OWNER_PASSWORD_HASH` as a literal/secret value so `$` is not interpolated.

---

## 3) MCP Setup (for LLMs)

Endpoint:

```text
http://localhost:8080/mcp
```

For Codex, Claude, CLI, and other legacy clients, if `API_TOKEN` is set, send:

```text
Authorization: Bearer <API_TOKEN>
```

For ChatGPT / remote MCP clients with OAuth:

- Authorization metadata: `http://localhost:8080/.well-known/oauth-authorization-server`
- Protected resource metadata: `http://localhost:8080/.well-known/oauth-protected-resource`
- The login page is `http://localhost:8080/login`
- Dynamic client registration is available at `POST /oauth/register`
- PostFlow allows MCP discovery requests without auth (`initialize`, `notifications/initialized`, `ping`, and `tools/list`) so ChatGPT can complete the handshake cleanly.
- Actual MCP tool execution (`tools/call`) remains protected and requires OAuth bearer auth (or the legacy `API_TOKEN` flow for non-OAuth clients).

Main MCP tools available:
- `postflow_health`
- `postflow_list_schedule`
- `postflow_list_drafts`
- `postflow_list_accounts`
- `postflow_create_static_account`
- `postflow_connect_account`
- `postflow_disconnect_account`
- `postflow_set_x_premium`
- `postflow_delete_account`
- `postflow_list_failed`
- `postflow_create_post`
- `postflow_cancel_post`
- `postflow_schedule_post`
- `postflow_edit_post`
- `postflow_delete_post`
- `postflow_validate_post`
- `postflow_upload_media`
- `postflow_list_media`
- `postflow_delete_media`
- `postflow_requeue_failed`
- `postflow_delete_failed`
- `postflow_set_timezone`

Thread payload support (same shape in API/MCP/CLI):
- `segments`: `[{ "text": "...", "media_ids": ["med_x"] }]`
- If `segments` is present, step `1` is the root post and steps `2..N` are follow-ups.
- Publishing semantics: X follow-ups are chained as replies; other supported thread platforms publish follow-ups as comments on the root post.
- Backward compatibility is preserved for legacy `text` + `media_ids`.
- `postflow_edit_post` accepts optional `media_ids` to replace media on editable posts (`[]` clears all media).
- Editing without `intent` and without `scheduled_at` preserves the current scheduling state.

### Codex CLI

```bash
codex mcp add postflow --url http://localhost:8080/mcp
```

`~/.codex/config.toml` example:

```toml
[mcp_servers.postflow]
url = "http://localhost:8080/mcp"
bearer_token_env_var = "POSTFLOW_API_TOKEN"
```

Then:

```bash
export POSTFLOW_API_TOKEN="<same-value-as-API_TOKEN>"
```

### Claude Code

```bash
claude mcp add -t http postflow http://localhost:8080/mcp --header "Authorization: Bearer <API_TOKEN>"
```

Tip: in the app UI (`settings`) you can copy ready-to-use MCP snippets for Claude and Codex.

---

## 4) CLI Setup (`postflow`)

### Option A: Homebrew (recommended)

```bash
brew tap antoniolg/tap
brew install antoniolg/tap/postflow
postflow --help
```

### Option B: Run from source

```bash
go run ./cmd/postflow --help
```

Configure CLI env:

```bash
export POSTFLOW_BASE_URL="http://localhost:8080"
export POSTFLOW_API_TOKEN="<API_TOKEN>"
```

Common commands:

```bash
postflow health
postflow schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
postflow schedule list --view posts --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
postflow drafts list --limit 20
postflow posts validate --account-id acc_xxx --text "hello"
postflow posts validate --account-id acc_xxx --segments-json '[{"text":"root"},{"text":"reply 1"}]'
postflow posts create --account-id acc_xxx --segments-json '[{"text":"root"},{"text":"reply 1","media_ids":["med_x"]}]' --scheduled-at 2026-03-01T10:00:00Z
postflow posts schedule --id pst_xxx --scheduled-at 2026-03-01T10:00:00Z
postflow posts edit --id pst_xxx --text "copy updated" --intent schedule --scheduled-at 2026-03-01T10:30:00Z
postflow posts edit --id pst_xxx --segments-json '[{"text":"root updated"},{"text":"reply updated"}]'
postflow posts edit --id pst_xxx --text "copy + media" --replace-media --media-id med_a --media-id med_b
postflow posts delete --id pst_xxx
postflow posts cancel --id pst_xxx
postflow accounts list
postflow settings set-timezone --timezone Europe/Madrid
postflow media list --limit 20
```

`--text` and `--segments-json` are mutually exclusive on `posts create`, `posts validate`, and `posts edit`.

`schedule list` returns grouped publications by default. Use `--view posts` to inspect the raw per-post/thread rows.

---

## 5) Deploy (Coolify + GHCR image)

You can deploy from prebuilt image (no build on server):

```text
ghcr.io/antoniolg/postflow:latest
```

or pinned:

```text
ghcr.io/antoniolg/postflow:vX.Y.Z
```

Full production runbook:
- [docs/coolify-deploy.md](docs/coolify-deploy.md)

---

## 6) Troubleshooting

- `401 unauthorized`:
  - check `API_TOKEN`
  - check `Authorization: Bearer ...` in MCP/API clients
- OAuth callback errors:
  - verify `PUBLIC_BASE_URL` matches your real public domain
  - for X, verify `X_CLIENT_ID` is set and the callback URL is registered in the X app settings
- Instagram media create errors (`code=9004`, `error_subcode=2207052`):
  - verify `PUBLIC_BASE_URL` is public/reachable from the internet (not `localhost` in production)
  - for image posts, upload JPEG or PNG (`.jpg` / `.jpeg` / `.png`)
  - for video posts, use MP4 or MOV
  - media uploads are capped at 512 MiB
- CLI auth errors:
  - verify `POSTFLOW_API_TOKEN` matches server `API_TOKEN`

---

## 7) Additional Docs

- API contract: [docs/specs/openapi.yaml](docs/specs/openapi.yaml)
- Deployment guide: [docs/coolify-deploy.md](docs/coolify-deploy.md)
