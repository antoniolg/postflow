# Publisher MVP Spec (LLM-first)

## Objetivo
Servicio mínimo para programar publicaciones en redes, optimizado para recursos bajos y uso por LLMs vía API.

## Alcance v1
- Plataforma: `x`
- Subida de media (vídeo/imagen)
- Programación de publicación (texto + media + fecha)
- Worker que ejecuta publicaciones pendientes
- Vista de calendario informativa (solo lectura)
- Cancelación de publicaciones futuras
- Reintentos con backoff + DLQ
- Idempotency key en creación de posts

## No objetivo v1
- Editor web completo
- Multicuenta por red
- Analítica avanzada
- Reintentos multinivel / colas distribuidas

## Requisitos no funcionales
- Runtime único (proceso único)
- Persistencia local (SQLite)
- Memoria baja, CPU estable
- API explícita y estable para consumo por LLM

## Modelo de datos
- `media`: archivo subido y metadatos
- `posts`: intención de publicación y estado
- `post_media`: relación N:M

Estados de post:
- `scheduled`
- `publishing`
- `published`
- `failed`
- `canceled`

## Endpoints MVP
- `POST /media`
- `POST /posts`
- `GET /schedule`
- `POST /posts/{id}/cancel`
- `GET /dlq`
- `POST /dlq/{id}/requeue`
- `GET /healthz`
- `GET /` (UI read-only)

## Flujo principal
1. Cliente sube media
2. Cliente crea post con fecha/hora y `media_ids`
3. Worker detecta due posts, los publica, marca estado
4. UI y API muestran el calendario actualizado

## Roadmap inmediato (v1.1)
- Upload chunked de media para vídeos largos
- Auth por token para API
- Migración de `statuses/update` a endpoint de creación en X API v2 cuando el entorno esté listo
