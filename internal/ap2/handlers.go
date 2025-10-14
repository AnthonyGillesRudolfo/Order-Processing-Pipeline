package ap2

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/google/uuid"
)

// CreateMandateRequest represents the request to create a mandate
type CreateMandateRequest struct {
	CustomerID  string  `json:"customer_id"`
	Scope       string  `json:"scope"`
	AmountLimit float64 `json:"amount_limit"`
	ExpiresAt   string  `json:"expires_at"`
}

// CreateMandateResponse represents the response after creating a mandate
type CreateMandateResponse struct {
	MandateID string `json:"mandate_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// GetMandateResponse represents the response for getting a mandate
type GetMandateResponse struct {
	Mandate *Mandate `json:"mandate"`
}

// CreateIntentRequest represents the request to create an intent
type CreateIntentRequest struct {
	MandateID       string          `json:"mandate_id"`
	CustomerID      string          `json:"customer_id"`
	CartID          string          `json:"cart_id"`
	ShippingAddress ShippingAddress `json:"shipping_address"`
}

// CreateIntentResponse represents the response after creating an intent
type CreateIntentResponse struct {
	IntentID    string     `json:"intent_id"`
	TotalAmount float64    `json:"total_amount"`
	Items       []CartItem `json:"items"`
	Status      string     `json:"status"`
}

// AuthorizeRequest represents the request to authorize a payment
type AuthorizeRequest struct {
	IntentID  string `json:"intent_id"`
	MandateID string `json:"mandate_id"`
}

// AuthorizeResponse represents the response after authorization
type AuthorizeResponse struct {
	Authorized      bool   `json:"authorized"`
	AuthorizationID string `json:"authorization_id"`
	Message         string `json:"message"`
}

// ExecuteRequest represents the request to execute a payment
type ExecuteRequest struct {
	AuthorizationID string `json:"authorization_id"`
	IntentID        string `json:"intent_id"`
}

// ExecuteResponse represents the response after execution
type ExecuteResponse struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	InvoiceURL  string `json:"invoice_url"`
	PaymentID   string `json:"payment_id"`
	OrderID     string `json:"order_id"`
}

// RefundRequest represents the request to process a refund
type RefundRequest struct {
	ExecutionID string  `json:"execution_id"`
	Amount      float64 `json:"amount"`
	Reason      string  `json:"reason"`
}

// RefundResponse represents the response after refund processing
type RefundResponse struct {
	RefundID string `json:"refund_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

// HandleCreateMandate handles POST /ap2/mandates
func HandleCreateMandate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateMandateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// Parse expires_at
	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		http.Error(w, "invalid expires_at format, use RFC3339", http.StatusBadRequest)
		return
	}

	// Create mandate
	mandate := CreateMandate(req.CustomerID, req.Scope, req.AmountLimit, expiresAt)

	// Store in database using direct SQL
	if postgres.DB != nil {
		_, err = postgres.DB.Exec(`
			INSERT INTO ap2_mandates (id, customer_id, scope, amount_limit, expires_at, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, mandate.ID, mandate.CustomerID, mandate.Scope, mandate.AmountLimit, mandate.ExpiresAt, mandate.Status, mandate.CreatedAt)

		if err != nil {
			log.Printf("[AP2] Failed to insert mandate: %v", err)
			http.Error(w, "failed to create mandate", http.StatusInternalServerError)
			return
		}
	}

	response := CreateMandateResponse{
		MandateID: mandate.ID,
		Status:    mandate.Status,
		CreatedAt: mandate.CreatedAt.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleGetMandate handles GET /ap2/mandates/{id}
func HandleGetMandate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract mandate ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/ap2/mandates/")
	if path == "" {
		http.Error(w, "mandate ID required", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	var id, customerID, scope, status string
	var amountLimit float64
	var expiresAt, createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, customer_id, scope, amount_limit, expires_at, status, created_at
		FROM ap2_mandates
		WHERE id = $1
	`, path).Scan(&id, &customerID, &scope, &amountLimit, &expiresAt, &status, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "mandate not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get mandate: %v", err)
		http.Error(w, "failed to get mandate", http.StatusInternalServerError)
		return
	}

	mandate := &Mandate{
		ID:          id,
		CustomerID:  customerID,
		Scope:       scope,
		AmountLimit: amountLimit,
		ExpiresAt:   expiresAt,
		Status:      status,
		CreatedAt:   createdAt,
	}

	response := GetMandateResponse{
		Mandate: mandate,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleCreateIntent handles POST /ap2/intents
func HandleCreateIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	// Get mandate
	var mandateID, customerID, scope, status string
	var amountLimit float64
	var expiresAt, createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, customer_id, scope, amount_limit, expires_at, status, created_at
		FROM ap2_mandates
		WHERE id = $1
	`, req.MandateID).Scan(&mandateID, &customerID, &scope, &amountLimit, &expiresAt, &status, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "mandate not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get mandate: %v", err)
		http.Error(w, "failed to get mandate", http.StatusInternalServerError)
		return
	}

	// Create mandate object for validation
	mandate := &Mandate{
		ID:          mandateID,
		CustomerID:  customerID,
		Scope:       scope,
		AmountLimit: amountLimit,
		ExpiresAt:   expiresAt,
		Status:      status,
		CreatedAt:   createdAt,
	}

	// Validate mandate
	valid, message := ValidateMandate(mandate)
	if !valid {
		http.Error(w, message, http.StatusBadRequest)
		return
	}

	// Get cart from Restate (this would need to be implemented)
	// For now, we'll simulate getting cart data
	cartItems := []CartItem{
		{ProductID: "i_001", Name: "Apple", Quantity: 2, UnitPrice: 1.50},
		{ProductID: "i_002", Name: "Banana", Quantity: 1, UnitPrice: 0.75},
	}
	totalAmount := 0.0
	for _, item := range cartItems {
		totalAmount += float64(item.Quantity) * item.UnitPrice
	}

	// Create intent
	intent := CreateIntent(req.MandateID, req.CustomerID, req.CartID, cartItems, totalAmount)

	// Validate intent amount
	valid, message = ValidateIntentAmount(intent, mandate)
	if !valid {
		http.Error(w, message, http.StatusBadRequest)
		return
	}

	// Store in database
	_, err = postgres.DB.Exec(`
		INSERT INTO ap2_intents (id, mandate_id, customer_id, cart_id, total_amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, intent.ID, intent.MandateID, intent.CustomerID, intent.CartID, intent.TotalAmount, intent.Status, intent.CreatedAt)

	if err != nil {
		log.Printf("[AP2] Failed to insert intent: %v", err)
		http.Error(w, "failed to create intent", http.StatusInternalServerError)
		return
	}

	response := CreateIntentResponse{
		IntentID:    intent.ID,
		TotalAmount: intent.TotalAmount,
		Items:       intent.Items,
		Status:      intent.Status,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleAuthorize handles POST /ap2/authorize
func HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthorizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	// Get intent
	var intentID, mandateID, customerID, cartID, status string
	var totalAmount float64
	var createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, mandate_id, customer_id, cart_id, total_amount, status, created_at
		FROM ap2_intents
		WHERE id = $1
	`, req.IntentID).Scan(&intentID, &mandateID, &customerID, &cartID, &totalAmount, &status, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "intent not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get intent: %v", err)
		http.Error(w, "failed to get intent", http.StatusInternalServerError)
		return
	}

	// Get mandate
	var mandateIDResult, mandateCustomerID, scope, mandateStatus string
	var amountLimit float64
	var expiresAt, mandateCreatedAt time.Time

	err = postgres.DB.QueryRow(`
		SELECT id, customer_id, scope, amount_limit, expires_at, status, created_at
		FROM ap2_mandates
		WHERE id = $1
	`, req.MandateID).Scan(&mandateIDResult, &mandateCustomerID, &scope, &amountLimit, &expiresAt, &mandateStatus, &mandateCreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "mandate not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get mandate: %v", err)
		http.Error(w, "failed to get mandate", http.StatusInternalServerError)
		return
	}

	// Create mandate object for validation
	mandate := &Mandate{
		ID:          mandateIDResult,
		CustomerID:  mandateCustomerID,
		Scope:       scope,
		AmountLimit: amountLimit,
		ExpiresAt:   expiresAt,
		Status:      mandateStatus,
		CreatedAt:   mandateCreatedAt,
	}

	// Create intent object for validation
	intent := &Intent{
		ID:          intentID,
		MandateID:   mandateID,
		CustomerID:  customerID,
		CartID:      cartID,
		TotalAmount: totalAmount,
		Status:      status,
		CreatedAt:   createdAt,
	}

	// Validate mandate
	valid, message := ValidateMandate(mandate)
	if !valid {
		authorization := CreateAuthorization(req.IntentID, req.MandateID, false, message)

		// Store authorization
		_, err = postgres.DB.Exec(`
			INSERT INTO ap2_authorizations (id, intent_id, mandate_id, authorized, message, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, authorization.ID, authorization.IntentID, authorization.MandateID, authorization.Authorized, authorization.Message, authorization.CreatedAt)

		response := AuthorizeResponse{
			Authorized:      false,
			AuthorizationID: authorization.ID,
			Message:         message,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Validate intent amount
	valid, message = ValidateIntentAmount(intent, mandate)
	if !valid {
		authorization := CreateAuthorization(req.IntentID, req.MandateID, false, message)

		// Store authorization
		_, err = postgres.DB.Exec(`
			INSERT INTO ap2_authorizations (id, intent_id, mandate_id, authorized, message, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, authorization.ID, authorization.IntentID, authorization.MandateID, authorization.Authorized, authorization.Message, authorization.CreatedAt)

		response := AuthorizeResponse{
			Authorized:      false,
			AuthorizationID: authorization.ID,
			Message:         message,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Authorize the payment
	authorization := CreateAuthorization(req.IntentID, req.MandateID, true, "Payment authorized")

	// Store authorization
	_, err = postgres.DB.Exec(`
		INSERT INTO ap2_authorizations (id, intent_id, mandate_id, authorized, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, authorization.ID, authorization.IntentID, authorization.MandateID, authorization.Authorized, authorization.Message, authorization.CreatedAt)

	if err != nil {
		log.Printf("[AP2] Failed to insert authorization: %v", err)
		http.Error(w, "failed to create authorization", http.StatusInternalServerError)
		return
	}

	// Update intent status
	_, err = postgres.DB.Exec(`
		UPDATE ap2_intents
		SET status = $1
		WHERE id = $2
	`, IntentStatusAuthorized, intent.ID)

	if err != nil {
		log.Printf("[AP2] Failed to update intent status: %v", err)
	}

	response := AuthorizeResponse{
		Authorized:      true,
		AuthorizationID: authorization.ID,
		Message:         "Payment authorized",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleExecute handles POST /ap2/execute
func HandleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	// Get authorization
	var authID, intentID, mandateID, message string
	var authorized bool
	var createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, intent_id, mandate_id, authorized, message, created_at
		FROM ap2_authorizations
		WHERE id = $1
	`, req.AuthorizationID).Scan(&authID, &intentID, &mandateID, &authorized, &message, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "authorization not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get authorization: %v", err)
		http.Error(w, "failed to get authorization", http.StatusInternalServerError)
		return
	}

	if !authorized {
		http.Error(w, "authorization not approved", http.StatusBadRequest)
		return
	}

	// Get intent
	var intentIDResult, mandateIDResult, customerID, cartID, status string
	var totalAmount float64
	var intentCreatedAt time.Time

	err = postgres.DB.QueryRow(`
		SELECT id, mandate_id, customer_id, cart_id, total_amount, status, created_at
		FROM ap2_intents
		WHERE id = $1
	`, req.IntentID).Scan(&intentIDResult, &mandateIDResult, &customerID, &cartID, &totalAmount, &status, &intentCreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "intent not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get intent: %v", err)
		http.Error(w, "failed to get intent", http.StatusInternalServerError)
		return
	}

	// Generate order ID and payment ID
	orderID := "order-" + uuid.New().String()[:8]
	paymentID := "payment-" + uuid.New().String()[:8]

	// Create Xendit invoice (simplified - would call actual Xendit API)
	invoiceURL := fmt.Sprintf("https://checkout.xendit.co/web/%s", paymentID)

	// Create execution result
	execution := CreateExecutionResult(intentIDResult, req.AuthorizationID, orderID, paymentID, ExecutionStatusPending, invoiceURL)

	// Store in database
	_, err = postgres.DB.Exec(`
		INSERT INTO ap2_executions (id, intent_id, authorization_id, order_id, payment_id, status, invoice_url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, execution.ID, execution.IntentID, execution.AuthorizationID, execution.OrderID, execution.PaymentID, execution.Status, execution.InvoiceURL, execution.CreatedAt)

	if err != nil {
		log.Printf("[AP2] Failed to insert execution: %v", err)
		http.Error(w, "failed to create execution", http.StatusInternalServerError)
		return
	}

	// Update intent status
	_, err = postgres.DB.Exec(`
		UPDATE ap2_intents
		SET status = $1
		WHERE id = $2
	`, IntentStatusExecuted, intentIDResult)

	if err != nil {
		log.Printf("[AP2] Failed to update intent status: %v", err)
	}

	response := ExecuteResponse{
		ExecutionID: execution.ID,
		Status:      execution.Status,
		InvoiceURL:  execution.InvoiceURL,
		PaymentID:   execution.PaymentID,
		OrderID:     execution.OrderID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleRefund handles POST /ap2/refunds
func HandleRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	// Get execution
	var execID, intentID, authorizationID, orderID, paymentID, status, invoiceURL string
	var createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, intent_id, authorization_id, order_id, payment_id, status, invoice_url, created_at
		FROM ap2_executions
		WHERE id = $1
	`, req.ExecutionID).Scan(&execID, &intentID, &authorizationID, &orderID, &paymentID, &status, &invoiceURL, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "execution not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get execution: %v", err)
		http.Error(w, "failed to get execution", http.StatusInternalServerError)
		return
	}

	if status != ExecutionStatusCompleted {
		http.Error(w, "can only refund completed payments", http.StatusBadRequest)
		return
	}

	// Create refund
	refund := CreateRefund(req.ExecutionID, req.Amount, req.Reason)

	// Store in database
	_, err = postgres.DB.Exec(`
		INSERT INTO ap2_refunds (id, execution_id, amount, reason, status, refund_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, refund.ID, refund.ExecutionID, refund.Amount, refund.Reason, refund.Status, refund.RefundID, refund.CreatedAt)

	if err != nil {
		log.Printf("[AP2] Failed to insert refund: %v", err)
		http.Error(w, "failed to create refund", http.StatusInternalServerError)
		return
	}

	// Update execution status
	_, err = postgres.DB.Exec(`
		UPDATE ap2_executions
		SET status = $1
		WHERE id = $2
	`, ExecutionStatusRefunded, execID)

	if err != nil {
		log.Printf("[AP2] Failed to update execution status: %v", err)
	}

	response := RefundResponse{
		RefundID: refund.ID,
		Status:   refund.Status,
		Message:  "Refund processed successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleGetRefund handles GET /ap2/refunds/{id}
func HandleGetRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract refund ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/ap2/refunds/")
	if path == "" {
		http.Error(w, "refund ID required", http.StatusBadRequest)
		return
	}

	if postgres.DB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	var id, executionID, reason, status, refundID string
	var amount float64
	var createdAt time.Time

	err := postgres.DB.QueryRow(`
		SELECT id, execution_id, amount, reason, status, refund_id, created_at
		FROM ap2_refunds
		WHERE id = $1
	`, path).Scan(&id, &executionID, &amount, &reason, &status, &refundID, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "refund not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Failed to get refund: %v", err)
		http.Error(w, "failed to get refund", http.StatusInternalServerError)
		return
	}

	refund := &Refund{
		ID:          id,
		ExecutionID: executionID,
		Amount:      amount,
		Reason:      reason,
		Status:      status,
		RefundID:    refundID,
		CreatedAt:   createdAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(refund)
}
