# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/publisher ./cmd/publisher

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
