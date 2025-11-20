package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "time"
)

type tupleKey struct {
    User     string `json:"user"`
    Relation string `json:"relation"`
    Object   string `json:"object"`
}

func main() {
    api := getenv("OPENFGA_API_URL", "http://localhost:8081")
    store := os.Getenv("OPENFGA_STORE_ID")
    if store == "" {
        log.Fatal("OPENFGA_STORE_ID not set. Create a store and export its ID.")
    }
    httpClient := &http.Client{Timeout: 5 * time.Second}

    tuples := []tupleKey{
        {User: "user:alice", Relation: "admin",    Object: "org:acme"},
        {User: "user:bob",   Relation: "merchant", Object: "org:acme"},
        {User: "org:acme",   Relation: "org",      Object: "store:acme-main"}, // note: relation on store points to org
        {User: "store:acme-main", Relation: "store", Object: "order:ord123"},
        {User: "user:charlie", Relation: "buyer",   Object: "order:ord123"},
    }

    if err := writeTuples(httpClient, api, store, tuples); err != nil {
        log.Fatalf("write tuples: %v", err)
    }
    log.Println("seeded tuples")

    // Verify checks
    allowed, err := check(httpClient, api, store, tupleKey{User: "user:alice", Relation: "can_refund", Object: "order:ord123"})
    if err != nil { log.Fatalf("check alice refund: %v", err) }
    log.Printf("Check alice refund -> %v", allowed)
    if !allowed { os.Exit(1) }

    denied, err := check(httpClient, api, store, tupleKey{User: "user:charlie", Relation: "can_refund", Object: "order:ord123"})
    if err != nil { log.Fatalf("check charlie refund: %v", err) }
    log.Printf("Check charlie refund -> %v", denied)
    if denied { os.Exit(1) }

    log.Println("Authz seed verification passed")
}

func writeTuples(httpClient *http.Client, api, store string, tuples []tupleKey) error {
    url := fmt.Sprintf("%s/stores/%s/write", api, store)
    body := map[string]any{"writes": map[string]any{"tuple_keys": tuples}}
    b, _ := json.Marshal(body)
    req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    resp, err := httpClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("write status %d", resp.StatusCode)
    }
    return nil
}

func check(httpClient *http.Client, api, store string, tk tupleKey) (bool, error) {
    url := fmt.Sprintf("%s/stores/%s/check", api, store)
    body := map[string]any{"tuple_key": tk}
    b, _ := json.Marshal(body)
    req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    resp, err := httpClient.Do(req)
    if err != nil { return false, err }
    defer resp.Body.Close()
    var jr struct{ Allowed bool `json:"allowed"` }
    if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil { return false, err }
    return jr.Allowed, nil
}

func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }

