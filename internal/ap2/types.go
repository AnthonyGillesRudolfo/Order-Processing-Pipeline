package ap2

// Shared AP2 response models used by both HTTP handlers and callers.

// AP2ExecuteResult models the result of an /ap2/execute call.
// Keys are camelCase for public API consistency.
type AP2ExecuteResult struct {
    ExecutionID string `json:"executionId"`
    Status      string `json:"status"`      // "pending" | "completed" | "failed" | "refunded"
    InvoiceLink string `json:"invoiceLink"` // invoice URL
    PaymentID   string `json:"paymentId"`
    OrderID     string `json:"orderId"`
}

// AP2StatusResult models the result of an /ap2/status/{executionId} call.
type AP2StatusResult struct {
    ExecutionID string `json:"executionId"`
    Status      string `json:"status"`
    InvoiceLink string `json:"invoiceLink"`
    PaymentID   string `json:"paymentId"`
    OrderID     string `json:"orderId"`
    CreatedAt   string `json:"createdAt"`
}

// AP2Envelope wraps responses to provide a stable, extensible shape.
type AP2Envelope[T any] struct {
    Result T `json:"result"`
}

