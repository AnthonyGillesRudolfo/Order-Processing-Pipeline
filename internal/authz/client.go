package authz

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"
)

// Client performs authorization checks.
type Client interface {
    Check(ctx context.Context, user, object, relation string) (bool, error)
}

// OpenFGAClient implements Client against an OpenFGA HTTP API.
type OpenFGAClient struct {
    apiURL    string
    storeID   string
    http      *http.Client
    available bool
}

// NewFromEnv constructs a Client based on OPENFGA_* env vars.
// If not configured, returns a no-op client that always allows.
func NewFromEnv() Client {
    apiURL := os.Getenv("OPENFGA_API_URL")
    storeID := os.Getenv("OPENFGA_STORE_ID")
    if apiURL == "" || storeID == "" {
        return &NoopClient{}
    }
    return &OpenFGAClient{
        apiURL:  apiURL,
        storeID: storeID,
        http: &http.Client{Timeout: 3 * time.Second},
        available: true,
    }
}

// Check calls OpenFGA /check. Returns (false, nil) on a definitive deny.
func (c *OpenFGAClient) Check(ctx context.Context, user, object, relation string) (bool, error) {
    if c == nil || !c.available {
        return true, nil
    }
    // POST {api}/stores/{store_id}/check
    url := fmt.Sprintf("%s/stores/%s/check", c.apiURL, c.storeID)
    body := map[string]any{
        "tuple_key": map[string]string{
            "user":     user,
            "relation": relation,
            "object":   object,
        },
    }
    b, _ := json.Marshal(body)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
    if err != nil { return false, err }
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.http.Do(req)
    if err != nil { return false, err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return false, fmt.Errorf("openfga check status %d", resp.StatusCode)
    }
    var jr struct{ Allowed bool `json:"allowed"` }
    if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil { return false, err }
    return jr.Allowed, nil
}

// NoopClient allows everything. Useful for local dev without OpenFGA.
type NoopClient struct{}

func (n *NoopClient) Check(ctx context.Context, user, object, relation string) (bool, error) {
    return true, nil
}

