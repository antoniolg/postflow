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
- Go 1.24+
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
PUBLISHER_MASTER_KEY=<base64-from-openssl>
API_TOKEN=<hex-token>
PUBLIC_BASE_URL=http://localhost:8080
UI_BASIC_USER=admin
UI_BASIC_PASS=<strong-password>
PUBLISHER_DRIVER=mock
```

Run:

```bash
go run ./cmd/publisher
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
| `PUBLISHER_MASTER_KEY` | Yes | Generate locally: `openssl rand -base64 32` |
| `API_TOKEN` | Recommended | Generate locally (random token), used for API/MCP auth |
| `PUBLIC_BASE_URL` | Yes for OAuth | Your app URL (`http://localhost:8080` locally, your domain in prod) |
| `UI_BASIC_USER` / `UI_BASIC_PASS` | Recommended | Values you define for UI basic auth |

### Storage/runtime

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DATABASE_PATH` | `publisher.db` | SQLite DB path |
| `DATA_DIR` | `data` | Uploaded media path |

### Network credentials (only if you use that network)

| Network | Variables | Where to get them |
|---|---|---|
| X | `X_API_KEY`, `X_API_SECRET`, `X_ACCESS_TOKEN`, `X_ACCESS_TOKEN_SECRET` | X Developer Portal app credentials/tokens |
| LinkedIn | `LINKEDIN_CLIENT_ID`, `LINKEDIN_CLIENT_SECRET` | LinkedIn Developer app |
| Facebook/Instagram | `META_APP_ID`, `META_APP_SECRET` | Meta Developers app |

Important:
- If you want real publishing, set `PUBLISHER_DRIVER=live`.
- For local testing without real publishing, keep `PUBLISHER_DRIVER=mock`.
- In production (Coolify), set secrets in the platform UI, not in committed files.

---

## 3) MCP Setup (for LLMs)

Endpoint:

```text
http://localhost:8080/mcp
```

If `API_TOKEN` is set, send:

```text
Authorization: Bearer <API_TOKEN>
```

Main MCP tools available:
- `publisher_health`
- `publisher_list_schedule`
- `publisher_list_drafts`
- `publisher_list_accounts`
- `publisher_create_static_account`
- `publisher_connect_account`
- `publisher_disconnect_account`
- `publisher_set_x_premium`
- `publisher_delete_account`
- `publisher_list_failed`
- `publisher_create_post`
- `publisher_cancel_post`
- `publisher_schedule_post`
- `publisher_edit_post`
- `publisher_delete_post`
- `publisher_validate_post`
- `publisher_upload_media`
- `publisher_list_media`
- `publisher_delete_media`
- `publisher_requeue_failed`
- `publisher_delete_failed`
- `publisher_set_timezone`

### Codex CLI

```bash
codex mcp add publisher --url http://localhost:8080/mcp
```

`~/.codex/config.toml` example:

```toml
[mcp_servers.publisher]
url = "http://localhost:8080/mcp"
bearer_token_env_var = "PUBLISHER_API_TOKEN"
```

Then:

```bash
export PUBLISHER_API_TOKEN="<same-value-as-API_TOKEN>"
```

### Claude Code

```bash
claude mcp add -t http publisher http://localhost:8080/mcp --header "Authorization: Bearer <API_TOKEN>"
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
export PUBLISHER_BASE_URL="http://localhost:8080"
export PUBLISHER_API_TOKEN="<API_TOKEN>"
```

Common commands:

```bash
postflow health
postflow schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
postflow drafts list --limit 20
postflow posts validate --account-id acc_xxx --text "hello"
postflow posts schedule --id pst_xxx --scheduled-at 2026-03-01T10:00:00Z
postflow posts edit --id pst_xxx --text "copy updated" --intent schedule --scheduled-at 2026-03-01T10:30:00Z
postflow posts delete --id pst_xxx
postflow posts cancel --id pst_xxx
postflow accounts list
postflow accounts create-static --platform x --external-account-id x-default --credential access_token=... --credential access_token_secret=...
postflow settings set-timezone --timezone Europe/Madrid
postflow media list --limit 20
```

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
- CLI auth errors:
  - verify `PUBLISHER_API_TOKEN` matches server `API_TOKEN`

---

## 7) Additional Docs

- API contract: [docs/specs/openapi.yaml](docs/specs/openapi.yaml)
- Deployment guide: [docs/coolify-deploy.md](docs/coolify-deploy.md)
