---
name: postflow-cli
description: "Manage PostFlow posts, schedules, validation, and failed-publication retries through its CLI."
---

# PostFlow CLI

Use this skill to operate PostFlow from terminal via `postflow` (HTTP API, no MCP required).

For Antonio's workflows, this is the canonical/default path for social publishing. Prefer the CLI over direct API calls unless you are debugging a CLI failure.

## Requirements

- Assume the CLI is already configured and ready to use.
- Do not go hunting for env vars or API config up front.
- If the CLI fails because base URL / token / env is missing, then ask the user to configure it or point out what is missing.

## Select the operation

Read [commands](references/commands.md) for the specific operation. Use the installed CLI; `go run ./cmd/postflow` examples assume the PostFlow repository root. Secret-dependent commands run through `oprun`.

Inspect live targets before any mutation and read them back afterward. A lookup or draft request does not authorize scheduling or publishing. Reuse explicit approval only for the same reviewed destination and payload. If authentication fails, report the redacted error and block that account's dependent work; [OAuth recovery](references/reauthorize.md) is a separate explicitly authorized operation.

## Guidance

- For operational inspection (`drafts list`, `schedule list`, status checks), use `--json` first, even for manual investigations. It avoids ambiguity around state, ids, timestamps, and per-platform entries.
- `schedule list` returns grouped publications by default. Use `--view posts` when you need raw per-post thread metadata (`thread_group_id`, `thread_position`, `parent_post_id`, `root_post_id`).
- Prefer `--json` when output is consumed by scripts or further tooling.
- PostFlow preserves classic Markdown emphasis in post text. When the copy needs emphasis, keep `**bold**` and `*italic*` in the payload instead of stripping them.
- To mention someone on LinkedIn, write `@[Name](urn:li:person:ID)` or `@[Name](urn:li:organization:ID)` in the text. `Name` must match the member's first, last or full name, or the page's full name, case sensitive, or LinkedIn shows it as plain text.
- For people, the `ID` is this PostFlow app's API member id (short, like `Dk968RHxiO`), never the `ACoAA...` id from the LinkedIn website, which LinkedIn rejects. If you don't have it, ask for someone to mention the person by hand in a post published as a connected LinkedIn page, then run `postflow --json linkedin mentions --account-id <page account>` and take the `urn` from the output. Organization ids are the numeric page ids.
- Use `--idempotency-key` for retries/replays of `posts create`.
- Keep timestamps in RFC3339 for CLI/API consistency.
- Use `--segments-json` when the user asks for "first comment", "next comment", "thread", or multiple steps in one publication.
- Segment semantics:
  - X: follow-ups are chained as replies.
  - LinkedIn/Facebook: follow-ups are published as comments on the root post.
- `--text` and `--segments-json` are mutually exclusive on create/validate/edit.
- When a post needs a first comment plus media on the root post, put media IDs on the first segment only.
