package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var ErrOpenBaoSecretNotFound = errors.New("openbao secret path not found")

// BootstrapFromOpenBao loads secrets from an OpenBao KV path and exports them as environment variables.
// When OpenBao configuration variables are not present, the function is a no-op so existing workflows continue to work.
func BootstrapFromOpenBao(ctx context.Context) error {
	cfg := openBaoConfigFromEnv()
	if !cfg.enabled {
		return nil
	}

	secrets, err := readSecrets(ctx, cfg)
	if err != nil {
		return err
	}

	for k, v := range secrets {
		_ = os.Setenv(k, v)
	}
	return nil
}

type openBaoConfig struct {
	addr      string
	token     string
	mountPath string
	secretKey string
	namespace string
	enabled   bool
}

func openBaoConfigFromEnv() openBaoConfig {
	addr := strings.TrimSpace(os.Getenv("OPENBAO_ADDR"))
	token := os.Getenv("OPENBAO_TOKEN")
	secretPath := strings.Trim(strings.TrimSpace(os.Getenv("OPENBAO_SECRET_PATH")), "/")

	if addr == "" || token == "" || secretPath == "" {
		return openBaoConfig{enabled: false}
	}

	mount := os.Getenv("OPENBAO_MOUNT")
	if mount == "" {
		mount = "secret"
	}

	namespace := os.Getenv("OPENBAO_NAMESPACE")

	return openBaoConfig{
		addr:      strings.TrimRight(addr, "/"),
		token:     token,
		mountPath: strings.Trim(strings.TrimSpace(mount), "/"),
		secretKey: secretPath,
		namespace: strings.TrimSpace(namespace),
		enabled:   true,
	}
}

func readSecrets(ctx context.Context, cfg openBaoConfig) (map[string]string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/v1/%s/data/%s", cfg.addr, cfg.mountPath, cfg.secretKey),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create OpenBao request: %w", err)
	}

	req.Header.Set("X-Vault-Token", cfg.token)
	if cfg.namespace != "" {
		req.Header.Set("X-Vault-Namespace", cfg.namespace)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call OpenBao: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusNotFound:
		return nil, ErrOpenBaoSecretNotFound
	default:
		return nil, fmt.Errorf("openbao request failed: status=%d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Data map[string]interface{} `json:"data"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode OpenBao response: %w", err)
	}

	out := make(map[string]string, len(payload.Data.Data))
	for k, v := range payload.Data.Data {
		switch val := v.(type) {
		case string:
			out[k] = val
		case fmt.Stringer:
			out[k] = val.String()
		case json.Number:
			out[k] = val.String()
		case float64:
			out[k] = strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", val), "0"), ".")
		default:
			// ignore unsupported types to avoid failing the entire bootstrap
		}
	}

	return out, nil
}
