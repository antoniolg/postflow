# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eux; \
	goos="${TARGETOS:-linux}"; \
	goarch="${TARGETARCH:-amd64}"; \
	goarm=""; \
	if [ "$goarch" = "arm" ] && [ -n "${TARGETVARIANT:-}" ]; then goarm="${TARGETVARIANT#v}"; fi; \
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags='-s -w' -o /out/publisher ./cmd/publisher

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /srv

COPY --from=builder /out/publisher /usr/local/bin/publisher
RUN mkdir -p /srv/data && chown -R app:app /srv

USER app

ENV PORT=8080 \
    DATABASE_PATH=/srv/data/publisher.db \
    DATA_DIR=/srv/data/media \
    LOG_LEVEL=info

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/publisher"]
