---
name: postflow-cli
description: Use the postflow CLI to manage scheduled posts, validate/create posts, and operate DLQ entries against the Publisher HTTP API. Use when the user asks to inspect schedule, create posts, or requeue failed publications quickly from terminal.
---

# Publisher CLI

Use this skill to operate Publisher from terminal via `postflow` (HTTP API, no MCP required).

## Requirements

- Server reachable (default `http://localhost:8080`)
- Optional auth token in `PUBLISHER_API_TOKEN`

## Quick Start

```bash
go run ./cmd/postflow --help
```

Global defaults:
- Base URL: `PUBLISHER_BASE_URL` or `http://localhost:8080`
- Token: `PUBLISHER_API_TOKEN`

## Core Commands

List schedule:

```bash
go run ./cmd/postflow --json schedule list \
  --from 2026-03-01T00:00:00Z \
  --to 2026-03-31T23:59:59Z
```

Create a scheduled post:

```bash
go run ./cmd/postflow posts create \
  --text "New launch post" \
  --scheduled-at 2026-03-10T09:00:00Z \
  --idempotency-key launch-2026-03-10
```

Validate payload without persisting:

```bash
go run ./cmd/postflow posts validate \
  --text "Check this content" \
  --scheduled-at 2026-03-10T09:00:00Z
```

Inspect and requeue dead letters:

```bash
go run ./cmd/postflow dlq list --limit 50
go run ./cmd/postflow dlq requeue --id dlq_xxx
```

## Guidance

- Prefer `--json` when output is consumed by scripts or further tooling.
- Use `--idempotency-key` for retries/replays of `posts create`.
- Keep timestamps in RFC3339 for CLI/API consistency.
