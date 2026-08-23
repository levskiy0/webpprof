#!/bin/sh

set -eu

root_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
modules=$(cd "$root_dir" && GOWORK=off go list -m all)

for optional in \
	github.com/gin-gonic/gin \
	github.com/go-chi/chi \
	github.com/gofiber/fiber \
	github.com/hibiken/asynq \
	github.com/jackc/pgx \
	github.com/labstack/echo \
	github.com/levskiy0/go-cache \
	github.com/levskiy0/go-queue \
	github.com/modelcontextprotocol/go-sdk \
	github.com/nats-io/nats.go \
	github.com/redis/go-redis \
	github.com/rs/zerolog \
	github.com/segmentio/kafka-go \
	github.com/uptrace/bun \
	github.com/wneessen/go-mail \
	go.opentelemetry.io/otel \
	go.uber.org/zap \
	google.golang.org/grpc \
	gorm.io/gorm \
	modernc.org/sqlite
do
	if printf '%s\n' "$modules" | grep -F "$optional " >/dev/null; then
		echo "optional module leaked into core graph: $optional" >&2
		exit 1
	fi
done

echo "core dependency graph contains no optional profiler SDKs"
