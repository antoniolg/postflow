# publisher

Publicador minimalista orientado a LLMs: API first, worker ligero y UI informativa.

Estado actual (MVP v0):
- Plataforma soportada: `x`
- Flujo soportado: subir media, programar posts, listar calendario, cancelar
- Ejecución: worker interno con cliente mock (publica y marca como `published`)

## Quickstart

```bash
go run ./cmd/publisher
```

Servidor en `http://localhost:8080`.

Variables opcionales:
- `PORT` (default: `8080`)
- `DATABASE_PATH` (default: `publisher.db`)
- `DATA_DIR` (default: `data`)
- `WORKER_INTERVAL_SECONDS` (default: `30`)

## API rápida

### 1) Subir media

```bash
curl -X POST http://localhost:8080/media \
  -F platform=x \
  -F kind=video \
  -F file=@./clip.mp4
```

### 2) Programar post

```bash
curl -X POST http://localhost:8080/posts \
  -H 'content-type: application/json' \
  -d '{
    "platform": "x",
    "text": "Nuevo short",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"]
  }'
```

### 3) Ver calendario JSON

```bash
curl 'http://localhost:8080/schedule'
```

### 4) Cancelar

```bash
curl -X POST http://localhost:8080/posts/pst_xxx/cancel
```

## UI mínima

Abre `http://localhost:8080/` para ver una tabla de publicaciones (solo lectura).

## Specs

- MVP: `docs/specs/mvp.md`
- OpenAPI: `docs/specs/openapi.yaml`
