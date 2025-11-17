package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	bao "github.com/openbao/openbao/api/v2"
)

// Config aggregates runtime configuration grouped by concern.
type Config struct {
	ServiceName   string
	HTTP          HTTPConfig
	Restate       RestateConfig
	Kafka         KafkaConfig
	Database      postgres.DatabaseConfig
	Email         EmailConfig
	Xendit        XenditConfig
	OpenRouter    OpenRouterConfig
	secretsLoaded bool
	usingOpenBao  bool
}

// XenditConfig holds Xendit payment gateway credentials
type XenditConfig struct {
	SecretKey     string
	CallbackToken string
}

// OpenRouterConfig holds OpenRouter LLM API credentials
type OpenRouterConfig struct {
	APIKey string
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

// newOpenBaoClient creates and authenticates an OpenBao client using AppRole
func newOpenBaoClient() (*bao.Client, error) {
	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		return nil, fmt.Errorf("VAULT_ADDR not set")
	}

	config := bao.DefaultConfig()
	config.Address = vaultAddr

	client, err := bao.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenBao client: %w", err)
	}

	// Authenticate using AppRole
	roleID := os.Getenv("BAO_ROLE_ID")
	secretID := os.Getenv("BAO_SECRET_ID")
	if roleID == "" || secretID == "" {
		return nil, fmt.Errorf("BAO_ROLE_ID or BAO_SECRET_ID not set")
	}

	data := map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Logical().WriteWithContext(ctx, "auth/approle/login", data)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate with AppRole: %w", err)
	}

	if resp == nil || resp.Auth == nil {
		return nil, fmt.Errorf("AppRole authentication returned no token")
	}

	client.SetToken(resp.Auth.ClientToken)
	return client, nil
}

// getSecret fetches a secret from OpenBao at the given path and key
func getSecret(client *bao.Client, path, key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	secret, err := client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret at %s: %w", path, err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("no data found at path %s", path)
	}

	// Handle KV v2 (data wrapper) and KV v1 (direct data)
	data := secret.Data
	if dataWrapper, ok := secret.Data["data"].(map[string]interface{}); ok {
		data = dataWrapper
	}

	value, ok := data[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret at %s", key, path)
	}

	strValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("value for key %s is not a string", key)
	}

	return strValue, nil
}

// loadSecretsFromOpenBao attempts to load all secrets from OpenBao
func (c *Config) loadSecretsFromOpenBao() error {
	client, err := newOpenBaoClient()
	if err != nil {
		return fmt.Errorf("failed to create OpenBao client: %w", err)
	}

	// Load Xendit secrets
	xenditSecretKey, err := getSecret(client, "secret/data/myapp/xendit", "XENDIT_SECRET_KEY")
	if err != nil {
		return fmt.Errorf("failed to load Xendit secret key: %w", err)
	}

	xenditCallbackToken, err := getSecret(client, "secret/data/myapp/xendit", "XENDIT_CALLBACK_TOKEN")
	if err != nil {
		return fmt.Errorf("failed to load Xendit callback token: %w", err)
	}

	// Load OpenRouter secret
	openRouterAPIKey, err := getSecret(client, "secret/data/myapp/openrouter", "OPENROUTER_API_KEY")
	if err != nil {
		return fmt.Errorf("failed to load OpenRouter API key: %w", err)
	}

	// Load Database password
	dbPassword, err := getSecret(client, "secret/data/myapp/database", "ORDER_DB_PASSWORD")
	if err != nil {
		return fmt.Errorf("failed to load database password: %w", err)
	}

	// Populate config
	c.Xendit = XenditConfig{
		SecretKey:     xenditSecretKey,
		CallbackToken: xenditCallbackToken,
	}
	c.OpenRouter = OpenRouterConfig{
		APIKey: openRouterAPIKey,
	}
	c.Database.Password = dbPassword

	// Export to environment variables for backward compatibility
	os.Setenv("XENDIT_SECRET_KEY", xenditSecretKey)
	os.Setenv("XENDIT_CALLBACK_TOKEN", xenditCallbackToken)
	os.Setenv("OPENROUTER_API_KEY", openRouterAPIKey)
	os.Setenv("ORDER_DB_PASSWORD", dbPassword)

	c.secretsLoaded = true
	c.usingOpenBao = true

	log.Println("✓ Secrets loaded successfully from OpenBao")
	return nil
}

// loadSecretsFromEnv loads secrets from environment variables as fallback
func (c *Config) loadSecretsFromEnv() {
	c.Xendit = XenditConfig{
		SecretKey:     os.Getenv("XENDIT_SECRET_KEY"),
		CallbackToken: os.Getenv("XENDIT_CALLBACK_TOKEN"),
	}
	c.OpenRouter = OpenRouterConfig{
		APIKey: os.Getenv("OPENROUTER_API_KEY"),
	}
	// Database password already loaded in Load()

	c.secretsLoaded = true
	c.usingOpenBao = false

	log.Println("✓ Secrets loaded from environment variables")
}

// Load reads configuration from environment variables and OpenBao
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

	// Try to load secrets from OpenBao first, fall back to environment variables
	if err := cfg.loadSecretsFromOpenBao(); err != nil {
		log.Printf("⚠ OpenBao unavailable (%v), falling back to environment variables", err)
		cfg.loadSecretsFromEnv()
	}

	return cfg, nil
}

// GetXenditSecretKey returns the Xendit secret key
func (c *Config) GetXenditSecretKey() string {
	return c.Xendit.SecretKey
}

// GetXenditCallbackToken returns the Xendit callback token
func (c *Config) GetXenditCallbackToken() string {
	return c.Xendit.CallbackToken
}

// GetOpenRouterAPIKey returns the OpenRouter API key
func (c *Config) GetOpenRouterAPIKey() string {
	return c.OpenRouter.APIKey
}

// GetDatabaseURL returns the database connection URL
func (c *Config) GetDatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.Database.User,
		c.Database.Password,
		c.Database.Host,
		c.Database.Port,
		c.Database.Database,
	)
}

// IsUsingOpenBao returns true if secrets were loaded from OpenBao
func (c *Config) IsUsingOpenBao() bool {
	return c.usingOpenBao
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
