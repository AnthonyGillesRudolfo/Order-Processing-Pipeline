#!/usr/bin/env bash

set -euo pipefail

# Bootstrap an OpenBao dev instance with application secrets.

OPENBAO_ADDR=${OPENBAO_ADDR:-http://127.0.0.1:8200}
OPENBAO_TOKEN=${OPENBAO_TOKEN:-dev-root-token}
OPENBAO_MOUNT=${OPENBAO_MOUNT:-secret}
OPENBAO_SECRET_PATH=${OPENBAO_SECRET_PATH:-order-processing/dev}

wait_for_openbao() {
    echo "Waiting for OpenBao at ${OPENBAO_ADDR}..."
    for _ in $(seq 1 30); do
        if curl -sSf --header "X-Vault-Token: ${OPENBAO_TOKEN}" \
            "${OPENBAO_ADDR}/v1/sys/health" >/dev/null; then
            echo "OpenBao is ready."
            return 0
        fi
        sleep 2
    done
    echo "OpenBao did not become healthy in time" >&2
    return 1
}

write_secret() {
    local path=$1
    local payload=$2
    curl -sSf --header "X-Vault-Token: ${OPENBAO_TOKEN}" \
        --header "Content-Type: application/json" \
        --request POST \
        --data "${payload}" \
        "${OPENBAO_ADDR}/v1/${OPENBAO_MOUNT}/data/${path}" >/dev/null
}

wait_for_openbao

echo "Seeding application secrets under ${OPENBAO_MOUNT}/data/${OPENBAO_SECRET_PATH}"

write_secret "${OPENBAO_SECRET_PATH}" '{
  "data": {
    "ORDER_DB_HOST": "postgres",
    "ORDER_DB_PORT": "5432",
    "ORDER_DB_NAME": "orderpipeline",
    "ORDER_DB_USER": "orderpipelineadmin",
    "ORDER_DB_PASSWORD": "postgres",
    "KAFKA_BROKERS": "kafka:9092",
    "KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
    "RESTATE_RUNTIME_URL": "http://restate:8080",
    "SECRET_KEY": "xnd_development_NnaKKIesgUACmzR4qUGekPA0y51KIjPmRfWZOaEjXezBAeyje6q6sC03Fb50XW8",
    "XENDIT_SECRET_KEY": "xnd_development_NnaKKIesgUACmzR4qUGekPA0y51KIjPmRfWZOaEjXezBAeyje6q6sC03Fb50XW8",
    "XENDIT_CALLBACK_TOKEN": "MAUJPZibkIPApduvaWV0DtsRbcfAFJJ62VuPl43Hi7FS4cWB",
    "XENDIT_SUCCESS_URL": "https://missy-internarial-rubye.ngrok-free.dev/",
    "XENDIT_FAILURE_URL": "https://missy-internarial-rubye.ngrok-free.dev/",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://jaeger:4318",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://jaeger:4318/v1/traces",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_SERVICE_NAME": "order-processing-pipeline",
    "OTEL_LOG_LEVEL": "debug",
    "WEB_DIR": "/app/web",
    "SMTP_HOST": "mailhog",
    "SMTP_PORT": "1025",
    "SMTP_FROM": "noreply@example.test",
    "SMTP_TLS": "false",
    "OPENROUTER_API_KEY": "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"
  }
}'

echo "OpenBao bootstrap complete."
