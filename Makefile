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
        deps tidy docker-up docker-down \
        proto-gen proto-clean proto-tools \
        buf-lint buf-breaking

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

# ── Protobuf / ConnectRPC code generation ──────────────────────────────────────

PROTO_DIR  := api
GEN_DIR    := gen

## proto-tools: Install protoc-gen-go + protoc-gen-connect-go locally (to ./bin)
proto-tools:
	@echo "→ Installing protoc plugins to $(BUILD_DIR)/"
	@mkdir -p $(BUILD_DIR)
	@GOBIN=$(abspath $(BUILD_DIR)) go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@GOBIN=$(abspath $(BUILD_DIR)) go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
	@echo "✓ protoc-gen-go and protoc-gen-connect-go installed in $(BUILD_DIR)/"
	@echo "  Ensure $(abspath $(BUILD_DIR)) is in $$PATH for protoc, or use make proto-gen"

PROTO_INCLUDE ?= $(BUILD_DIR)/protoc_extracted/include

## proto-gen: Generate Go + Connect code from .proto files (requires protoc + plugins)
proto-gen:
	@echo "→ Generating protobuf + ConnectRPC code..."
	@mkdir -p $(GEN_DIR)
	@PATH="$(abspath $(BUILD_DIR)):$$PATH" protoc \
		--proto_path=. \
		--proto_path=$(PROTO_INCLUDE) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--connect-go_out=$(GEN_DIR) --connect-go_opt=paths=source_relative \
		$$(find $(PROTO_DIR) -name "*.proto" -print)
	@echo "✓ Generated files in $(GEN_DIR)/"

## proto-gen-buf: Alternative codegen using buf.build (install: https://buf.build)
proto-gen-buf:
	@echo "→ Running buf generate..."
	@mkdir -p $(GEN_DIR)
	@buf generate
	@echo "✓ Generated files in $(GEN_DIR)/ via buf"

## proto-clean: Remove all generated proto/connect files
proto-clean:
	rm -rf $(GEN_DIR)
	@echo "✓ Cleaned $(GEN_DIR)/"

## buf-lint: Lint .proto files with buf
buf-lint:
	buf lint

## buf-breaking: Check for breaking changes between current and last committed .proto
buf-breaking:
	buf breaking --against '.git#branch=main'

# ── Help ───────────────────────────────────────────────────────────────────────
help:
	@grep -E '^##' Makefile | sed 's/## //'
