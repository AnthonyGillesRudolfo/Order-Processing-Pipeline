MODULE := $(shell go list -m)

.PHONY: buf-lint buf-generate buf-breaking proto proto-check-clean run migrate-up migrate-down db-env migrate-create migrate-up-cli migrate-down-cli migrate-force

BUF_CACHE_DIR ?= .bufcache

# Database environment (override via env)
ORDER_DB_HOST ?= localhost
ORDER_DB_PORT ?= 5432
ORDER_DB_NAME ?= orderpipeline
ORDER_DB_USER ?= orderpipelineadmin
ORDER_DB_PASSWORD ?=

# Export env to run target
db-env:
	@echo "Using DB: host=$(ORDER_DB_HOST) port=$(ORDER_DB_PORT) name=$(ORDER_DB_NAME) user=$(ORDER_DB_USER)"

# Buf helpers
buf-lint:
	@cd api && BUF_CACHE_DIR=../$(BUF_CACHE_DIR) buf lint

buf-generate:
	@rm -rf gen/go && mkdir -p gen/go
	@cd api && BUF_CACHE_DIR=../$(BUF_CACHE_DIR) buf generate

buf-breaking:
	@cd api && BUF_CACHE_DIR=../$(BUF_CACHE_DIR) buf breaking --against '../.git#branch=master,subdir=api'

# Generate Go code via buf
proto: buf-lint buf-generate buf-breaking
	@echo "Protos linted, generated, and checked for breaking changes."

proto-check-clean:
	@git config --global --add safe.directory $(PWD)
	@if ! git diff --quiet; then \
	  echo 'Protobuf outputs are not up to date. Run "make proto" and commit changes.'; \
	  git status --porcelain; \
	  exit 1; \
	fi
# Run your app
run: db-env
	unset PGREQUIRESSL && \
	PGSSLMODE=disable \
	ORDER_DB_HOST=$(ORDER_DB_HOST) \
	ORDER_DB_PORT=$(ORDER_DB_PORT) \
	ORDER_DB_NAME=$(ORDER_DB_NAME) \
	ORDER_DB_USER=$(ORDER_DB_USER) \
	ORDER_DB_PASSWORD=$(ORDER_DB_PASSWORD) \
	go run ./cmd/server

# Simple SQL-based migrations using psql (legacy)
migrate-up:
	@[ -f db/migrations/000001_init_core.up.sql ] || (echo "No up migration found" && exit 1)
	psql "host=$(ORDER_DB_HOST) port=$(ORDER_DB_PORT) dbname=$(ORDER_DB_NAME) user=$(ORDER_DB_USER) password=$(ORDER_DB_PASSWORD) sslmode=disable" -f db/migrations/000001_init_core.up.sql

migrate-down:
	@[ -f db/migrations/000001_init_core.down.sql ] || (echo "No down migration found" && exit 1)
	psql "host=$(ORDER_DB_HOST) port=$(ORDER_DB_PORT) dbname=$(ORDER_DB_NAME) user=$(ORDER_DB_USER) password=$(ORDER_DB_PASSWORD) sslmode=disable" -f db/migrations/000001_init_core.down.sql

# golang-migrate CLI targets
# Install migrate CLI (macOS): brew install golang-migrate
# Docs: https://github.com/golang-migrate/migrate
MIGRATE_DSN := postgres://$(ORDER_DB_USER):$(ORDER_DB_PASSWORD)@$(ORDER_DB_HOST):$(ORDER_DB_PORT)/$(ORDER_DB_NAME)?sslmode=disable

migrate-create:
	@[ -n "$(name)" ] || (echo "Usage: make migrate-create name=snake_case_name" && exit 1)
	@migrate create -ext sql -dir db/migrations -seq $(name)

migrate-up-cli:
	@migrate -path db/migrations -database "$(MIGRATE_DSN)" up

migrate-down-cli:
	@migrate -path db/migrations -database "$(MIGRATE_DSN)" down 1

migrate-force:
	@[ -n "$(version)" ] || (echo "Usage: make migrate-force version=NNN" && exit 1)
	@migrate -path db/migrations -database "$(MIGRATE_DSN)" force $(version)
