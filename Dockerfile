# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

# Install git (needed by go modules for private repos) and ca-certificates
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Cache dependency downloads as a separate layer.
# Only re-runs when go.mod / go.sum change.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically-linked binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/bin/gogo-dl ./cmd/server/main.go

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM scratch

# Bring in timezone data and TLS certificates from the builder stage.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary.
COPY --from=builder /app/bin/gogo-dl /gogo-dl

# Copy migration files so the app can run migrations on startup.
COPY --from=builder /app/migrations /migrations

EXPOSE 8080

ENTRYPOINT ["/gogo-dl"]
