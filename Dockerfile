# ---- Build stage ----
FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /app

# Enable static builds
ENV CGO_ENABLED=0

# Dependencies first for better caching
COPY go.mod go.sum ./
COPY third_party/ ./third_party/ 
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Bring in the rest
COPY . .

# Build both binaries (smaller & reproducible)
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server/main.go && \
    go build -trimpath -ldflags="-s -w" -o /out/emailworker ./cmd/emailworker/main.go

# ---- Runtime stage ----
FROM alpine:3.20
WORKDIR /app

# TLS & timezones for outbound HTTPS and sane time handling
RUN apk add --no-cache ca-certificates tzdata

# Copy binaries
COPY --from=builder /out/server /app/order-pipeline
COPY --from=builder /out/emailworker /app/emailworker

# Static web assets (choose one location; keeping /app/web)
COPY web/ /app/web/

# (Optional) drop privileges
# RUN adduser -D -H -s /sbin/nologin app && chown -R app:app /app
# USER app

EXPOSE 3000 9081
ENTRYPOINT ["/app/order-pipeline"]