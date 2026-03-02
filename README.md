# PostFlow

> ⚠️ **Disclaimer**: PostFlow is currently in active development. It is not yet tested enough to guarantee correct behavior in all scenarios. Use it at your own discretion and risk.

API-first social publisher built for LLM workflows, with a lightweight worker and multi-network, multi-account support via `account_id`.

Current status (v2):
- Supported networks: `x`, `linkedin`, `facebook`, `instagram`
- Model: `1 post = 1 account_id`
- Per-account auth: `static` or `oauth`
- App secrets from env + encrypted account credentials in SQLite (AES-256-GCM)
- Web UI with scheduler/composer and account management in `settings`

## Quickstart

```bash
cp .env.example .env
# required key (32 bytes in base64)
openssl rand -base64 32
# put the value into PUBLISHER_MASTER_KEY

go run ./cmd/publisher
```

Server runs at `http://localhost:8080`.

The app loads variables from `.env` when present:
- Shell-exported variables take precedence.
- You can use another file with `ENV_FILE=/path/to/other.env`.

## CLI (`postflow`)

```bash
go run ./cmd/postflow --help
```

Examples:

```bash
go run ./cmd/postflow schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
go run ./cmd/postflow posts create --account-id acc_xxx --text "hello" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/postflow posts validate --account-id acc_xxx --text "hello" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/postflow dlq list --limit 50
go run ./cmd/postflow dlq requeue --id dlq_xxx
```

Homebrew install (tap):

```bash
brew tap antoniolg/tap
brew install antoniolg/tap/postflow
postflow --help
```

## Quality Gate (CI)

Runs automatically on:
- every `pull_request`
- every push to `main`

Included checks:
- `gofmt` (fails if files are not formatted)
- `go mod tidy` (fails if `go.mod/go.sum` changes)
- `golangci-lint` (config in `.golangci.yml`)
- `go build ./...`
- `go test ./...`
- `go test -race ./...`
- minimum global coverage (`50%`)
- minimum coverage by critical package:
  - `internal/worker`: `80%`
  - `internal/api`: `60%`
  - `internal/cli`: `40%`
  - `internal/db`: `55%`
  - `internal/publisher`: `50%`
- `govulncheck`

Threshold update policy:
- Thresholds are configured via CI job env vars in `.github/workflows/quality.yml`.
- If stable coverage improves over multiple PRs, raise thresholds gradually (`+2` to `+5`).
- If a structural refactor adds temporary noise, fix tests before lowering thresholds.

Surface parity (LLM-first):
- `internal/parity` includes executable black-box contracts for `API`, `MCP`, and `CLI`.
- Parity is validated for both success and error paths on shared capabilities.
- Tests also generate a machine-readable capability matrix artifact.

## Release Docker image (GHCR)

Docker image publishing is automated in GitHub Actions and runs only on published releases.

- Workflow: `.github/workflows/release-image.yml`
- Registry: `ghcr.io`
- Image: `ghcr.io/antoniolg/postflow`
- Tags on each release:
  - `<release-tag>` (for example `v1.2.0`)
  - `latest`

Coolify can deploy directly from this image without compiling on the server:

- `ghcr.io/antoniolg/postflow:latest` (rolling)
- `ghcr.io/antoniolg/postflow:vX.Y.Z` (pinned)

## Release CLI artifacts + Homebrew tap

CLI release automation runs in `.github/workflows/release-cli-homebrew.yml`:

- Trigger:
  - on published GitHub release
  - manual dispatch with a specific tag (`workflow_dispatch`)
- Uploads release assets:
  - `postflow_<version>_darwin_amd64.tar.gz`
  - `postflow_<version>_darwin_arm64.tar.gz`
  - `postflow_<version>_linux_amd64.tar.gz`
  - `postflow_<version>_linux_arm64.tar.gz`
- Updates formula `Formula/postflow.rb` in `antoniolg/homebrew-tap`

Required secret (in `antoniolg/postflow` GitHub repo):

- `HOMEBREW_TAP_GITHUB_TOKEN` with write access to `antoniolg/homebrew-tap`

## Database Migrations (SQLite, safe)

- Startup runs **versioned, non-destructive migrations** (`schema_migrations` table).
- No schema reset/drop is performed on upgrades.
- If pending migrations are detected on an existing DB, the app creates a local pre-migration snapshot:
  - `<DATABASE_PATH>.bak-YYYYMMDDTHHMMSSZ`
  - plus `-wal` / `-shm` sidecars when present.
- On migration failure, startup aborts and keeps current data untouched.

## Architecture

Reference docs:
- `docs/architecture.md` (overall view and layering rules)
- `docs/adr/0001-modular-monolith-application-layer.md` (adopted architecture decision)

## Configuration

Main variables:
- `PORT` (default: `8080`)
- `DATABASE_PATH` (default: `publisher.db`)
- `DATA_DIR` (default: `data`)
- `WORKER_INTERVAL_SECONDS` (default: `30`)
- `RETRY_BACKOFF_SECONDS` (default: `30`)
- `DEFAULT_MAX_RETRIES` (default: `3`)
- `RATE_LIMIT_RPM` (default: `120`, `0` to disable)
- `API_TOKEN` (Bearer or `X-API-Key`)
- `UI_BASIC_USER`
- `UI_BASIC_PASS`
- `LOG_LEVEL` (`debug|info|warn|error`, default: `info`)
- `PUBLISHER_DRIVER` (`mock` by default, `live` for real publishing; `x` accepted as legacy alias)

Security/OAuth (v2):
- `PUBLISHER_MASTER_KEY` (required, base64 32 bytes)
- `PUBLIC_BASE_URL` (public base URL for OAuth callbacks)
- `LINKEDIN_CLIENT_ID`
- `LINKEDIN_CLIENT_SECRET`
- `META_APP_ID`
- `META_APP_SECRET`

X (static bootstrap account `x-default`):
- `X_API_BASE_URL` (default: `https://api.twitter.com`)
- `X_UPLOAD_BASE_URL` (default: `https://upload.twitter.com`)
- `X_API_KEY`
- `X_API_SECRET`
- `X_ACCESS_TOKEN`
- `X_ACCESS_TOKEN_SECRET`

## API quick reference (v2)

### 1) List accounts

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/accounts
```

### 2) Create static account

```bash
curl -X POST http://localhost:8080/accounts/static \
  -H 'content-type: application/json' \
  -d '{
    "platform": "x",
    "display_name": "X Default",
    "external_account_id": "x-default",
    "credentials": {
      "access_token": "...",
      "access_token_secret": "...",
      "token_type": "oauth1"
    }
  }'
```

### 3) Start OAuth

```bash
curl -X POST http://localhost:8080/oauth/linkedin/start
curl -X POST http://localhost:8080/oauth/facebook/start
curl -X POST http://localhost:8080/oauth/instagram/start
```

### 4) Upload media

```bash
curl -X POST http://localhost:8080/media \
  -F kind=video \
  -F file=@./clip.mp4
```

### 5) Create post (draft or scheduled)

Get a valid `account_id` first using `GET /accounts`.

```bash
curl -X POST http://localhost:8080/posts \
  -H 'content-type: application/json' \
  -H 'Idempotency-Key: short-2026-02-25-001' \
  -d '{
    "account_id": "acc_xxx",
    "text": "new short clip",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
  }'
```

### 6) Validate post (dry-run)

```bash
curl -X POST http://localhost:8080/posts/validate \
  -H 'content-type: application/json' \
  -d '{
    "account_id": "acc_xxx",
    "text": "new short clip",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
  }'
```

### 7) Auxiliary operations

```bash
curl 'http://localhost:8080/schedule'
curl -X POST http://localhost:8080/posts/pst_xxx/cancel
curl 'http://localhost:8080/dlq?limit=50'
curl -X POST http://localhost:8080/dlq/dlq_xxx/requeue
curl -X POST http://localhost:8080/dlq/dlq_xxx/delete
curl -X POST http://localhost:8080/dlq/delete -H 'content-type: application/json' -d '{"ids":["dlq_xxx","dlq_yyy"]}'
curl -X POST http://localhost:8080/accounts/acc_xxx/disconnect
curl -X DELETE http://localhost:8080/accounts/acc_xxx
```

## UI

- `GET /`: scheduler/composer (account selector is required)
- `GET /accounts`: JSON API to list accounts (`text/html` redirects to `/?view=settings`)
- `GET /?view=settings`: UI timezone + connected accounts block + MCP settings

## MCP (streamable HTTP)

Endpoint:

```bash
http://localhost:8080/mcp
```

Exposed tools:
- `publisher_list_schedule`
- `publisher_list_drafts`
- `publisher_list_failed`
- `publisher_create_post` (requires `account_id`)
- `publisher_upload_media` (without `platform`)
- `publisher_list_media`
- `publisher_delete_media`

Claude Code example:

```bash
claude mcp add -t http publisher http://localhost:8080/mcp --header "Authorization: Bearer <API_TOKEN>"
```

Codex example:

```bash
codex mcp add publisher --url http://localhost:8080/mcp
```
