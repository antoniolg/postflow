# AGENTS.md

This file defines the working contract for any coding agent in this repository.

Project: **PostFlow**  
Stack: **Go + SQLite**  
Primary goal: **LLM-first publisher with consistent behavior across API, MCP, CLI, and worker**

## 1) Architecture (must follow)

PostFlow uses a **modular monolith** with an application layer.

- `cmd/`: executable entrypoints (`publisher`, `publisher-cli`)
- `internal/api`: HTTP + MCP adapters (request parsing, transport mapping, HTML rendering)
- `internal/cli`: CLI adapter
- `internal/worker`: runtime adapter for background execution
- `internal/application`: business use cases/orchestration
- `internal/application/ports`: contracts for infra dependencies
- `internal/db`, `internal/publisher`, `internal/secure`: infrastructure implementations
- `internal/domain`: entities/status enums
- `internal/parity`: cross-surface parity tests/contracts

Authoritative docs:
- `docs/architecture.md`
- `docs/adr/0001-modular-monolith-application-layer.md`

### Layering rules

1. Adapters call `application` use cases.
2. `application` depends on `ports` + `domain`, never on transport adapters.
3. Error-to-HTTP/CLI mapping remains in adapters.
4. Avoid embedding business rules directly in handlers, CLI commands, or worker loops.

## 2) Test Strategy (black-box first)

Prefer tests that validate behavior and contracts, not implementation internals.

- Prioritize:
  - end-to-end adapter tests (`internal/api`, `internal/cli`, `internal/worker`)
  - integration tests with real DB where relevant
  - parity tests in `internal/parity`
- Avoid:
  - brittle call-count tests
  - tests coupled to private implementation details

When fixing bugs, add a regression test for the failure path when feasible.

## 3) Required Validation After Each Change

At minimum, run:

```bash
gofmt -w <changed-go-files>
go test ./...
```

For significant changes (new feature, refactor, infra, worker, auth, persistence), run full gate:

```bash
go test ./...
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

CI also enforces:
- `gofmt`
- `go mod tidy` consistency
- `golangci-lint`
- build, test, race
- coverage thresholds
- `govulncheck`

Coverage thresholds (from CI):
- total: `50%`
- `internal/worker`: `80%`
- `internal/api`: `60%`
- `internal/cli`: `40%`
- `internal/db`: `55%`
- `internal/publisher`: `50%`

## 4) API / MCP / CLI Parity Rules (critical)

PostFlow is LLM-first. Surface parity is a hard requirement.

When adding or changing a capability:
1. Implement behavior once (application layer).
2. Expose consistently through API/MCP/CLI where applicable.
3. Update parity tests in `internal/parity`.
4. Ensure error semantics are consistent across surfaces.

Do not ship features that exist only in one surface unless explicitly intentional and documented.

## 5) Database and Migrations (no data loss)

SQLite is used in production scenarios. Data safety is mandatory.

- Do not use destructive schema reset strategies.
- Use versioned migrations in `internal/db/db_migrations.go`.
- On schema changes:
  1. add a new migration version
  2. keep migrations idempotent/safe
  3. add/adjust migration tests (`internal/db/db_migrations_test.go`)

Current behavior:
- startup applies non-destructive migrations (`schema_migrations`)
- if pending migrations exist on an existing DB, a backup snapshot is created first

Never introduce migration logic that can silently drop user data.

## 6) File Size and Maintainability

Keep files manageable and focused.

Targets:
- Go source files: prefer `< 500 LOC`
- Tests: split by domain/behavior if they grow too much
- Large templates/scripts: extract reusable pieces when complexity increases

If a file exceeds limits, split by feature boundaries (parser/handler/service/helpers) before adding more complexity.

## 7) Documentation Requirements

If behavior/API/contracts change, update docs in the same PR/commit set:
- `README.md` (usage/config/surface behavior)
- `docs/specs/openapi.yaml` (API contract)
- architecture docs/ADR if design rules change

Documentation language in this repo is **English**.

## 8) Operational Guardrails

- Do not commit secrets, tokens, local DBs, or built binaries.
- Keep `.env` local only.
- Respect existing backup/restore scripts in `scripts/`.
- Prefer small, reviewable commits with Conventional Commit messages.

## 9) Change Checklist (copy/paste)

- [ ] Business logic placed in `internal/application` (or existing use case updated)
- [ ] Adapters are thin (API/MCP/CLI/worker)
- [ ] Regression tests added/updated (black-box where possible)
- [ ] Parity maintained across API/MCP/CLI (or documented intentional exception)
- [ ] Migrations added safely if schema changed
- [ ] Docs updated (`README`, OpenAPI, architecture notes as needed)
- [ ] `go test ./...` passes
- [ ] Full gate run for significant changes

