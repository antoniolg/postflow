---
name: postflow-cli
description: Use the postflow CLI to manage scheduled posts, validate/create posts, and operate DLQ entries against the PostFlow HTTP API. Use when the user asks to inspect schedule, create posts, or requeue failed publications quickly from terminal.
---

# PostFlow CLI

Use this skill to operate PostFlow from terminal via `postflow` (HTTP API, no MCP required).

For Antonio's workflows, this is the canonical/default path for social publishing. Prefer the CLI over direct API calls unless you are debugging a CLI failure.

## Requirements

- Assume the CLI is already configured and ready to use.
- Do not go hunting for env vars or API config up front.
- If the CLI fails because base URL / token / env is missing, then ask the user to configure it or point out what is missing.

## Quick Start

```bash
go run ./cmd/postflow --help
```

Global defaults:
- Base URL: `POSTFLOW_BASE_URL` or `http://localhost:8080`
- Token: `POSTFLOW_API_TOKEN`

## Core Commands

For inspection commands, use `--json` by default so states, ids, and timestamps are unambiguous. This applies especially to `schedule list`, `drafts list`, and any status lookup. Only fall back to human-readable output if the user explicitly wants it.

List schedule:

```bash
go run ./cmd/postflow --json schedule list \
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

## Guidance

- For operational inspection (`drafts list`, `schedule list`, status checks), use `--json` first, even for manual investigations. It avoids ambiguity around state, ids, timestamps, and per-platform entries.
- Prefer `--json` when output is consumed by scripts or further tooling.
- Use `--idempotency-key` for retries/replays of `posts create`.
- Keep timestamps in RFC3339 for CLI/API consistency.
- Use `--segments-json` when the user asks for "first comment", "next comment", "thread", or multiple steps in one publication.
- Segment semantics:
  - X: follow-ups are chained as replies.
  - LinkedIn/Facebook: follow-ups are published as comments on the root post.
- `--text` and `--segments-json` are mutually exclusive on create/validate/edit.
- When a post needs a first comment plus media on the root post, put media IDs on the first segment only.
