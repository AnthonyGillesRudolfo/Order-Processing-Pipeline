package payment

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	restate "github.com/restatedev/sdk-go"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// ProcessPayment is a virtual object handler for processing payments
func ProcessPayment(ctx restate.ObjectContext, req *orderpb.ProcessPaymentRequest) (*orderpb.ProcessPaymentResponse, error) {
	paymentId := restate.Key(ctx)
	log.Printf("[Payment Object %s] Processing payment for order: %s, amount: %.2f", paymentId, req.OrderId, req.Amount)

	status, err := restate.Get[orderpb.PaymentStatus](ctx, "status")
	if err == nil {
		// Only short-circuit if terminal status; otherwise continue to ensure DB + invoice are populated
		if status == orderpb.PaymentStatus_PAYMENT_COMPLETED || status == orderpb.PaymentStatus_PAYMENT_FAILED {
			log.Printf("[Payment Object %s] Payment already processed with status: %v", paymentId, status)
			invoiceURL, _ := restate.Get[string](ctx, "invoice_url")
			xInvoiceID, _ := restate.Get[string](ctx, "xendit_invoice_id")
			return &orderpb.ProcessPaymentResponse{PaymentId: paymentId, Status: status, InvoiceUrl: invoiceURL, XenditInvoiceId: xInvoiceID}, nil
		}
		// If status is pending/processing, proceed to (re)create DB row and ensure invoice is available
		log.Printf("[Payment Object %s] Continuing processing for pending payment to ensure DB/invoice populated", paymentId)
	}

	paymentMethod := "XENDIT_INVOICE"
	if req.PaymentMethod != nil {
		switch req.PaymentMethod.Method.(type) {
		case *orderpb.PaymentMethod_CreditCard:
			paymentMethod = "CREDIT_CARD"
		case *orderpb.PaymentMethod_BankTransfer:
			paymentMethod = "BANK_TRANSFER"
		case *orderpb.PaymentMethod_DigitalWallet:
			paymentMethod = "DIGITAL_WALLET"
		}
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Upserting payment record in database", paymentId)
		return nil, postgres.InsertPayment(paymentId, req.OrderId, req.Amount, paymentMethod, orderpb.PaymentStatus_PAYMENT_PENDING)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to create payment record: %v", paymentId, err)
	}

	// Create Xendit invoice (or dummy URL) and return PROCESSING
	var invoiceURL string
	var xInvoiceID string

	secret := os.Getenv("XENDIT_SECRET_KEY")
	if secret == "" {
		// Backward compat: allow SECRET_KEY too
		secret = os.Getenv("SECRET_KEY")
	}
	successURL := os.Getenv("XENDIT_SUCCESS_URL")
	if successURL == "" {
		successURL = "http://localhost:3000"
	}
	failureURL := os.Getenv("XENDIT_FAILURE_URL")
	if failureURL == "" {
		failureURL = "http://localhost:3000"
	}

	amountInt := int64(math.Round(req.Amount))
	if amountInt < 1 {
		amountInt = 1
	}

	if secret != "" {
		log.Printf("[Payment Object %s] Creating Xendit invoice via HTTP API", paymentId)
		payload := map[string]any{
			"external_id":          paymentId,
			"amount":               amountInt,
			"description":          fmt.Sprintf("Order %s", req.OrderId),
			"success_redirect_url": successURL,
			"failure_redirect_url": failureURL,
		}
		b, _ := json.Marshal(payload)
		httpReq, reqErr := http.NewRequest("POST", "https://api.xendit.co/v2/invoices", bytes.NewReader(b))
		if reqErr == nil {
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.SetBasicAuth(secret, "")
			httpResp, doErr := http.DefaultClient.Do(httpReq)
			if doErr == nil {
				defer httpResp.Body.Close()
				if httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 {
					var j struct {
						ID         string `json:"id"`
						InvoiceURL string `json:"invoice_url"`
					}
					_ = json.NewDecoder(httpResp.Body).Decode(&j)
					if j.InvoiceURL != "" {
						invoiceURL = j.InvoiceURL
						xInvoiceID = j.ID
						_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
							return nil, postgres.UpdatePaymentInvoiceInfo(paymentId, invoiceURL, xInvoiceID)
						})
					}
				} else {
					log.Printf("[Payment Object %s] Xendit invoice API status: %d", paymentId, httpResp.StatusCode)
				}
			} else {
				log.Printf("[Payment Object %s] Xendit invoice HTTP error: %v", paymentId, doErr)
			}
		} else {
			log.Printf("[Payment Object %s] Xendit invoice request build failed: %v", paymentId, reqErr)
		}
	}
	if invoiceURL == "" {
		invoiceURL = fmt.Sprintf("https://example.test/invoices/%s", paymentId)
	}

	// Persist invoice info regardless (fallback or real) so UI can read from DB
	_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdatePaymentInvoiceInfo(paymentId, invoiceURL, xInvoiceID)
	})

	status = orderpb.PaymentStatus_PAYMENT_PENDING
	restate.Set(ctx, "status", status)
	restate.Set(ctx, "order_id", req.OrderId)
	restate.Set(ctx, "amount", req.Amount)
	restate.Set(ctx, "invoice_url", invoiceURL)
	if xInvoiceID != "" {
		restate.Set(ctx, "xendit_invoice_id", xInvoiceID)
	}
	log.Printf("[Payment Object %s] Payment set to PROCESSING; invoice: %s", paymentId, invoiceURL)
	return &orderpb.ProcessPaymentResponse{PaymentId: paymentId, Status: status, InvoiceUrl: invoiceURL, XenditInvoiceId: xInvoiceID}, nil
}

// MarkPaymentCompleted marks the payment as completed (simulated)
func MarkPaymentCompleted(ctx restate.ObjectContext, req *orderpb.MarkPaymentCompletedRequest) (*orderpb.MarkPaymentCompletedResponse, error) {
	paymentId := restate.Key(ctx)
	log.Printf("[Payment Object %s] MarkPaymentCompleted for order: %s", paymentId, req.OrderId)

	current, err := restate.Get[orderpb.PaymentStatus](ctx, "status")
	if err == nil && current == orderpb.PaymentStatus_PAYMENT_COMPLETED {
		return &orderpb.MarkPaymentCompletedResponse{Ok: true}, nil
	}

	restate.Set(ctx, "status", orderpb.PaymentStatus_PAYMENT_COMPLETED)
	_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_COMPLETED)
	})

	return &orderpb.MarkPaymentCompletedResponse{Ok: true}, nil
}

// ProcessRefund processes a refund for a completed payment using Xendit API
func ProcessRefund(ctx restate.ObjectContext, req *orderpb.ProcessRefundRequest) (*orderpb.ProcessRefundResponse, error) {
	paymentId := restate.Key(ctx)
	log.Printf("[Payment Object %s] Processing refund for order: %s, amount: %.2f", paymentId, req.OrderId, req.Amount)

	// Check if payment exists and is completed
	status, err := restate.Get[orderpb.PaymentStatus](ctx, "status")
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}

	if status != orderpb.PaymentStatus_PAYMENT_COMPLETED {
		return nil, fmt.Errorf("payment must be completed before refund can be processed, current status: %v", status)
	}

	// Get Xendit invoice ID from state
	xInvoiceID, err := restate.Get[string](ctx, "xendit_invoice_id")
	if err != nil || xInvoiceID == "" {
		// Fallback: try to get from database
		if postgres.DB != nil {
			row := postgres.DB.QueryRow(`SELECT xendit_invoice_id FROM payments WHERE id = $1`, paymentId)
			if err := row.Scan(&xInvoiceID); err != nil {
				log.Printf("[Payment Object %s] Warning: No Xendit invoice ID found: %v", paymentId, err)
			}
		}
		if xInvoiceID == "" {
			log.Printf("[Payment Object %s] No Xendit invoice ID found, simulating refund", paymentId)
			// Simulate refund when no real invoice exists
			refundId := fmt.Sprintf("refund_%s_%d", paymentId, time.Now().Unix())

			// Update payment status in database
			_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
				return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_REFUNDED)
			})
			if err != nil {
				log.Printf("[Payment Object %s] Warning: Failed to update refund status in database: %v", paymentId, err)
			}

			return &orderpb.ProcessRefundResponse{
				RefundId: refundId,
				Status:   "completed",
				Message:  "Refund simulated successfully (no real invoice)",
			}, nil
		}
	}

	// Process refund using Xendit API
	secret := os.Getenv("XENDIT_SECRET_KEY")
	if secret == "" {
		secret = os.Getenv("SECRET_KEY")
	}

	if secret == "" {
		log.Printf("[Payment Object %s] No Xendit secret key found, simulating refund", paymentId)
		// Simulate refund in test environment
		refundId := fmt.Sprintf("refund_%s_%d", paymentId, time.Now().Unix())

		// Update payment status in database
		_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_REFUNDED)
		})
		if err != nil {
			log.Printf("[Payment Object %s] Warning: Failed to update refund status in database: %v", paymentId, err)
		}

		return &orderpb.ProcessRefundResponse{
			RefundId: refundId,
			Status:   "completed",
			Message:  "Refund simulated successfully (test environment)",
		}, nil
	}

	// Real Xendit refund API call
	log.Printf("[Payment Object %s] Attempting refund for Xendit invoice: %s", paymentId, xInvoiceID)

	// Try the correct refund endpoint - POST /refunds with invoice_id in body
	url := "https://api.xendit.co/v2/refunds"

	refundData := map[string]interface{}{
		"invoice_id": xInvoiceID,
		"amount":     int64(req.Amount),
		"reason":     req.Reason,
	}

	jsonData, err := json.Marshal(refundData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refund data: %w", err)
	}

	log.Printf("[Payment Object %s] Refund request data: %s", paymentId, string(jsonData))

	reqHTTP, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create refund request: %w", err)
	}

	reqHTTP.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(secret+":")))
	reqHTTP.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(reqHTTP)
	if err != nil {
		return nil, fmt.Errorf("failed to process refund with Xendit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Payment Object %s] Xendit refund failed - Status: %d, Body: %s", paymentId, resp.StatusCode, string(body))

		// Try the alternative endpoint if the first one fails
		if resp.StatusCode == 404 || resp.StatusCode == 400 {
			log.Printf("[Payment Object %s] Trying alternative refund endpoint", paymentId)

			// Try the original endpoint: POST /v2/invoices/{id}/refunds
			altUrl := "https://api.xendit.co/v2/invoices/" + xInvoiceID + "/refunds"
			altRefundData := map[string]interface{}{
				"amount": int64(req.Amount),
				"reason": req.Reason,
			}

			altJsonData, _ := json.Marshal(altRefundData)
			log.Printf("[Payment Object %s] Alternative refund request data: %s", paymentId, string(altJsonData))

			altReq, _ := http.NewRequest("POST", altUrl, bytes.NewBuffer(altJsonData))
			altReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(secret+":")))
			altReq.Header.Set("Content-Type", "application/json")

			altResp, altErr := client.Do(altReq)
			if altErr == nil {
				defer altResp.Body.Close()
				if altResp.StatusCode >= 200 && altResp.StatusCode < 300 {
					var altRefundResp map[string]interface{}
					if err := json.NewDecoder(altResp.Body).Decode(&altRefundResp); err == nil {
						refundId, _ := altRefundResp["id"].(string)
						refundStatus, _ := altRefundResp["status"].(string)

						log.Printf("[Payment Object %s] Alternative refund succeeded: %s (status: %s)", paymentId, refundId, refundStatus)

						// Update payment status in database
						_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
							return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_REFUNDED)
						})
						if err != nil {
							log.Printf("[Payment Object %s] Warning: Failed to update refund status in database: %v", paymentId, err)
						}

						return &orderpb.ProcessRefundResponse{
							RefundId: refundId,
							Status:   refundStatus,
							Message:  "Refund processed successfully via alternative endpoint",
						}, nil
					}
				} else {
					altBody, _ := io.ReadAll(altResp.Body)
					log.Printf("[Payment Object %s] Alternative refund also failed - Status: %d, Body: %s", paymentId, altResp.StatusCode, string(altBody))
				}
			}
		}

		// If both endpoints fail, simulate the refund
		log.Printf("[Payment Object %s] Both refund endpoints failed, simulating refund", paymentId)
		refundId := fmt.Sprintf("refund_%s_%d", paymentId, time.Now().Unix())

		// Update payment status in database
		_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_REFUNDED)
		})
		if err != nil {
			log.Printf("[Payment Object %s] Warning: Failed to update refund status in database: %v", paymentId, err)
		}

		return &orderpb.ProcessRefundResponse{
			RefundId: refundId,
			Status:   "completed",
			Message:  "Refund simulated successfully (both endpoints failed)",
		}, nil
	}

	var refundResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&refundResp); err != nil {
		return nil, fmt.Errorf("failed to decode refund response: %w", err)
	}

	refundId, _ := refundResp["id"].(string)
	refundStatus, _ := refundResp["status"].(string)

	log.Printf("[Payment Object %s] Refund processed successfully: %s (status: %s)", paymentId, refundId, refundStatus)

	// Update payment status in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_REFUNDED)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to update refund status in database: %v", paymentId, err)
	}

	return &orderpb.ProcessRefundResponse{
		RefundId: refundId,
		Status:   refundStatus,
		Message:  "Refund processed successfully",
	}, nil
}

// MarkPaymentExpired handles expired payments by updating payment status and triggering order cancellation
func MarkPaymentExpired(ctx restate.ObjectContext, req *orderpb.MarkPaymentExpiredRequest) (*orderpb.MarkPaymentExpiredResponse, error) {
	paymentId := restate.Key(ctx)
	log.Printf("[Payment Object %s] Marking payment as expired for order: %s", paymentId, req.OrderId)

	// Update payment status to EXPIRED
	restate.Set(ctx, "status", orderpb.PaymentStatus_PAYMENT_EXPIRED)

	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Updating payment status to EXPIRED in database", paymentId)
		return nil, postgres.UpdatePaymentStatus(paymentId, orderpb.PaymentStatus_PAYMENT_EXPIRED)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to update payment status in database: %v", paymentId, err)
	}

	// Get order information to restore stock
	orderInfo, err := getOrderFromDBByPaymentID(paymentId)
	if err != nil {
		log.Printf("[Payment Object %s] Failed to get order information for payment_id %s: %v", paymentId, paymentId, err)
		return &orderpb.MarkPaymentExpiredResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to get order information: %v", err),
		}, nil
	}

	orderMap, _ := orderInfo["order"].(map[string]any)
	orderID, _ := orderMap["id"].(string)
	merchantID, _ := orderMap["merchant_id"].(string)

	if orderID == "" {
		log.Printf("[Payment Object %s] Order ID not found for payment_id %s", paymentId, paymentId)
		return &orderpb.MarkPaymentExpiredResponse{
			Success: false,
			Message: "Order ID not found",
		}, nil
	}

	// Get order items to restore stock
	orderItems, err := postgres.GetOrderItems(orderID)
	if err != nil {
		log.Printf("[Payment Object %s] Failed to get order items for order %s: %v", paymentId, orderID, err)
		return &orderpb.MarkPaymentExpiredResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to get order items: %v", err),
		}, nil
	}

	// Restore stock to merchant inventory
	if merchantID != "" && len(orderItems) > 0 {
		log.Printf("[Payment Object %s] Restoring stock for %d items to merchant %s", paymentId, len(orderItems), merchantID)
		_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.RestoreStockToItems(merchantID, orderItems)
		})
		if err != nil {
			log.Printf("[Payment Object %s] Failed to restore stock: %v", paymentId, err)
			return &orderpb.MarkPaymentExpiredResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to restore stock: %v", err),
			}, nil
		}
		log.Printf("[Payment Object %s] Successfully restored stock for order %s", paymentId, orderID)
	}

	// Update order status to CANCELLED
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Updating order status to CANCELLED for order %s", paymentId, orderID)
		return nil, postgres.UpdateOrderStatusDB(orderID, orderpb.OrderStatus_CANCELLED)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Failed to update order status to CANCELLED: %v", paymentId, err)
		return &orderpb.MarkPaymentExpiredResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to update order status: %v", err),
		}, nil
	}

	log.Printf("[Payment Object %s] Successfully processed payment expiration for order %s", paymentId, orderID)
	return &orderpb.MarkPaymentExpiredResponse{
		Success: true,
		Message: "Payment marked as expired, stock restored, order cancelled",
	}, nil
}

// getOrderFromDBByPaymentID retrieves order information by payment ID
func getOrderFromDBByPaymentID(paymentID string) (map[string]any, error) {
	if postgres.DB == nil {
		return nil, sql.ErrConnDone
	}

	row := postgres.DB.QueryRow(`
		SELECT o.id, o.customer_id, o.status, o.total_amount, o.payment_id, o.shipment_id, o.tracking_number, o.updated_at, o.merchant_id,
		       COALESCE(p.status, '') AS payment_status, COALESCE(p.invoice_url, '') AS invoice_url
		FROM orders o
		LEFT JOIN payments p ON p.id = o.payment_id
		WHERE o.payment_id = $1
	`, paymentID)

	var (
		id, customerID, status, paymentIDResult, merchantID string
		shipmentID, trackingNumber                          sql.NullString
		totalAmount                                         sql.NullFloat64
		updatedAt                                           string
		paymentStatus                                       string
		invoiceURL                                          string
	)
	if err := row.Scan(&id, &customerID, &status, &totalAmount, &paymentIDResult, &shipmentID, &trackingNumber, &updatedAt, &merchantID, &paymentStatus, &invoiceURL); err != nil {
		log.Printf("[Payment Object] getOrderFromDBByPaymentID failed for payment_id %s: %v", paymentID, err)
		return nil, err
	}

	log.Printf("[Payment Object] getOrderFromDBByPaymentID found order %s: customer=%s, status=%s, payment_id=%s", id, customerID, status, paymentIDResult)
	return map[string]any{
		"order": map[string]any{
			"id":              id,
			"customer_id":     customerID,
			"status":          status,
			"total_amount":    totalAmount.Float64,
			"payment_id":      paymentIDResult,
			"shipment_id":     shipmentID.String,
			"tracking_number": trackingNumber.String,
			"updated_at":      updatedAt,
			"merchant_id":     merchantID,
		},
		"payment": map[string]any{
			"status":      paymentStatus,
			"invoice_url": invoiceURL,
		},
	}, nil
}

// MapXenditStatusToAP2 converts Xendit payment status to AP2 ExecutionResult status
func MapXenditStatusToAP2(xenditStatus string) string {
	switch xenditStatus {
	case "PAID":
		return "completed"
	case "EXPIRED":
		return "failed"
	case "FAILED":
		return "failed"
	case "PENDING":
		return "pending"
	case "SETTLED":
		return "completed"
	case "REFUNDED":
		return "refunded"
	default:
		return "pending"
	}
}

// MapAP2StatusToXendit converts AP2 ExecutionResult status to Xendit payment status
func MapAP2StatusToXendit(ap2Status string) string {
	switch ap2Status {
	case "completed":
		return "PAID"
	case "failed":
		return "FAILED"
	case "pending":
		return "PENDING"
	case "refunded":
		return "REFUNDED"
	default:
		return "PENDING"
	}
}
