# PostFlow MVP Spec (LLM-first)

## Goal
A minimal service to schedule social posts, optimized for low-resource environments and LLM-driven API usage.

## v1 Scope
- Platform: `x`
- Media upload (video/image)
- Post scheduling (text + media + date)
- Worker that executes pending posts
- Informational calendar view (read-only)
- Future post cancellation
- Retries with backoff + DLQ
- Idempotency key on post creation
- Simple token / Basic Auth
- Basic per-client rate limiting

## v1 Non-goals
- Full web editor
- Multi-account per network
- Advanced analytics
- Multi-level retries / distributed queues

## Non-functional requirements
- Single runtime (single process)
- Local persistence (SQLite)
- Low memory, stable CPU
- Explicit, stable API for LLM consumption

## Data model
- `media`: uploaded file + metadata
- `posts`: publish intent + status
- `post_media`: N:M relation

Post states:
- `scheduled`
- `publishing`
- `published`
- `failed`
- `canceled`

## MVP Endpoints
- `POST /media`
- `POST /posts`
- `POST /posts/validate`
- `GET /schedule`
- `POST /posts/{id}/cancel`
- `GET /dlq`
- `POST /dlq/{id}/requeue`
- `POST /dlq/{id}/delete`
- `GET /healthz`
- `GET /` (read-only UI)

## Main flow
1. Client uploads media
2. Client creates post with date/time and `media_ids`
3. Worker detects due posts, publishes them, updates status
4. UI and API show the updated calendar

## Immediate roadmap (v1.1)
- Chunked media upload for long videos
- Token auth for API
- Publish via `POST /2/tweets` with compatible v1.1 media upload
