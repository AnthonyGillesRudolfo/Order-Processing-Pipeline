package api

import (
    "bytes"
    "database/sql"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "strings"

    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterOrdersRoutes wires the orders API endpoints into the provided mux.
// It uses the repository for DB access and calls the Restate runtime via HTTP for actions.
func RegisterOrdersRoutes(mux *http.ServeMux, repo *postgres.Repository) {
    // GET /api/orders → list
    mux.Handle("/api/orders", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        handleOrdersList(repo, w, r)
    }), "orders-list"))

    // /api/orders/* → individual + actions
    mux.Handle("/api/orders/", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        handleOrders(repo, w, r)
    }), "orders"))
}

func handleOrdersList(repo *postgres.Repository, w http.ResponseWriter, r *http.Request) {
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
        ORDER BY o.updated_at DESC
        LIMIT 50`)
    if err != nil {
        http.Error(w, "query failed", http.StatusInternalServerError)
        return
    }
    defer rows.Close()
    var list []map[string]any
    for rows.Next() {
        var id, customerID, status, paymentID, updatedAt string
        var totalAmount sql.NullFloat64
        var payStatus, invoiceURL, itemsJSON string
        if err := rows.Scan(&id, &customerID, &status, &totalAmount, &paymentID, &updatedAt, &payStatus, &invoiceURL, &itemsJSON); err != nil {
            continue
        }
        var items any
        _ = json.Unmarshal([]byte(itemsJSON), &items)
        list = append(list, map[string]any{
            "id":             id,
            "customer_id":    customerID,
            "status":         status,
            "total_amount":   totalAmount.Float64,
            "payment_id":     paymentID,
            "payment_status": payStatus,
            "invoice_url":    invoiceURL,
            "updated_at":     updatedAt,
            "items":          items,
        })
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"orders": list})
}

func handleOrders(repo *postgres.Repository, w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/api/orders/")
    if path == "" {
        http.Error(w, "order id required", http.StatusBadRequest)
        return
    }

    // Ship
    if strings.HasSuffix(path, "/ship") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/ship")
        var reqBody struct{ TrackingNumber, Carrier, ServiceType string }
        if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
            http.Error(w, "invalid JSON body", http.StatusBadRequest)
            return
        }
        if reqBody.TrackingNumber == "" { reqBody.TrackingNumber = "TRK00000000" }
        if reqBody.Carrier == "" { reqBody.Carrier = "FedEx" }
        if reqBody.ServiceType == "" { reqBody.ServiceType = "Ground" }
        url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ShipOrder", getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"), orderID)
        shipReq := map[string]any{"order_id": orderID, "tracking_number": reqBody.TrackingNumber, "carrier": reqBody.Carrier, "service_type": reqBody.ServiceType}
        if err := postJSON(url, shipReq); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to ship order", "detail": err.Error()})
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order shipped successfully"})
        return
    }

    // Deliver
    if strings.HasSuffix(path, "/deliver") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/deliver")
        url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/DeliverOrder", getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"), orderID)
        if err := postJSON(url, map[string]any{"order_id": orderID}); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to deliver order", "detail": err.Error()})
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order delivered successfully"})
        return
    }

    // Cancel
    if strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/cancel")
        var reqBody struct{ Reason string `json:"reason"` }
        if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
            http.Error(w, "invalid JSON body", http.StatusBadRequest)
            return
        }
        url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/CancelOrder", getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"), orderID)
        if err := postJSON(url, map[string]any{"order_id": orderID, "reason": reqBody.Reason}); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to cancel order", "detail": err.Error()})
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order cancelled successfully"})
        return
    }

    // Confirm
    if strings.HasSuffix(path, "/confirm") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/confirm")
        url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ConfirmOrder", getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"), orderID)
        if err := postJSON(url, map[string]any{"order_id": orderID}); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to confirm order", "detail": err.Error()})
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order confirmed successfully"})
        return
    }

    // Return
    if strings.HasSuffix(path, "/return") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/return")
        var reqBody struct{ Reason string `json:"reason"` }
        if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
            http.Error(w, "invalid JSON body", http.StatusBadRequest)
            return
        }
        url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ReturnOrder", getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080"), orderID)
        b, _ := json.Marshal(map[string]any{"order_id": orderID, "reason": reqBody.Reason})
        resp, err := http.Post(url, "application/json", bytes.NewReader(b))
        if err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to return order", "detail": err.Error()})
            return
        }
        defer resp.Body.Close()
        if resp.StatusCode < 200 || resp.StatusCode >= 300 {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to return order", "detail": fmt.Sprintf("status %d", resp.StatusCode)})
            return
        }
        var returnResp map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&returnResp)
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(returnResp)
        return
    }

    // Simulate payment success (dev)
    if strings.HasSuffix(path, "/simulate_payment_success") && r.Method == http.MethodPost {
        orderID := strings.TrimSuffix(path, "/simulate_payment_success")
        info, err := repo.GetOrderWithPayment(orderID)
        if err != nil {
            http.Error(w, "order not found", http.StatusNotFound)
            return
        }
        orderMap, _ := info["order"].(map[string]any)
        paymentID, _ := orderMap["payment_id"].(string)
        if paymentID == "" && repo != nil && repo.DB != nil {
            // Fallback: find latest payment by order_id
            _ = repo.DB.QueryRow(`SELECT id FROM payments WHERE order_id = $1 ORDER BY updated_at DESC LIMIT 1`, orderID).Scan(&paymentID)
        }
        if paymentID == "" {
            http.Error(w, "payment_id not set", http.StatusBadRequest)
            return
        }
        runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
        url := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentCompleted", runtimeURL, paymentID)
        if err := postJSON(url, map[string]any{"payment_id": paymentID, "order_id": orderID}); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to mark payment completed", "detail": err.Error(), "payment_id": paymentID})
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
        return
    }

    // GET /api/orders/{id}
    if r.Method == http.MethodGet {
        orderID := path
        resp, err := repo.GetOrderWithPayment(orderID)
        if err != nil {
            http.Error(w, "order not found or DB unavailable", http.StatusNotFound)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(resp)
        return
    }

    http.NotFound(w, r)
}

func postJSON(url string, body map[string]any) error {
    b, _ := json.Marshal(body)
    resp, err := http.Post(url, "application/json", bytes.NewReader(b))
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("status %d", resp.StatusCode)
    }
    return nil
}

// order details helper removed in favor of postgres.GetOrderWithPayment

func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
