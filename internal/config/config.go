package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// Config aggregates runtime configuration grouped by concern.
type Config struct {
	ServiceName string
	HTTP        HTTPConfig
	Restate     RestateConfig
	Kafka       KafkaConfig
	Database    postgres.DatabaseConfig
	Email       EmailConfig
}

type HTTPConfig struct {
	Addr string
}

type RestateConfig struct {
	ListenAddr string
	RuntimeURL string
}

type KafkaConfig struct {
	Brokers       []string
	OrdersTopic   string
	PaymentsTopic string
	OrdersGroup   string
	PaymentsGroup string
	EmailGroup    string
}

type EmailConfig struct {
	DemoRecipient string
}

// Load reads configuration from environment variables, applying sensible defaults.
func Load() (Config, error) {
	cfg := Config{
		ServiceName: getEnv("SERVICE_NAME", "order-processing-pipeline"),
		HTTP: HTTPConfig{
			Addr: getEnv("HTTP_LISTEN_ADDR", ":3000"),
		},
		Restate: RestateConfig{
			ListenAddr: getEnv("RESTATE_LISTEN_ADDR", ":9081"),
			RuntimeURL: getEnv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"),
		},
		Kafka: KafkaConfig{
			Brokers:       splitAndTrim(getEnv("KAFKA_BROKERS", "localhost:9092")),
			OrdersTopic:   getEnv("KAFKA_ORDERS_TOPIC", "orders.v1"),
			PaymentsTopic: getEnv("KAFKA_PAYMENTS_TOPIC", "payments.v1"),
			OrdersGroup:   getEnv("KAFKA_ORDERS_GROUP_ID", "order-workers"),
			PaymentsGroup: getEnv("KAFKA_PAYMENTS_GROUP_ID", "payment-workers"),
			EmailGroup:    getEnv("KAFKA_EMAIL_GROUP_ID", "email-workers"),
		},
		Email: EmailConfig{
			DemoRecipient: getEnv("DEMO_TO_EMAIL", "test@example.local"),
		},
	}

	portStr := getEnv("ORDER_DB_PORT", "5432")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Config{}, fmt.Errorf("parse ORDER_DB_PORT: %w", err)
	}

	cfg.Database = postgres.DatabaseConfig{
		Host:     getEnv("ORDER_DB_HOST", "localhost"),
		Port:     port,
		Database: getEnv("ORDER_DB_NAME", "orderpipeline"),
		User:     getEnv("ORDER_DB_USER", "orderpipelineadmin"),
		Password: getEnv("ORDER_DB_PASSWORD", ""),
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	var out []string
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
