# ADR 0001: Modular Monolith with Application Layer

Read when:
- You need to decide where to put new business logic.
- You are refactoring handlers or worker code.
- You are reviewing architecture tradeoffs.

Status: Accepted  
Date: 2026-02-28

## Context

The project started with fast iteration in handlers/worker and gradually accumulated business logic in adapter code.
This increased coupling between transport concerns (HTTP/MCP/forms) and core behavior (post lifecycle, media rules, retries).

The system now includes multiple entry points:

- HTTP UI/API
- MCP tools
- CLI
- background worker

All of them need consistent behavior and regression-safe refactors.

## Decision

Adopt a **modular monolith** with explicit `application` use cases and thin adapters.

Key rules:

1. Business orchestration lives in `internal/application/*`.
2. Shared contracts are defined in `internal/application/ports`.
3. Adapters (`api`, `worker`, `cli`) only parse input, call use cases, and map output/errors.
4. Infrastructure (`db`, provider clients, encryption) stays behind interfaces.

## Consequences

Positive:

- Reuse across HTTP/MCP/CLI/worker without duplicating logic.
- Faster, safer refactors with focused unit tests in `application`.
- Clearer ownership of errors (domain/application vs transport).

Tradeoffs:

- More interface types and files.
- Slightly more boilerplate when adding features.

## Rollout notes

Migrated use cases:

- post creation
- post mutations (edit/schedule/cancel/delete)
- media list/delete
- DLQ list/requeue/delete (single + bulk)
- publish cycle runner

The preferred path for new features is now:

`application -> adapter wiring -> regression tests -> CI gate`.
