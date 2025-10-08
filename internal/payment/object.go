package payment

import (
	"bytes"
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

	paymentMethod := "UNKNOWN"
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
			return nil, fmt.Errorf("no Xendit invoice ID found for payment %s", paymentId)
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
	url := "https://api.xendit.co/v2/invoices/" + xInvoiceID + "/refunds"

	refundData := map[string]interface{}{
		"amount": int64(req.Amount),
		"reason": req.Reason,
	}

	jsonData, err := json.Marshal(refundData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refund data: %w", err)
	}

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
		return nil, fmt.Errorf("Xendit refund failed with status %d: %s", resp.StatusCode, string(body))
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
