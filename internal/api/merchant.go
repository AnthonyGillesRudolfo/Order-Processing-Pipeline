package api

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"

    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterMerchantRoutes wires merchant management endpoints into the mux.
func RegisterMerchantRoutes(mux *http.ServeMux, repo *postgres.Repository) {
    mux.Handle("/api/merchants/", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        handleMerchantAPI(repo, w, r)
    }), "merchant-api"))
}

func handleMerchantAPI(repo *postgres.Repository, w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/api/merchants/")
    parts := strings.Split(path, "/")
    if len(parts) < 2 || parts[0] == "" || parts[1] != "items" {
        http.Error(w, "invalid path, expected: /api/merchants/{merchant_id}/items[/{item_id}]", http.StatusBadRequest)
        return
    }
    merchantID := parts[0]
    itemID := ""
    if len(parts) > 2 { itemID = parts[2] }
    runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")

    switch r.Method {
    case http.MethodGet:
        handleListMerchantItems(repo, w, r, runtimeURL, merchantID)
    case http.MethodPost:
        handleAddMerchantItem(w, r, runtimeURL, merchantID, itemID)
    case http.MethodPut:
        handleUpdateMerchantItem(w, r, runtimeURL, merchantID, itemID)
    case http.MethodDelete:
        handleDeleteMerchantItem(w, r, runtimeURL, merchantID, itemID)
    default:
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
    }
}

func handleListMerchantItems(repo *postgres.Repository, w http.ResponseWriter, r *http.Request, runtimeURL, merchantID string) {
    // Prefer authoritative stock from database; fallback to Restate runtime
    if repo != nil {
        if items, err := postgres.GetMerchantItems(merchantID); err == nil {
            w.Header().Set("Content-Type", "application/json")
            out := make([]map[string]any, 0, len(items))
            for _, it := range items {
                out = append(out, map[string]any{
                    "itemId":   it.ItemId,
                    "name":     it.Name,
                    "quantity": it.Quantity,
                    "price":    it.Price,
                })
            }
            _ = json.NewEncoder(w).Encode(map[string]any{"items": out})
            return
        }
    }
    // Fallback to Restate runtime
    url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/ListItems", runtimeURL, merchantID)
    reqBody := map[string]any{"merchant_id": merchantID, "page_size": 100, "page_token": ""}
    reqBytes, _ := json.Marshal(reqBody)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
        return
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        var detail map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&detail)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to fetch merchant items", "detail": detail})
        return
    }
    var response map[string]any
    if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to decode response", "detail": err.Error()})
        return
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(response)
}

func handleAddMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
    var reqBody map[string]any
    if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
        http.Error(w, "invalid JSON body", http.StatusBadRequest)
        return
    }
    url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/AddItem", runtimeURL, merchantID)
    reqBytes, _ := json.Marshal(reqBody)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        var detail map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&detail)
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to add merchant item", "detail": detail})
        return
    }
    var response map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&response)
    _ = json.NewEncoder(w).Encode(response)
}

func handleUpdateMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
    var reqBody map[string]any
    if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
        http.Error(w, "invalid JSON body", http.StatusBadRequest)
        return
    }
    url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/UpdateItem", runtimeURL, merchantID)
    reqBytes, _ := json.Marshal(reqBody)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        var detail map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&detail)
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to update merchant item", "detail": detail})
        return
    }
    var response map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&response)
    _ = json.NewEncoder(w).Encode(response)
}

func handleDeleteMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
    if itemID == "" {
        http.Error(w, "item ID required for deletion", http.StatusBadRequest)
        return
    }
    url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/DeleteItem", runtimeURL, merchantID)
    reqBody := map[string]any{"merchant_id": merchantID, "item_id": itemID}
    reqBytes, _ := json.Marshal(reqBody)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        var detail map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&detail)
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to delete merchant item", "detail": detail})
        return
    }
    var response map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&response)
    _ = json.NewEncoder(w).Encode(response)
}

