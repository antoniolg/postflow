# Coolify Deploy Runbook

## 1) Create the service

### Option A (recommended): deploy from prebuilt GHCR image

1. In Coolify, create a new service from **Docker Image**.
2. Image: `ghcr.io/antoniolg/postflow:latest` (or pin a tag like `ghcr.io/antoniolg/postflow:vX.Y.Z`).
3. Set application internal port to `8080` in Coolify (do not publish host port `8080` via Docker `ports`).
4. Attach a persistent volume mounted at `/srv/data`.
5. If the package is private, add registry credentials in Coolify:
   - Registry: `ghcr.io`
   - Username: your GitHub user
   - Password: GitHub PAT with `read:packages`

### Option B: deploy from repository build

1. In Coolify, create a new service from Git repository `antoniolg/postflow`.
2. Use branch `main` and enable auto-deploy on new commits.
3. Build using project `Dockerfile`.
4. Set application internal port to `8080` in Coolify (do not publish host port `8080` via Docker `ports`).
5. Attach a persistent volume mounted at `/srv/data`.

## 1.1) How GHCR image publishing works

- GitHub Actions publishes Docker images to `ghcr.io/antoniolg/postflow`.
- Trigger: only when a GitHub Release is published.
- Tags pushed on each release:
  - release tag name (for example `v1.2.0`)
  - `latest`

## 2) Required environment variables

Set these in Coolify:

- `PORT=8080`
- `DATABASE_PATH=/srv/data/postflow.db`
- `DATA_DIR=/srv/data/media`
- `API_TOKEN=<long-random-token>`
- `OWNER_EMAIL=<your-email>`
- `OWNER_PASSWORD_HASH=<bcrypt-hash>`
- `POSTFLOW_MASTER_KEY=<base64-32-bytes>`
- `PUBLIC_BASE_URL=https://<your-coolify-domain>` (must be publicly reachable; do not use `localhost` in production)

Optional recommended:

- `LOG_LEVEL=info`
- `RATE_LIMIT_RPM=120`
- `WORKER_INTERVAL_SECONDS=30`
- `RETRY_BACKOFF_SECONDS=30`
- `DEFAULT_MAX_RETRIES=3`
- `UI_BASIC_USER=<your-user>` and `UI_BASIC_PASS=<long-password>` only if you want the temporary legacy Basic Auth fallback for the UI

For real X publishing:

- `POSTFLOW_DRIVER=live` (`x` also works as a legacy alias)
- `X_API_KEY=...`
- `X_API_SECRET=...`
- `X_ACCESS_TOKEN=...`
- `X_ACCESS_TOKEN_SECRET=...`

For LinkedIn OAuth:
- `LINKEDIN_CLIENT_ID=...`
- `LINKEDIN_CLIENT_SECRET=...`
- Ensure the LinkedIn app is approved for organization admin/posting scopes if you want company page connections.

For Facebook/Instagram OAuth:
- `META_APP_ID=...`
- `META_APP_SECRET=...`

Notes:

- For local development, use `.env` (template available at `.env.example`).
- In Coolify, configure secrets in the service environment UI, not in a committed `.env`.
- In Coolify, mark `OWNER_PASSWORD_HASH` as a literal value (`isLiteral`) so the `$` characters in the bcrypt hash are not expanded.
- For ChatGPT MCP usage, PostFlow keeps `tools/call` protected with OAuth, but allows MCP handshake/discovery requests (`initialize`, `notifications/initialized`, `ping`, `tools/list`) without auth to avoid reconnect loops during setup.
- For Instagram publishing, keep `PUBLIC_BASE_URL` reachable by Meta crawlers. PostFlow serves signed media URLs under `/media/` and a public `robots.txt` that allows that path; do not add proxy-level auth or bot rules that block either one.

## 3) Post-deploy smoke test

Run these from local terminal against your public URL:

```bash
export BASE_URL="https://<your-coolify-domain>"
export API_TOKEN="<same-token-as-coolify>"

curl -H "Authorization: Bearer $API_TOKEN" "$BASE_URL/healthz"
curl -H "Authorization: Bearer $API_TOKEN" "$BASE_URL/schedule"
curl -H "Authorization: Bearer $API_TOKEN" "$BASE_URL/accounts"
curl -H "Authorization: Bearer $API_TOKEN" \
  -H 'content-type: application/json' \
  -X POST "$BASE_URL/posts/validate" \
  -d '{"account_id":"<account-id-from-/accounts>","text":"smoke","scheduled_at":"2026-03-01T10:00:00Z"}'
```

Expected:

- `/healthz` returns `{"status":"ok"}`
- `/schedule` returns `200`
- `/accounts` returns `200`
- `/posts/validate` returns `200` and `valid: true`

## 4) Backup strategy with persistent volume

Because this app uses SQLite + local media files:

- Schema upgrades run automatically at startup using non-destructive migrations.
- When there are pending migrations on an existing DB, the app creates a local snapshot first:
  - `/srv/data/postflow.db.bak-YYYYMMDDTHHMMSSZ` (and `-wal`/`-shm` if present).

- Keep `/srv/data` as persistent volume.
- Backup both database and media using scripts:

```bash
# inside container or mounted host path
DATA_ROOT=/srv/data BACKUP_DIR=/srv/backups ./scripts/backup.sh
```

- Restore:

```bash
./scripts/restore.sh /srv/backups/postflow-backup-YYYYMMDDTHHMMSSZ.tar.gz /srv/data
```

Recommended cadence:

- Daily backups
- Keep at least 14 days (`RETENTION_DAYS=14`)

## 5) Operational actions

- List DLQ:

```bash
curl -H "Authorization: Bearer $API_TOKEN" "$BASE_URL/dlq?limit=50"
```

- Requeue one entry:

```bash
curl -H "Authorization: Bearer $API_TOKEN" -X POST "$BASE_URL/dlq/<dead_letter_id>/requeue"
```

- Delete one failed entry:

```bash
curl -H "Authorization: Bearer $API_TOKEN" -X POST "$BASE_URL/dlq/<dead_letter_id>/delete"
```

## 6) Troubleshooting

- `401 unauthorized`: check `API_TOKEN` or Basic Auth credentials.
- `429 rate limit exceeded`: raise `RATE_LIMIT_RPM` or reduce request burst.
- Post stuck in `failed`: inspect `/dlq` and requeue after fixing root cause.
