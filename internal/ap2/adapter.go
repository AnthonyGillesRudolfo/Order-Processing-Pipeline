package ap2

import (
	"time"
)

// Mandate represents a user authorization credential for AP2
type Mandate struct {
	ID          string    `json:"id"`
	CustomerID  string    `json:"customer_id"`
	Scope       string    `json:"scope"`
	AmountLimit float64   `json:"amount_limit"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Intent represents a transaction intent with cart details
type Intent struct {
	ID          string     `json:"id"`
	MandateID   string     `json:"mandate_id"`
	CustomerID  string     `json:"customer_id"`
	CartID      string     `json:"cart_id"`
	TotalAmount float64    `json:"total_amount"`
	Items       []CartItem `json:"items"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CartItem represents an item in the cart (reused from cart package)
type CartItem struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int32   `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// ExecutionResult represents the payment execution outcome
type ExecutionResult struct {
	ID              string    `json:"id"`
	IntentID        string    `json:"intent_id"`
	AuthorizationID string    `json:"authorization_id"`
	OrderID         string    `json:"order_id"`
	PaymentID       string    `json:"payment_id"`
	Status          string    `json:"status"`
	InvoiceURL      string    `json:"invoice_url"`
	CreatedAt       time.Time `json:"created_at"`
}

// Authorization represents a payment authorization
type Authorization struct {
	ID         string    `json:"id"`
	IntentID   string    `json:"intent_id"`
	MandateID  string    `json:"mandate_id"`
	Authorized bool      `json:"authorized"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// Refund represents a refund request/result
type Refund struct {
	ID          string    `json:"id"`
	ExecutionID string    `json:"execution_id"`
	Amount      float64   `json:"amount"`
	Reason      string    `json:"reason"`
	Status      string    `json:"status"`
	RefundID    string    `json:"refund_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// ShippingAddress represents shipping information
type ShippingAddress struct {
	AddressLine1   string `json:"address_line1"`
	AddressLine2   string `json:"address_line2"`
	City           string `json:"city"`
	State          string `json:"state"`
	PostalCode     string `json:"postal_code"`
	Country        string `json:"country"`
	DeliveryMethod string `json:"delivery_method"`
}

// ShippingPreferences represents customer shipping preferences
type ShippingPreferences struct {
	CustomerID string          `json:"customer_id"`
	Address    ShippingAddress `json:"address"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// AP2 Status Constants
const (
	// Mandate Status
	MandateStatusActive  = "active"
	MandateStatusExpired = "expired"
	MandateStatusRevoked = "revoked"

	// Intent Status
	IntentStatusCreated    = "created"
	IntentStatusValidated  = "validated"
	IntentStatusAuthorized = "authorized"
	IntentStatusExecuted   = "executed"
	IntentStatusFailed     = "failed"

	// Execution Status
	ExecutionStatusPending   = "pending"
	ExecutionStatusCompleted = "completed"
	ExecutionStatusFailed    = "failed"
	ExecutionStatusRefunded  = "refunded"

	// Authorization Status
	AuthorizationStatusApproved = "approved"
	AuthorizationStatusDenied   = "denied"
)

// CreateMandate generates a mandate from user confirmation
func CreateMandate(customerID, scope string, amountLimit float64, expiresAt time.Time) *Mandate {
	return &Mandate{
		ID:          generateID("mandate"),
		CustomerID:  customerID,
		Scope:       scope,
		AmountLimit: amountLimit,
		ExpiresAt:   expiresAt,
		Status:      MandateStatusActive,
		CreatedAt:   time.Now(),
	}
}

// ValidateMandate verifies that a mandate is valid and not expired
func ValidateMandate(mandate *Mandate) (bool, string) {
	if mandate == nil {
		return false, "mandate is nil"
	}

	if mandate.Status != MandateStatusActive {
		return false, "mandate is not active"
	}

	if time.Now().After(mandate.ExpiresAt) {
		return false, "mandate has expired"
	}

	return true, ""
}

// CreateIntent builds a payment intent from cart
func CreateIntent(mandateID, customerID, cartID string, items []CartItem, totalAmount float64) *Intent {
	return &Intent{
		ID:          generateID("intent"),
		MandateID:   mandateID,
		CustomerID:  customerID,
		CartID:      cartID,
		TotalAmount: totalAmount,
		Items:       items,
		Status:      IntentStatusCreated,
		CreatedAt:   time.Now(),
	}
}

// MapXenditStatusToAP2 converts Xendit status to AP2 ExecutionResult status
func MapXenditStatusToAP2(xenditStatus string) string {
	switch xenditStatus {
	case "PAID":
		return ExecutionStatusCompleted
	case "EXPIRED":
		return ExecutionStatusFailed
	case "FAILED":
		return ExecutionStatusFailed
	case "PENDING":
		return ExecutionStatusPending
	default:
		return ExecutionStatusPending
	}
}

// ValidateIntentAmount checks if the intent amount is within mandate limits
func ValidateIntentAmount(intent *Intent, mandate *Mandate) (bool, string) {
	if intent.TotalAmount > mandate.AmountLimit {
		return false, "intent amount exceeds mandate limit"
	}
	return true, ""
}

// CreateAuthorization creates a new authorization
func CreateAuthorization(intentID, mandateID string, authorized bool, message string) *Authorization {
	return &Authorization{
		ID:         generateID("auth"),
		IntentID:   intentID,
		MandateID:  mandateID,
		Authorized: authorized,
		Message:    message,
		CreatedAt:  time.Now(),
	}
}

// CreateExecutionResult creates a new execution result
func CreateExecutionResult(intentID, authorizationID, orderID, paymentID, status, invoiceURL string) *ExecutionResult {
	return &ExecutionResult{
		ID:              generateID("exec"),
		IntentID:        intentID,
		AuthorizationID: authorizationID,
		OrderID:         orderID,
		PaymentID:       paymentID,
		Status:          status,
		InvoiceURL:      invoiceURL,
		CreatedAt:       time.Now(),
	}
}

// CreateRefund creates a new refund request
func CreateRefund(executionID string, amount float64, reason string) *Refund {
	return &Refund{
		ID:          generateID("refund"),
		ExecutionID: executionID,
		Amount:      amount,
		Reason:      reason,
		Status:      "pending",
		CreatedAt:   time.Now(),
	}
}

// generateID generates a unique ID with a prefix
func generateID(prefix string) string {
	return prefix + "-" + time.Now().Format("20060102150405") + "-" + randomString(8)
}

// randomString generates a random string of specified length
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
