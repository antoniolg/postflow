# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG APP_VERSION=dev
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eux; \
	goos="${TARGETOS:-linux}"; \
	goarch="${TARGETARCH:-amd64}"; \
	goarm=""; \
	if [ "$goarch" = "arm" ] && [ -n "${TARGETVARIANT:-}" ]; then goarm="${TARGETVARIANT#v}"; fi; \
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags "-s -w -X github.com/antoniolg/postflow/cmd/postflow-server.Version=${APP_VERSION}" -o /out/postflow-server ./cmd/postflow-server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /srv

COPY --from=builder /out/postflow-server /usr/local/bin/postflow-server
RUN mkdir -p /srv/data && chown -R app:app /srv

USER app

ENV PORT=8080 \
    DATABASE_PATH=/srv/data/postflow.db \
    DATA_DIR=/srv/data/media \
    LOG_LEVEL=info \
    POSTFLOW_DRIVER=mock \
    POSTFLOW_MASTER_KEY= \
    PUBLIC_BASE_URL= \
    LINKEDIN_CLIENT_ID= \
    LINKEDIN_CLIENT_SECRET= \
    META_APP_ID= \
    META_APP_SECRET= \
    X_API_BASE_URL=https://api.twitter.com \
    X_UPLOAD_BASE_URL=https://upload.twitter.com \
    X_API_KEY= \
    X_API_SECRET= \
    X_ACCESS_TOKEN= \
    X_ACCESS_TOKEN_SECRET=

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/postflow-server"]
