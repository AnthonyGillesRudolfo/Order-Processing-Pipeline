package api

import (
    "context"
    "encoding/json"
    "log"
    "net/http"
    "os"
    "strings"

    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/events"
    "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterWebhookRoutes mounts payment webhook endpoints.
func RegisterWebhookRoutes(mux *http.ServeMux, prod *events.Producer, repo *postgres.Repository) {
    mux.Handle("/api/webhooks/xendit", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        processPaymentWebhook(prod, repo, w, r, true)
    }), "xendit-webhook"))

    mux.Handle("/webhooks/payment", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        processPaymentWebhook(prod, repo, w, r, false)
    }), "payment-webhook"))
}

// processPaymentWebhook handles both Xendit and legacy payment webhooks.
func processPaymentWebhook(prod *events.Producer, repo *postgres.Repository, w http.ResponseWriter, r *http.Request, verifyToken bool) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    if verifyToken {
        expected := os.Getenv("XENDIT_CALLBACK_TOKEN")
        if expected != "" && r.Header.Get("x-callback-token") != expected {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
    }

    var payload map[string]any
    if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
        http.Error(w, "invalid JSON payload", http.StatusBadRequest)
        return
    }

    externalID, _ := payload["external_id"].(string)
    status, _ := payload["status"].(string)
    invoiceID, _ := payload["id"].(string)
    if externalID == "" {
        http.Error(w, "missing external_id", http.StatusBadRequest)
        return
    }

    var (
        orderID, customerID, invoiceURL string
        totalAmount float64
    )
    if info, err := repo.GetOrderWithPaymentByPaymentID(externalID); err == nil {
        if ord, ok := info["order"].(map[string]any); ok {
            if v, ok := ord["id"].(string); ok { orderID = v }
            if v, ok := ord["customer_id"].(string); ok { customerID = v }
            if v, ok := ord["total_amount"].(float64); ok { totalAmount = v }
        }
        if pay, ok := info["payment"].(map[string]any); ok {
            if v, ok := pay["invoice_url"].(string); ok { invoiceURL = v }
        }
    }

    eventType, err := publishPaymentEvent(r.Context(), prod, status, orderID, externalID, customerID, invoiceURL, totalAmount, invoiceID)
    if err != nil {
        log.Printf("[Webhook] failed to publish event: %v", err)
        http.Error(w, "failed to enqueue payment event", http.StatusInternalServerError)
        return
    }
    if eventType == "" {
        log.Printf("[Webhook] ignoring status %s for %s", status, externalID)
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"status": "received"})
}

func publishPaymentEvent(ctx context.Context, prod *events.Producer, status, orderID, paymentID, customerID, invoiceURL string, totalAmount float64, invoiceID string) (string, error) {
    normalized := strings.ToUpper(status)
    var eventType string
    switch normalized {
    case "PAID":
        eventType = "PaymentCompleted"
    case "EXPIRED":
        eventType = "PaymentExpired"
    default:
        return "", nil
    }
    key := orderID
    if key == "" { key = paymentID }
    data := map[string]any{
        "orderId":     orderID,
        "paymentId":   paymentID,
        "customerId":  customerID,
        "invoiceURL":  invoiceURL,
        "totalAmount": totalAmount,
        "provider":    "xendit",
        "status":      normalized,
    }
    if invoiceID != "" { data["invoiceId"] = invoiceID }
    evt := events.Envelope{EventType: eventType, EventVersion: "v1", AggregateID: key, Data: data}
    if err := prod.Publish(ctx, "payments.v1", key, evt); err != nil { return "", err }
    return eventType, nil
}

// removed repo-level helper in favor of postgres.GetOrderWithPaymentByPaymentID
