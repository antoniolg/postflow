# publisher

Publicador API-first orientado a LLMs con worker ligero, soporte multi-red y multi-cuenta por `account_id`.

Estado actual (v2):
- Redes soportadas: `x`, `linkedin`, `facebook`, `instagram`
- Modelo: `1 post = 1 account_id`
- Auth por cuenta: `static` u `oauth`
- Secretos de app en env + credenciales de cuenta cifradas en SQLite (AES-256-GCM)
- UI web con scheduler/composer y bloque visual de cuentas dentro de `settings`

## Quickstart

```bash
cp .env.example .env
# clave obligatoria (32 bytes en base64)
openssl rand -base64 32
# pega el valor en PUBLISHER_MASTER_KEY

go run ./cmd/publisher
```

Servidor en `http://localhost:8080`.

La app carga variables desde `.env` si existe:
- Variables exportadas en shell tienen prioridad.
- Puedes usar otro fichero con `ENV_FILE=/ruta/a/otro.env`.

## CLI (`publisher-cli`)

```bash
go run ./cmd/publisher-cli --help
```

Ejemplos:

```bash
go run ./cmd/publisher-cli schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
go run ./cmd/publisher-cli posts create --account-id acc_xxx --text "hola" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/publisher-cli posts validate --account-id acc_xxx --text "hola" --scheduled-at 2026-03-10T09:00:00Z
go run ./cmd/publisher-cli dlq list --limit 50
go run ./cmd/publisher-cli dlq requeue --id dlq_xxx
```

## Configuración

Variables principales:
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
- `PUBLISHER_DRIVER` (`mock` por defecto, `live` para publicación real; `x` se acepta como alias legacy)

Seguridad/OAuth (v2):
- `PUBLISHER_MASTER_KEY` (obligatoria, base64 32 bytes)
- `PUBLIC_BASE_URL` (base URL pública para callbacks OAuth)
- `LINKEDIN_CLIENT_ID`
- `LINKEDIN_CLIENT_SECRET`
- `META_APP_ID`
- `META_APP_SECRET`

X (cuenta estática bootstrap `x-default`):
- `X_API_BASE_URL` (default: `https://api.twitter.com`)
- `X_UPLOAD_BASE_URL` (default: `https://upload.twitter.com`)
- `X_API_KEY`
- `X_API_SECRET`
- `X_ACCESS_TOKEN`
- `X_ACCESS_TOKEN_SECRET`

## API rápida (v2)

### 1) Listar cuentas

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/accounts
```

### 2) Crear cuenta estática

```bash
curl -X POST http://localhost:8080/accounts/static \
  -H 'content-type: application/json' \
  -d '{
    "platform": "x",
    "display_name": "X Default",
    "external_account_id": "x-default",
    "credentials": {
      "access_token": "...",
      "access_token_secret": "...",
      "token_type": "oauth1"
    }
  }'
```

### 3) Iniciar OAuth

```bash
curl -X POST http://localhost:8080/oauth/linkedin/start
curl -X POST http://localhost:8080/oauth/facebook/start
curl -X POST http://localhost:8080/oauth/instagram/start
```

### 4) Subir media

```bash
curl -X POST http://localhost:8080/media \
  -F kind=video \
  -F file=@./clip.mp4
```

### 5) Crear post (draft o scheduled)

Obtén antes un `account_id` válido con `GET /accounts`.

```bash
curl -X POST http://localhost:8080/posts \
  -H 'content-type: application/json' \
  -H 'Idempotency-Key: short-2026-02-25-001' \
  -d '{
    "account_id": "acc_xxx",
    "text": "Nuevo short",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
  }'
```

### 6) Validar post (dry-run)

```bash
curl -X POST http://localhost:8080/posts/validate \
  -H 'content-type: application/json' \
  -d '{
    "account_id": "acc_xxx",
    "text": "Nuevo short",
    "scheduled_at": "2026-02-26T10:00:00Z",
    "media_ids": ["med_xxx"],
    "max_attempts": 3
  }'
```

### 7) Operaciones auxiliares

```bash
curl 'http://localhost:8080/schedule'
curl -X POST http://localhost:8080/posts/pst_xxx/cancel
curl 'http://localhost:8080/dlq?limit=50'
curl -X POST http://localhost:8080/dlq/dlq_xxx/requeue
curl -X POST http://localhost:8080/dlq/dlq_xxx/delete
curl -X POST http://localhost:8080/dlq/delete -H 'content-type: application/json' -d '{"ids":["dlq_xxx","dlq_yyy"]}'
curl -X POST http://localhost:8080/accounts/acc_xxx/disconnect
curl -X DELETE http://localhost:8080/accounts/acc_xxx
```

## UI

- `GET /`: scheduler/composer (selector obligatorio de cuenta)
- `GET /accounts`: API JSON para listar cuentas (`text/html` redirige a `/?view=settings`)
- `GET /?view=settings`: zona horaria UI + bloque de cuentas conectadas + configuración MCP

## MCP (streamable HTTP)

Endpoint:

```bash
http://localhost:8080/mcp
```

Tools expuestas:
- `publisher_list_schedule`
- `publisher_list_drafts`
- `publisher_list_failed`
- `publisher_create_post` (requiere `account_id`)
- `publisher_upload_media` (sin `platform`)
- `publisher_list_media`
- `publisher_delete_media`

Ejemplo Claude Code:

```bash
claude mcp add -t http publisher http://localhost:8080/mcp --header "Authorization: Bearer <API_TOKEN>"
```

Ejemplo Codex:

```bash
codex mcp add publisher --url http://localhost:8080/mcp
```
