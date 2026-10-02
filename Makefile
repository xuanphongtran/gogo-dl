# ── Variables ─────────────────────────────────────────────────────────────────
APP_NAME    := gogo-dl
CMD_PATH    := ./cmd/server
BUILD_DIR   := ./bin
BINARY      := $(BUILD_DIR)/$(APP_NAME)
ENV_FILE    := configs/.env

MIGRATE_URL ?= $(shell grep DB_ $(ENV_FILE) 2>/dev/null | \
               awk -F= 'BEGIN{h="localhost";P="5432";u="postgres";p="";d="gogo_dl";s="disable"} \
               /DB_HOST/{h=$$2} /DB_PORT/{P=$$2} /DB_USER/{u=$$2} \
               /DB_PASSWORD/{p=$$2} /DB_NAME/{d=$$2} /DB_SSLMODE/{s=$$2} \
               END{printf "postgres://%s:%s@%s:%s/%s?sslmode=%s",u,p,h,P,d,s}')

MIGRATIONS_DIR := migrations

# ── Phony targets ──────────────────────────────────────────────────────────────
.PHONY: all run build clean test lint \
        migrate-up migrate-down migrate-create \
        deps tidy docker-up docker-down

# Default target
all: build

## run: Run the server with hot-reload via Air (install: go install github.com/air-verse/air@latest)
run:
	@echo "→ Starting $(APP_NAME)..."
	@if command -v air > /dev/null; then \
		air; \
	else \
		go run $(CMD_PATH)/main.go; \
	fi

## build: Compile a production binary to ./bin/
build:
	@echo "→ Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY) $(CMD_PATH)/main.go
	@echo "✓ Binary at $(BINARY)"

## clean: Remove build artefacts
clean:
	@rm -rf $(BUILD_DIR)
	@echo "✓ Cleaned"

## deps: Download and verify all Go modules
deps:
	go mod download
	go mod verify

## tidy: Clean up go.mod / go.sum
tidy:
	go mod tidy

## test: Run all tests with race detector
test:
	go test -race -count=1 ./...

## lint: Run golangci-lint (install: https://golangci-lint.run/usage/install/)
lint:
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed — run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# ── Database migrations ────────────────────────────────────────────────────────

## migrate-up: Apply all pending migrations
migrate-up:
	@echo "→ migrate up ($(MIGRATE_URL))"
	migrate -path $(MIGRATIONS_DIR) -database "$(MIGRATE_URL)" up

## migrate-down: Roll back all migrations
migrate-down:
	@echo "→ migrate down"
	migrate -path $(MIGRATIONS_DIR) -database "$(MIGRATE_URL)" down

## migrate-create name=<migration_name>: Create a new migration file pair
migrate-create:
	@[ "$(name)" ] || (echo "Usage: make migrate-create name=<migration_name>" && exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)

# ── Docker helpers ─────────────────────────────────────────────────────────────

## docker-up: Start only PostgreSQL (for local dev — app runs via `make run`)
docker-up:
	docker compose up -d postgres

## docker-up-all: Build image and start both PostgreSQL + app
docker-up-all:
	docker compose up -d --build

## docker-down: Stop and remove containers (keeps volumes)
docker-down:
	docker compose down

## docker-down-v: Stop and remove containers AND volumes (destroys DB data)
docker-down-v:
	docker compose down -v

## docker-build: Re-build the app image without starting
docker-build:
	docker compose build app

## docker-logs: Tail logs from all services
docker-logs:
	docker compose logs -f

# ── Help ───────────────────────────────────────────────────────────────────────
help:
	@grep -E '^##' Makefile | sed 's/## //'
