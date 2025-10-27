package ap2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/google/uuid"
)

// getenv returns the value of the environment variable key if set, otherwise defaultVal
func getenv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// Request/Response types for AP2 handlers
type CreateMandateRequest struct {
	CustomerID  string  `json:"customer_id"`
	Scope       string  `json:"scope"`
	AmountLimit float64 `json:"amount_limit"`
	ExpiresAt   string  `json:"expires_at"`
}

type CreateMandateResponse struct {
	MandateID string `json:"mandate_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type CreateIntentRequest struct {
	MandateID       string          `json:"mandate_id"`
	CustomerID      string          `json:"customer_id"`
	CartID          string          `json:"cart_id"`
	ShippingAddress ShippingAddress `json:"shipping_address"`
}

type CreateIntentResponse struct {
	IntentID    string     `json:"intent_id"`
	TotalAmount float64    `json:"total_amount"`
	Items       []CartItem `json:"items"`
	Status      string     `json:"status"`
}

type AuthorizeRequest struct {
	IntentID  string `json:"intent_id"`
	MandateID string `json:"mandate_id"`
}

type AuthorizeResponse struct {
	Authorized      bool   `json:"authorized"`
	AuthorizationID string `json:"authorization_id"`
	Message         string `json:"message"`
}

type ExecuteRequest struct {
	AuthorizationID string `json:"authorization_id"`
	IntentID        string `json:"intent_id"`
}

// New AP2 response models with camelCase and envelope
type AP2ExecuteResult struct {
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`      // "pending" | "completed" | "failed" | "refunded"
	InvoiceLink string `json:"invoiceLink"` // rename from invoice_url
	PaymentID   string `json:"paymentId"`
	OrderID     string `json:"orderId"`
}

type AP2StatusResult struct {
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`
	InvoiceLink string `json:"invoiceLink"`
	PaymentID   string `json:"paymentId"`
	OrderID     string `json:"orderId"`
	CreatedAt   string `json:"createdAt"`
}

type AP2Envelope[T any] struct {
	Result T `json:"result"`
}

// Legacy response type (keeping for backward compatibility if needed)
type ExecuteResponse struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	InvoiceURL  string `json:"invoice_url"`
	PaymentID   string `json:"payment_id"`
	OrderID     string `json:"order_id"`
}

// Simple working AP2 handlers

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

	mandateID := "mandate-" + time.Now().Format("20060102150405") + "-" + uuid.New().String()[:8]

	// Store mandate in database
	if postgres.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := postgres.DB.ExecContext(ctx, `
			INSERT INTO ap2_mandates (id, customer_id, scope, amount_limit, expires_at, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
		`, mandateID, req.CustomerID, req.Scope, req.AmountLimit, req.ExpiresAt, "active")

		if err != nil {
			log.Printf("[AP2] Failed to store mandate data: %v", err)
		} else {
			log.Printf("[AP2] Successfully stored mandate data: %s", mandateID)
		}
	}

	response := CreateMandateResponse{
		MandateID: mandateID,
		Status:    "active",
		CreatedAt: time.Now().Format("2006-01-02T15:04:05-07:00"),
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

	intentID := "intent-" + time.Now().Format("20060102150405") + "-" + uuid.New().String()[:8]

	// Get cart data from Restate cart service to calculate total amount and items
	var items []CartItem
	var totalAmount float64

	if req.CartID != "" {
		// Get cart items from Restate cart service
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://localhost:8080")
		cartURL := fmt.Sprintf("%s/cart.sv1.CartService/%s/ViewCart", runtimeURL, req.CustomerID)

		cartReq := map[string]interface{}{
			"customer_id": req.CustomerID,
		}
		cartBody, _ := json.Marshal(cartReq)

		cartHttpReq, err := http.NewRequest("POST", cartURL, bytes.NewReader(cartBody))
		if err == nil {
			cartHttpReq.Header.Set("Content-Type", "application/json")

			// Call cart service to get items
			cartClient := &http.Client{Timeout: 10 * time.Second}
			cartResp, err := cartClient.Do(cartHttpReq)
			if err == nil {
				defer cartResp.Body.Close()

				var cartData map[string]interface{}
				if err := json.NewDecoder(cartResp.Body).Decode(&cartData); err == nil {
					// Extract cart items and total amount
					if cartState, ok := cartData["cart_state"].(map[string]interface{}); ok {
						if itemsArray, ok := cartState["items"].([]interface{}); ok {
							for _, item := range itemsArray {
								if itemMap, ok := item.(map[string]interface{}); ok {
									items = append(items, CartItem{
										ProductID: itemMap["product_id"].(string),
										Name:      itemMap["name"].(string),
										Quantity:  int32(itemMap["quantity"].(float64)),
										UnitPrice: itemMap["unit_price"].(float64),
									})
								}
							}
						}
						if total, ok := cartState["total_amount"].(float64); ok {
							totalAmount = total
						}
					}
				}
			}
		}
	}

	log.Printf("[AP2] Intent created with cart data: customer_id=%s, cart_id=%s, items_count=%d, total_amount=%.2f", req.CustomerID, req.CartID, len(items), totalAmount)

	// Store intent data in database
	if postgres.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := postgres.DB.ExecContext(ctx, `
			INSERT INTO ap2_intents (id, mandate_id, customer_id, cart_id, total_amount, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
		`, intentID, req.MandateID, req.CustomerID, req.CartID, totalAmount, "created")

		if err != nil {
			log.Printf("[AP2] Failed to store intent data: %v", err)
		} else {
			log.Printf("[AP2] Successfully stored intent data: %s", intentID)
		}
	}

	response := CreateIntentResponse{
		IntentID:    intentID,
		TotalAmount: totalAmount,
		Items:       items,
		Status:      "created",
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

	authorizationID := "auth-" + time.Now().Format("20060102150405") + "-" + uuid.New().String()[:8]

	// Store authorization in database
	if postgres.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := postgres.DB.ExecContext(ctx, `
			INSERT INTO ap2_authorizations (id, intent_id, mandate_id, authorized, message, created_at)
			VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
		`, authorizationID, req.IntentID, req.MandateID, true, "Payment authorized")

		if err != nil {
			log.Printf("[AP2] Failed to store authorization data: %v", err)
		} else {
			log.Printf("[AP2] Successfully stored authorization data: %s", authorizationID)
		}
	}

	response := AuthorizeResponse{
		Authorized:      true,
		AuthorizationID: authorizationID,
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

	log.Printf("[AP2] Execute request received for authorization: %s, intent: %s", req.AuthorizationID, req.IntentID)

	// Generate execution ID for tracking
	executionID := "exec-" + uuid.New().String()[:8]
	orderID := "order-" + uuid.New().String()[:8]

	log.Printf("[AP2] Starting checkout workflow for order: %s", orderID)

	// Retrieve intent data from database to get customer and cart information
	var customerID, cartID string
	var totalAmount float64
	if postgres.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := postgres.DB.QueryRowContext(ctx, `
			SELECT customer_id, cart_id, total_amount 
			FROM ap2_intents 
			WHERE id = $1
		`, req.IntentID).Scan(&customerID, &cartID, &totalAmount)

		if err != nil {
			log.Printf("[AP2] Failed to retrieve intent data for intent_id %s: %v", req.IntentID, err)
			http.Error(w, "intent not found", http.StatusNotFound)
			return
		}
		log.Printf("[AP2] Retrieved intent data: customer_id=%s, cart_id=%s, total_amount=%.2f", customerID, cartID, totalAmount)
	} else {
		log.Printf("[AP2] Database not available, using fallback values")
		customerID = "customer-001"
		cartID = "cart-001"
		totalAmount = 0.0
	}

	// Get cart items from Restate cart service
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://localhost:8080")
	cartURL := fmt.Sprintf("%s/cart.sv1.CartService/%s/ViewCart", runtimeURL, customerID)

	cartReq := map[string]interface{}{
		"customer_id": customerID,
	}
	cartBody, _ := json.Marshal(cartReq)

	cartHttpReq, err := http.NewRequest("POST", cartURL, bytes.NewReader(cartBody))
	if err != nil {
		log.Printf("[AP2] Failed to create cart request: %v", err)
		http.Error(w, "failed to retrieve cart data", http.StatusInternalServerError)
		return
	}
	cartHttpReq.Header.Set("Content-Type", "application/json")

	// Call cart service to get items
	cartClient := &http.Client{Timeout: 10 * time.Second}
	cartResp, err := cartClient.Do(cartHttpReq)
	if err != nil {
		log.Printf("[AP2] Failed to call cart service: %v", err)
		http.Error(w, "failed to retrieve cart data", http.StatusInternalServerError)
		return
	}
	defer cartResp.Body.Close()

	var cartData map[string]interface{}
	if err := json.NewDecoder(cartResp.Body).Decode(&cartData); err != nil {
		log.Printf("[AP2] Failed to decode cart response: %v", err)
		http.Error(w, "failed to parse cart data", http.StatusInternalServerError)
		return
	}

	// Extract cart items and merchant ID
	var items []map[string]interface{}
	var merchantID string

	if cartState, ok := cartData["cart_state"].(map[string]interface{}); ok {
		if itemsArray, ok := cartState["items"].([]interface{}); ok {
			for _, item := range itemsArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					items = append(items, map[string]interface{}{
						"product_id": itemMap["product_id"],
						"quantity":   itemMap["quantity"],
					})
				}
			}
		}
		if merchant, ok := cartState["merchant_id"].(string); ok {
			merchantID = merchant
		}
	}

	log.Printf("[AP2] Retrieved cart data: merchant_id=%s, items_count=%d", merchantID, len(items))

	// Call the Checkout workflow via Restate runtime with real data
	checkoutReq := map[string]interface{}{
		"customer_id": customerID,
		"merchant_id": merchantID,
		"items":       items,
	}

	checkoutBody, _ := json.Marshal(checkoutReq)
	checkoutURL := fmt.Sprintf("%s/order.sv1.OrderService/%s/Checkout", runtimeURL, orderID)

	httpReq, err := http.NewRequest("POST", checkoutURL, bytes.NewReader(checkoutBody))
	if err != nil {
		log.Printf("[AP2] Failed to create checkout request: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Call the Checkout workflow - this should return immediately
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[AP2] Failed to call checkout workflow: %v", err)
		http.Error(w, "failed to process checkout", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[AP2] Checkout workflow failed with status: %d", resp.StatusCode)
		http.Error(w, "checkout failed", http.StatusInternalServerError)
		return
	}

	// Parse the checkout response
	var checkoutResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&checkoutResp); err != nil {
		log.Printf("[AP2] Failed to decode checkout response: %v", err)
		http.Error(w, "failed to process checkout response", http.StatusInternalServerError)
		return
	}

	// Extract values from checkout response
	paymentID, _ := checkoutResp["paymentId"].(string)
	invoiceURL, _ := checkoutResp["invoiceLink"].(string)
	orderIDFromResp, _ := checkoutResp["orderId"].(string)
	status, _ := checkoutResp["status"].(string)

	if orderIDFromResp != "" {
		orderID = orderIDFromResp
	}

	log.Printf("[AP2] Checkout workflow completed: order_id=%s, payment_id=%s, invoice_url=%s", orderID, paymentID, invoiceURL)

	// Best-effort: clear the user's cart after order creation
	if customerID != "" {
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://localhost:8080")
		clearURL := fmt.Sprintf("%s/cart.sv1.CartService/%s/ClearCart", runtimeURL, customerID)
		clearReq := map[string]any{"customer_id": customerID}
		b, _ := json.Marshal(clearReq)
		if resp2, err := http.Post(clearURL, "application/json", bytes.NewReader(b)); err != nil {
			log.Printf("[AP2] warning: failed to clear cart for %s: %v", customerID, err)
		} else {
			defer resp2.Body.Close()
			if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
				log.Printf("[AP2] warning: clear cart returned status %d for %s", resp2.StatusCode, customerID)
			}
		}
	}

	// Store the execution record in the database so we can track it
	if postgres.DB != nil {
		// Test database connection before attempting insert
		if err := postgres.DB.Ping(); err != nil {
			log.Printf("[AP2] Database connection test failed for insert: %v", err)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := postgres.DB.ExecContext(ctx, `
				INSERT INTO ap2_executions (id, authorization_id, intent_id, order_id, payment_id, status, invoice_url, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
			`, executionID, req.AuthorizationID, req.IntentID, orderID, paymentID, strings.ToLower(status), invoiceURL)

			if err != nil {
				log.Printf("[AP2] Failed to store execution record: %v", err)
			} else {
				log.Printf("[AP2] Successfully stored execution record: %s", executionID)
			}
		}
	}

	log.Printf("[AP2] Execute response: execution_id=%s, order_id=%s, payment_id=%s, invoice_url=%s", executionID, orderID, paymentID, invoiceURL)

	// Return immediately with JSON envelope and camelCase keys - no waiting
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AP2Envelope[AP2ExecuteResult]{
		Result: AP2ExecuteResult{
			ExecutionID: executionID,
			Status:      strings.ToLower(status), // ensure lowercase
			InvoiceLink: invoiceURL,              // was invoice_url
			PaymentID:   paymentID,
			OrderID:     orderID,
		},
	})
}

// HandleGetStatus handles GET /ap2/status/{execution_id}
func HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract execution ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/ap2/status/")
	executionID := path

	// Try to find the execution record by execution ID
	var orderID, paymentID, status string
	var createdAt time.Time

	if postgres.DB != nil {
		// Test database connection first
		if err := postgres.DB.Ping(); err != nil {
			log.Printf("[AP2] Database connection test failed for status: %v", err)
			// Fallback to generated response
			orderID = "order-" + uuid.New().String()[:8]
			paymentID = "payment-" + uuid.New().String()[:8]
			status = "pending"
			createdAt = time.Now()
		} else {
			// Look for execution record in ap2_executions table
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			row := postgres.DB.QueryRowContext(ctx, `
				SELECT order_id, payment_id, status, created_at
				FROM ap2_executions
				WHERE id = $1
			`, executionID)

			if err := row.Scan(&orderID, &paymentID, &status, &createdAt); err != nil {
				log.Printf("[AP2] Failed to get execution status: %v", err)
				// Fallback to generated response
				orderID = "order-" + uuid.New().String()[:8]
				paymentID = "payment-" + uuid.New().String()[:8]
				status = "pending"
				createdAt = time.Now()
			} else {
				log.Printf("[AP2] Found execution record: order_id=%s, status=%s", orderID, status)
			}
		}
	} else {
		// Fallback if no database
		orderID = "order-" + uuid.New().String()[:8]
		paymentID = "payment-" + uuid.New().String()[:8]
		status = "pending"
		createdAt = time.Now()
	}

	// Fetch actual payment data from database
	var invoiceURL string
	var actualPaymentID string
	if postgres.DB != nil && orderID != "" {
		payment, err := postgres.GetPaymentByOrderID(orderID)
		if err != nil {
			log.Printf("[AP2] warning: failed to load payment for order %s: %v", orderID, err)
			// Use execution record values as fallback
			invoiceURL = ""
			actualPaymentID = paymentID
		} else {
			invoiceURL = payment.InvoiceURL
			actualPaymentID = payment.ID
			// Update status from payment if available
			if payment.Status != "" {
				status = strings.ToLower(payment.Status)
			}
			log.Printf("[AP2] Found payment data: payment_id=%s, invoice_url=%s, status=%s", payment.ID, payment.InvoiceURL, payment.Status)
		}
	} else {
		invoiceURL = ""
		actualPaymentID = paymentID
	}

	// Return 200 with JSON envelope and camelCase keys
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AP2Envelope[AP2StatusResult]{
		Result: AP2StatusResult{
			ExecutionID: executionID,
			Status:      strings.ToLower(status), // ensure lowercase
			InvoiceLink: invoiceURL,
			PaymentID:   actualPaymentID,
			OrderID:     orderID,
			CreatedAt:   createdAt.Format(time.RFC3339),
		},
	})
}

// Placeholder handlers for other endpoints
func HandleGetMandate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func HandleRefund(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func HandleGetRefund(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
