# Coolify Deploy Runbook

## 1) Create the service

1. In Coolify, create a new service from Git repository `antoniolg/publisher`.
2. Use branch `main` and enable auto-deploy on new commits.
3. Build using project `Dockerfile`.
4. Set application internal port to `8080` in Coolify (do not publish host port `8080` via Docker `ports`).
5. Attach a persistent volume mounted at `/srv/data`.

## 2) Required environment variables

Set these in Coolify:

- `PORT=8080`
- `DATABASE_PATH=/srv/data/publisher.db`
- `DATA_DIR=/srv/data/media`
- `API_TOKEN=<long-random-token>`
- `UI_BASIC_USER=<your-user>`
- `UI_BASIC_PASS=<long-password>`
- `PUBLISHER_MASTER_KEY=<base64-32-bytes>`
- `PUBLIC_BASE_URL=https://<your-coolify-domain>`

Optional recommended:

- `LOG_LEVEL=info`
- `RATE_LIMIT_RPM=120`
- `WORKER_INTERVAL_SECONDS=30`
- `RETRY_BACKOFF_SECONDS=30`
- `DEFAULT_MAX_RETRIES=3`

For real X publishing:

- `PUBLISHER_DRIVER=live` (`x` también funciona como alias legacy)
- `X_API_KEY=...`
- `X_API_SECRET=...`
- `X_ACCESS_TOKEN=...`
- `X_ACCESS_TOKEN_SECRET=...`

For LinkedIn OAuth:
- `LINKEDIN_CLIENT_ID=...`
- `LINKEDIN_CLIENT_SECRET=...`

For Facebook/Instagram OAuth:
- `META_APP_ID=...`
- `META_APP_SECRET=...`

Notes:

- For local development, use `.env` (template available at `.env.example`).
- In Coolify, configure secrets in the service environment UI, not in a committed `.env`.

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

- Keep `/srv/data` as persistent volume.
- Backup both database and media using scripts:

```bash
# inside container or mounted host path
DATA_ROOT=/srv/data BACKUP_DIR=/srv/backups ./scripts/backup.sh
```

- Restore:

```bash
./scripts/restore.sh /srv/backups/publisher-backup-YYYYMMDDTHHMMSSZ.tar.gz /srv/data
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

## 6) Troubleshooting

- `401 unauthorized`: check `API_TOKEN` or Basic Auth credentials.
- `429 rate limit exceeded`: raise `RATE_LIMIT_RPM` or reduce request burst.
- Post stuck in `failed`: inspect `/dlq` and requeue after fixing root cause.
