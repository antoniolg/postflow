# Codex Subagents

Read when:
- You want to use the project-scoped Codex subagents in this repository.
- You need to choose between a PostFlow custom agent and a Codex built-in agent.
- You want a repeatable multi-agent workflow for features, parity work, or release prep.

## Project agents

### `postflow_mapper`

Use this agent to map impact before edits.
It should identify the affected layers, likely files, required validation, and whether parity docs must move with the change.

### `parity_guard`

Use this agent for API, MCP, and CLI parity work, contract sync, parity tests, and doc updates tied to surface changes.
This agent owns parity and docs sync, not core business logic in `internal/application`.

### `postflow_worker`

Use this agent for a small, explicitly assigned implementation slice.
It should preserve modular-monolith boundaries, keep adapters thin, and keep scope narrow.

### `owner_reviewer`

Use this agent for findings-first review of a branch or patch.
It should focus on correctness, regressions, concurrency, auth, data safety, and missing tests.

### `release_sheriff`

Use this agent for release-readiness checks, quality-gate expectations, release workflows, and asset verification.
It should follow `docs/RELEASING.md` and stay operational rather than product-focused.

## When to use these vs built-ins

Use the PostFlow custom agents when the task depends on this repo's rules around `internal/application`, cross-surface parity, non-destructive SQLite migrations, or release flow.
Use built-in `explorer` or `worker` for generic repo work that does not benefit from PostFlow-specific guardrails.
Use built-in `default` as the coordinator when you want Codex to orchestrate several agents together.

## Canonical recipes

### Feature change

Use `postflow_mapper` first, then `postflow_worker`, then `parity_guard`, then `owner_reviewer`.

Example prompt:

```text
Map the impact of adding a new field to the schedule list output. Use postflow_mapper first.
Then have postflow_worker implement only the assigned application and adapter slice.
After that, use parity_guard to update parity tests and any README or OpenAPI changes that must move with the contract.
Finish with owner_reviewer reviewing this branch against main and reporting findings first.
```

### Contract or surface change

Use `postflow_mapper` first, then `parity_guard`, then `owner_reviewer`.

Example prompt:

```text
We are changing the schedule.list contract. Use postflow_mapper to identify the affected surfaces and docs.
Then have parity_guard own the parity tests, error-semantics checks, and any README or OpenAPI updates.
Finish with owner_reviewer and report concrete risks or missing coverage.
```

### Release prep

Use `release_sheriff` first, then `owner_reviewer`.

Example prompt:

```text
Use release_sheriff to check whether this branch is ready to release from main according to docs/RELEASING.md.
Then have owner_reviewer review the release candidate for correctness, regression risk, and missing validation.
```

## Warnings

`parity_guard` owns parity and docs sync, not core business logic in `internal/application`.
This v1 intentionally does not include browser-dependent or extra MCP-dependent specialists.
