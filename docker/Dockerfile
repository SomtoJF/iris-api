# Build from repo root: docker build -f docker/Dockerfile .
# Requires runtime env (e.g. RENDER=true): REDIS_HOST, Temporal reachable from container,
# AWS_*, CLIENT_DOMAIN, JWT secrets, etc.
#
# SQLite lives under $HOME/iris/db/gorm.db — set HOME=/app (default) and mount a volume on /app/iris/db for persistence.

FROM golang:1.25.3-bookworm AS builder

WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev \
	&& rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=1
RUN go build -o /out/iris-api main.go

FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 \
	&& rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --home-dir /app --shell /usr/sbin/nologin --uid 1000 appuser

WORKDIR /app

COPY --from=builder /out/iris-api /app/iris-api

RUN mkdir -p /app/iris/db && chown -R appuser:appuser /app

USER appuser

ENV HOME=/app
ENV PORT=4000
ENV RENDER=true

EXPOSE 4000

CMD ["/app/iris-api"]
