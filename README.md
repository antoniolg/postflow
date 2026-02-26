# publisher

Publicador minimalista orientado a LLMs: API first, worker ligero y UI informativa.

Estado actual (MVP v0):
- Plataforma soportada: `x`
- Flujo soportado: subir media, programar posts, listar calendario, cancelar
- Borradores sin fecha (ideas) y programación posterior
- Ejecución: worker interno con `mock` o publicación real en X (`PUBLISHER_DRIVER=x`)

## Quickstart

```bash
# opcional: copia el example y rellena secretos locales
cp .env.example .env

go run ./cmd/publisher
```

Servidor en `http://localhost:8080`.

La app carga automáticamente variables desde `.env` si existe.
- Las variables exportadas en shell tienen prioridad sobre `.env`.
- Puedes usar otro fichero con `ENV_FILE=/ruta/a/otro.env`.

## Desarrollo con hot reload

Con `air` el servidor recompila/reinicia al guardar cambios:

```bash
go install github.com/air-verse/air@latest
air
```

## CLI (`publisher-cli`)

CLI para operar la API HTTP directamente (sin MCP).

```bash
go run ./cmd/publisher-cli --help
```

Global flags:
- `--base-url` (o `PUBLISHER_BASE_URL`)
- `--api-token` (o `PUBLISHER_API_TOKEN`)
- `--timeout`
- `--json`

Ejemplos:

```bash
go run ./cmd/publisher-cli schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
go run ./cmd/publisher-cli posts create --text "hola" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/publisher-cli posts validate --text "hola" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/publisher-cli dlq list --limit 50
go run ./cmd/publisher-cli dlq requeue --id dlq_xxx
```

Skill local incluida:
- `skills/publisher-cli/SKILL.md`

Variables opcionales:
- `PORT` (default: `8080`)
- `DATABASE_PATH` (default: `publisher.db`)
- `DATA_DIR` (default: `data`)
- `WORKER_INTERVAL_SECONDS` (default: `30`)
- `RETRY_BACKOFF_SECONDS` (default: `30`)
- `DEFAULT_MAX_RETRIES` (default: `3`)
- `RATE_LIMIT_RPM` (default: `120`, `0` para desactivar)
- `API_TOKEN` (Bearer o `X-API-Key`)
- `UI_BASIC_USER`
- `UI_BASIC_PASS`
- `LOG_LEVEL` (`debug|info|warn|error`, default: `info`)
- `PUBLISHER_DRIVER` (`mock` por defecto, `x` para publicar real)
- `X_API_BASE_URL` (default: `https://api.twitter.com`)
- `X_UPLOAD_BASE_URL` (default: `https://upload.twitter.com`)
- `X_API_KEY`
- `X_API_SECRET`
- `X_ACCESS_TOKEN`
- `X_ACCESS_TOKEN_SECRET`

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
  -H 'Idempotency-Key: short-2026-02-25-001' \
  -d '{
    "platform": "x",
    "text": "Nuevo short",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
  }'
```

### 2.1) Crear borrador (sin fecha)

```bash
curl -X POST http://localhost:8080/posts \
  -H 'content-type: application/json' \
  -d '{
    "platform": "x",
    "text": "Idea para pulir más tarde"
  }'
```

### 2.1) Validar post (dry-run, no guarda)

```bash
curl -X POST http://localhost:8080/posts/validate \
  -H 'content-type: application/json' \
  -d '{
    "platform": "x",
    "text": "Nuevo short",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
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

### 5) Ver DLQ

```bash
curl 'http://localhost:8080/dlq?limit=50'
```

### 6) Requeue de una entrada DLQ

```bash
curl -X POST http://localhost:8080/dlq/dlq_xxx/requeue
```

## UI mínima

Abre `http://localhost:8080/` para ver una tabla de publicaciones (solo lectura).

Incluye vista `settings` para definir la zona horaria de la UI (`IANA`, por ejemplo `Europe/Madrid`).
- La persistencia sigue en UTC.
- La visualización y los campos `datetime-local` se muestran/interpretan en la zona configurada.
- En la misma vista `settings` se muestra también la URL/config del MCP HTTP.

## Revisión de accesibilidad

Chequeo automático con axe en las vistas principales:

```bash
scripts/a11y-check.sh
```

Notas:
- Usa `@axe-core/cli` vía `npx` (sin instalación global).
- Si hay `UI_BASIC_USER` y `UI_BASIC_PASS` en `.env`, el script los usa automáticamente.
- Guarda JSON por vista en `/tmp/publisher-a11y` y devuelve código `1` si encuentra violaciones.

### 7) Guardar timezone de UI

```bash
curl -X POST http://localhost:8080/settings/timezone \
  -H 'content-type: application/json' \
  -d '{"timezone":"Europe/Madrid"}'
```

## MCP (streamable HTTP)

Endpoint MCP:

```bash
http://localhost:8080/mcp
```

Soporta transporte streamable HTTP y expone estas tools:
- `publisher_list_schedule`
- `publisher_list_drafts`
- `publisher_list_failed`
- `publisher_create_post`

Ejemplo para Claude Code:

```bash
claude mcp add -t http publisher http://localhost:8080/mcp --header "Authorization: Bearer <API_TOKEN>"
```

Ejemplo para Codex:

```bash
codex mcp add publisher --url http://localhost:8080/mcp
```

`~/.codex/config.toml`:

```toml
[mcp_servers.publisher]
url = "http://localhost:8080/mcp"
bearer_token_env_var = "PUBLISHER_API_TOKEN"
```

Ejemplo de config JSON:

```json
{
  "mcpServers": {
    "publisher": {
      "transport": "streamable_http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer <API_TOKEN>"
      }
    }
  }
}
```

## Publicación real en X

```bash
export PUBLISHER_DRIVER=x
export X_API_KEY=...
export X_API_SECRET=...
export X_ACCESS_TOKEN=...
export X_ACCESS_TOKEN_SECRET=...
go run ./cmd/publisher
```

Notas:
- Usa flujo chunked para media (`INIT`/`APPEND`/`FINALIZE`/`STATUS`) y luego `POST /2/tweets`.
- Si falta cualquier credencial, el proceso falla al arrancar (fail fast).

## Fiabilidad (v0.2)

- Reintentos con backoff exponencial (`RETRY_BACKOFF_SECONDS`).
- DLQ local en SQLite (`dead_letters`) cuando se supera `max_attempts`.
- API de rescate: `GET /dlq` y `POST /dlq/{id}/requeue`.
- Idempotencia en `POST /posts` usando header `Idempotency-Key`.

## Logs y trazabilidad

- Logs estructurados en JSON por stdout (listo para Coolify).
- Campos de request: `request_id`, `method`, `path`, `status`, `duration_ms`, `client`.
- Header `X-Request-Id` en todas las respuestas.

## Cerrar interfaz pública (simple)

Sin login completo, pero cerrada y funcional:

```bash
export API_TOKEN='pon-aqui-un-token-largo'
export UI_BASIC_USER='antonio'
export UI_BASIC_PASS='otra-clave-larga'
go run ./cmd/publisher
```

- UI en navegador: pedirá Basic Auth.
- API para LLMs/scripts: `Authorization: Bearer $API_TOKEN` o `X-API-Key: $API_TOKEN`.

## Specs

- MVP: `docs/specs/mvp.md`
- OpenAPI: `docs/specs/openapi.yaml`
- Coolify deploy runbook: `docs/coolify-deploy.md`

## Backup scripts

- Backup: `scripts/backup.sh`
- Restore: `scripts/restore.sh`
