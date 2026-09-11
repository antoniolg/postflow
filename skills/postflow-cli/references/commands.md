Read when: looking up the selected PostFlow command or its flags

Resolve command paths and bare `scripts/`, `assets/`, `resources/`, and `references/` paths from this skill directory, not from this reference directory. Preserve the operation's authorization and verification requirements.

## Core Commands

For inspection commands, use `--json` by default so states, ids, and timestamps are unambiguous. This applies especially to `schedule list`, `drafts list`, and any status lookup. Only fall back to human-readable output if the user explicitly wants it.

List schedule:

```bash
go run ./cmd/postflow --json schedule list \
  --from 2026-03-01T00:00:00Z \
  --to 2026-03-31T23:59:59Z

go run ./cmd/postflow --json schedule list \
  --view posts \
  --from 2026-03-01T00:00:00Z \
  --to 2026-03-31T23:59:59Z
```

Create a scheduled post:

```bash
go run ./cmd/postflow posts create \
  --account-id acc_xxx \
  --text "New launch post" \
  --scheduled-at 2026-03-10T09:00:00Z \
  --idempotency-key launch-2026-03-10
```

Create a scheduled post with media:

```bash
go run ./cmd/postflow media upload --file /path/to/card.jpg --kind image

go run ./cmd/postflow posts create \
  --account-id acc_xxx \
  --text "New launch post" \
  --media-id med_xxx \
  --scheduled-at 2026-03-10T09:00:00Z \
  --idempotency-key launch-2026-03-10
```

Create a multi-step post (root + follow-up/comment):

```bash
go run ./cmd/postflow posts create \
  --account-id acc_xxx \
  --segments-json '[{"text":"root post","media_ids":["med_x"]},{"text":"follow-up link https://example.com"}]' \
  --scheduled-at 2026-03-10T09:00:00Z \
  --idempotency-key launch-2026-03-10-thread
```

Validate payload without persisting:

```bash
go run ./cmd/postflow posts validate \
  --account-id acc_xxx \
  --text "Check this content" \
  --scheduled-at 2026-03-10T09:00:00Z
```

Validate a multi-step post:

```bash
go run ./cmd/postflow posts validate \
  --account-id acc_xxx \
  --segments-json '[{"text":"root post"},{"text":"follow-up link https://example.com"}]' \
  --scheduled-at 2026-03-10T09:00:00Z
```

Edit a scheduled post:

```bash
go run ./cmd/postflow posts edit \
  --id pst_xxx \
  --text "Updated post text" \
  --intent schedule \
  --scheduled-at 2026-03-10T09:30:00Z
```

Replace a scheduled post with multi-step segments:

```bash
go run ./cmd/postflow posts edit \
  --id pst_xxx \
  --segments-json '[{"text":"root updated"},{"text":"follow-up updated"}]' \
  --intent schedule \
  --scheduled-at 2026-03-10T09:30:00Z
```

Inspect and requeue dead letters:

```bash
go run ./cmd/postflow dlq list --limit 50
go run ./cmd/postflow dlq requeue --id dlq_xxx
```
