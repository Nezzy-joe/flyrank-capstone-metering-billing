# Metering & Billing Engine — development workflow
#
#   make db-up               start PostgreSQL (Docker), wait for health (bounded)
#   make migrate-up          apply migrations (explicit step, not on API boot)
#   make migrate-down N=1    roll back N migrations (count required)
#   make migrate-down-all    roll back ALL migrations (destructive; explicit)
#   make run                 run the API locally (go run ./cmd/api)
#   make ready               GET /ready
#   make test                unit tests (no database)
#   make test-integration    unit + DB connectivity (needs db-up; fails if no DATABASE_URL)
#
# Config is read from .env (copy .env.example first). `-include` tolerates a
# missing file; commands then fail with a clear "DATABASE_URL is required".

-include .env
export

PORT ?= 8080
HEALTH_TIMEOUT ?= 60

.PHONY: db-up db-down migrate-up migrate-down migrate-down-all migrate-version run ready health test test-integration psql tidy build

db-up:
	docker compose up -d
	@echo "waiting up to $(HEALTH_TIMEOUT)s for postgres to become healthy..."; \
	timeout=$(HEALTH_TIMEOUT); \
	while [ $$timeout -gt 0 ]; do \
		status=$$(docker inspect -f '{{.State.Health.Status}}' mb_postgres 2>/dev/null); \
		if [ "$$status" = "healthy" ]; then echo "postgres is healthy"; exit 0; fi; \
		sleep 1; timeout=$$((timeout-1)); \
	done; \
	echo "ERROR: postgres did not become healthy within $(HEALTH_TIMEOUT)s. Recent logs:"; \
	docker compose logs --tail=40 postgres; \
	exit 1

db-down:
	docker compose down

migrate-up:
	go run ./cmd/migrate up

# Roll back N migrations. A count is required: make migrate-down N=1
migrate-down:
	@if [ -z "$(N)" ]; then \
		echo "usage: make migrate-down N=<count>   (e.g. N=1). For a full rollback use 'make migrate-down-all'."; \
		exit 1; \
	fi
	go run ./cmd/migrate down $(N)

# Roll back ALL migrations (destructive). Never the default; always explicit.
migrate-down-all:
	@echo "WARNING: this rolls back ALL migrations and drops every table."
	go run ./cmd/migrate down-all

migrate-version:
	go run ./cmd/migrate version

seed:
	go run ./cmd/seed

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

# Requires a running database (make db-up). Fails fast if DATABASE_URL is empty,
# so the integration test can never silently skip here.
test-integration:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "ERROR: DATABASE_URL is empty. Copy .env.example to .env and run 'make db-up' first."; \
		exit 1; \
	fi
	TEST_DATABASE_URL="$(DATABASE_URL)" go test ./... -count=1

psql:
	docker exec -it mb_postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

tidy:
	go mod tidy
