# Architecture

Read when:
- You need to add a new feature or endpoint.
- You need to move code between `api`, `worker`, `cli` and `application`.
- You need to understand where business logic belongs.

## Current style

`postflow` uses a **modular monolith** with clear boundaries:

- `cmd/`: entrypoints (`server`, `worker`, `cli`).
- `internal/api`: HTTP/MCP adapters (parsing, HTTP status, redirects, render).
- `internal/worker`: runtime adapter for background execution.
- `internal/cli`: CLI adapter.
- `internal/application`: use cases and orchestration.
- `internal/db`, `internal/postflow`, `internal/secure`: infrastructure.
- `internal/domain`: entities and status enums.

## Layer rules

1. Adapters (`api`, `worker`, `cli`) call `application`.
2. `application` depends on interfaces (`ports`) and `domain`, not on adapters.
3. Infrastructure packages (`db`, provider SDK clients) are wired at the edges.
4. Error mapping to HTTP/CLI messages stays in adapters.

## Use cases already extracted

- `internal/application/posts/create.go`
- `internal/application/posts/mutations.go`
- `internal/application/media/service.go`
- `internal/application/dlq/service.go`
- `internal/application/publishcycle/runner.go`
- shared ports: `internal/application/ports/ports.go`

## Practical checklist for new work

1. Add/update use case in `internal/application/*`.
2. Add unit tests for the use case first.
3. Keep adapter handlers thin (request parsing + response mapping only).
4. Run full gate: `gofmt`, `go test ./...`, `go test -race ./...`, `golangci-lint`.
