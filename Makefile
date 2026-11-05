# Metering & Billing Engine — development workflow
#
#   make db-up        start PostgreSQL (Docker) and wait for health
#   make migrate-up   apply database migrations (explicit step, not on API boot)
#   make run          run the API locally (go run ./cmd/api)
#   make ready        curl the readiness endpoint
#   make test         unit tests (no database needed)
#   make test-integration   unit + DB connectivity (needs db-up)
#
# Config is read from .env (copy .env.example first). `-include` tolerates a
# missing file; the commands then fail with a clear "DATABASE_URL is required".

-include .env
export

PORT ?= 8080

.PHONY: db-up db-down migrate-up migrate-down migrate-version run ready health test test-integration psql tidy build

db-up:
	docker compose up -d
	@echo "waiting for postgres health..."
	@until [ "$$(docker inspect -f '{{.State.Health.Status}}' mb_postgres 2>/dev/null)" = "healthy" ]; do sleep 1; done
	@echo "postgres is healthy"

db-down:
	docker compose down

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-version:
	go run ./cmd/migrate version

run:
	go run ./cmd/api

health:
	curl -fsS http://localhost:$(PORT)/health && echo

ready:
	curl -i -sS http://localhost:$(PORT)/ready && echo

build:
	go build ./...

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL="$(DATABASE_URL)" go test ./... -count=1

psql:
	docker exec -it mb_postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

tidy:
	go mod tidy
