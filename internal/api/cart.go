package api

import (
    "bytes"
    "encoding/json"
    "net/http"
    "strings"

    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterCartRoutes wires the cart API endpoints into the provided mux.
// All operations delegate to the Restate runtime HTTP endpoints.
func RegisterCartRoutes(mux *http.ServeMux) {
    mux.Handle("/api/cart/", otelhttp.NewHandler(http.HandlerFunc(handleCartAPI), "cart-api"))
}

func handleCartAPI(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/api/cart/")
    parts := strings.Split(path, "/")
    if len(parts) < 2 {
        http.Error(w, "Invalid cart API path", http.StatusBadRequest)
        return
    }
    customerID := parts[0]
    action := parts[1]
    switch action {
    case "add":
        handleCartAdd(w, r, customerID)
    case "view":
        handleCartView(w, r, customerID)
    case "update":
        handleCartUpdate(w, r, customerID)
    case "remove":
        handleCartRemove(w, r, customerID)
    default:
        http.Error(w, "Unknown cart action", http.StatusBadRequest)
    }
}

func handleCartAdd(w http.ResponseWriter, r *http.Request, customerID string) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var req struct {
        CustomerID string `json:"customer_id"`
        MerchantID string `json:"merchant_id"`
        Items      []struct {
            ProductID string `json:"product_id"`
            Quantity  int32  `json:"quantity"`
        } `json:"items"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
    url := runtimeURL + "/cart.sv1.CartService/" + customerID + "/AddToCart"
    reqBytes, _ := json.Marshal(req)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(resp.StatusCode)
    var result map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&result)
    _ = json.NewEncoder(w).Encode(result)
}

func handleCartView(w http.ResponseWriter, r *http.Request, customerID string) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var req struct { CustomerID string `json:"customer_id"` }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
    url := runtimeURL + "/cart.sv1.CartService/" + customerID + "/ViewCart"
    reqBytes, _ := json.Marshal(req)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(resp.StatusCode)
    var result map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&result)
    _ = json.NewEncoder(w).Encode(result)
}

func handleCartUpdate(w http.ResponseWriter, r *http.Request, customerID string) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var req struct {
        CustomerID string `json:"customer_id"`
        ProductID  string `json:"product_id"`
        Quantity   int32  `json:"quantity"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
    url := runtimeURL + "/cart.sv1.CartService/" + customerID + "/UpdateCartItem"
    reqBytes, _ := json.Marshal(req)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(resp.StatusCode)
    var result map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&result)
    _ = json.NewEncoder(w).Encode(result)
}

func handleCartRemove(w http.ResponseWriter, r *http.Request, customerID string) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var req struct {
        CustomerID string   `json:"customer_id"`
        ProductIDs []string `json:"product_ids"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
    url := runtimeURL + "/cart.sv1.CartService/" + customerID + "/RemoveFromCart"
    reqBytes, _ := json.Marshal(req)
    resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
    if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        _ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
        return
    }
    defer resp.Body.Close()
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(resp.StatusCode)
    var result map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&result)
    _ = json.NewEncoder(w).Encode(result)
}

