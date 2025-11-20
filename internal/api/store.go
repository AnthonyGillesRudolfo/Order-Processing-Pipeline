package api

import (
    "database/sql"
    "encoding/json"
    "net/http"
    "strings"
    "os"

    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/authz"
    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterStoreRoutes exposes store aliases and secures them via OpenFGA.
// - GET /api/stores/{storeID}/orders -> requires viewer (admin/merchant/agent)
// - POST /api/stores/{storeID}/items -> requires merchant
func RegisterStoreRoutes(mux *http.ServeMux, repo *postgres.Repository, az authz.Client) {
    guard := authz.Require(az, func(r *http.Request) (string, string) {
        // Expect /api/stores/{id}/...
        path := strings.TrimPrefix(r.URL.Path, "/api/stores/")
        parts := strings.Split(path, "/")
        if len(parts) < 2 || parts[0] == "" {
            return "", ""
        }
        storeID, resource := parts[0], parts[1]
        switch resource {
        case "orders":
            if r.Method == http.MethodGet { return "store:" + storeID, "viewer" }
        case "items":
            if r.Method == http.MethodPost { return "store:" + storeID, "merchant" }
        }
        return "", ""
    })

    mux.Handle("/api/stores/", otelhttp.NewHandler(guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        handleStores(repo, w, r)
    })), "stores-api"))
}

func handleStores(repo *postgres.Repository, w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/api/stores/")
    parts := strings.Split(path, "/")
    if len(parts) < 2 || parts[0] == "" {
        http.Error(w, "invalid path, expected: /api/stores/{store_id}/(orders|items)", http.StatusBadRequest)
        return
    }
    storeID := parts[0]
    resource := parts[1]
    switch resource {
    case "orders":
        if r.Method != http.MethodGet {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        listStoreOrders(repo, w, r, storeID)
        return
    case "items":
        // Alias to merchant items endpoints
        // For POST we forward to AddItem
        if r.Method == http.MethodPost {
            runtimeURL := os.Getenv("RESTATE_RUNTIME_URL")
            if runtimeURL == "" { runtimeURL = "http://127.0.0.1:8080" }
            handleAddMerchantItem(w, r, runtimeURL, storeID, "")
            return
        }
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    default:
        http.NotFound(w, r)
        return
    }
}

func listStoreOrders(repo *postgres.Repository, w http.ResponseWriter, r *http.Request, storeID string) {
    if repo == nil || repo.DB == nil {
        http.Error(w, "db unavailable", http.StatusInternalServerError)
        return
    }
    rows, err := repo.DB.Query(`
        SELECT o.id, o.customer_id, o.status, o.total_amount, o.payment_id, o.updated_at,
               COALESCE(p.status,''), COALESCE(p.invoice_url,''),
               COALESCE(oi.items_json,'[]')
        FROM orders o
        LEFT JOIN payments p ON p.id = o.payment_id
        LEFT JOIN (
          SELECT oi.order_id,
                 json_agg(
                   json_build_object(
                     'product_id', oi.item_id,
                     'name', COALESCE(NULLIF(oi.name, ''), mi.name, oi.item_id),
                     'quantity', oi.quantity,
                     'unit_price', oi.unit_price
                   )
                 ) AS items_json
          FROM order_items oi
          LEFT JOIN merchant_items mi ON mi.merchant_id = oi.merchant_id AND mi.item_id = oi.item_id
          GROUP BY oi.order_id
        ) oi ON oi.order_id = o.id
        WHERE o.merchant_id = $1
        ORDER BY o.updated_at DESC
        LIMIT 50`, storeID)
    if err != nil {
        http.Error(w, "query failed", http.StatusInternalServerError)
        return
    }
    defer rows.Close()
    type rowT struct{
        id, customerID, status, paymentID, updatedAt string
        totalAmount sql.NullFloat64
        payStatus, invoiceURL, itemsJSON string
    }
    var list []map[string]any
    for rows.Next() {
        var r rowT
        if err := rows.Scan(&r.id, &r.customerID, &r.status, &r.totalAmount, &r.paymentID, &r.updatedAt, &r.payStatus, &r.invoiceURL, &r.itemsJSON); err != nil {
            continue
        }
        var items any
        _ = json.Unmarshal([]byte(r.itemsJSON), &items)
        list = append(list, map[string]any{
            "id":             r.id,
            "customer_id":    r.customerID,
            "status":         r.status,
            "total_amount":   r.totalAmount.Float64,
            "payment_id":     r.paymentID,
            "payment_status": r.payStatus,
            "invoice_url":    r.invoiceURL,
            "updated_at":     r.updatedAt,
            "items":          items,
        })
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"orders": list})
}
