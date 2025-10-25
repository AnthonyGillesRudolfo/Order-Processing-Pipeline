package bdd

import (
    "context"
	"fmt"
	"math"
	"strconv"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	postgresdb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/mocks"
	"github.com/stretchr/testify/mock"
)

func (w *PipelineWorld) registerPaymentSteps(sc *godog.ScenarioContext) {
	sc.Step(`^order "([^"]+)" exists for customer "([^"]+)" totaling ([\d\.]+)$`, w.ensureOrderRecord)
	sc.Step(`^a payment request for order "([^"]+)" with amount ([\d\.]+)$`, w.preparePaymentRequest)
	sc.Step(`^the payment object processes the request$`, w.runPaymentObject)
	sc.Step(`^the payment record is persisted with status "([^"]+)"$`, w.assertPaymentRecord)
	sc.Step(`^the payment response includes an invoice link$`, w.assertPaymentInvoice)
}

func (w *PipelineWorld) ensureOrderRecord(orderID, customerID, amountStr string) error {
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	if err := postgresdb.InsertOrder(context.Background(), orderID, customerID, "", orderpb.OrderStatus_PENDING, amount); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func (w *PipelineWorld) preparePaymentRequest(orderID, amountStr string) error {
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	w.paymentReq = &orderpb.ProcessPaymentRequest{
		OrderId: orderID,
		Amount:  amount,
	}
	w.paymentKey = uuid.New().String()
	return nil
}

func (w *PipelineWorld) runPaymentObject() error {
	if w.paymentReq == nil {
		return fmt.Errorf("payment request not prepared")
	}

	mockCtx := mocks.NewMockContext(w.t)

	mockCtx.EXPECT().Key().Return(w.paymentKey)
	mockCtx.EXPECT().Get("status", mock.Anything).Return(false, nil)

	w.interceptRun(mockCtx)

	invoiceURL := fmt.Sprintf("https://example.test/invoices/%s", w.paymentKey)
	mockCtx.EXPECT().Set("status", orderpb.PaymentStatus_PAYMENT_PENDING)
	mockCtx.EXPECT().Set("order_id", w.paymentReq.OrderId)
	mockCtx.EXPECT().Set("amount", w.paymentReq.Amount)
	mockCtx.EXPECT().Set("invoice_url", invoiceURL)
	mockCtx.EXPECT().Set("xendit_invoice_id", mock.Anything).Maybe()

	resp, err := payment.ProcessPayment(restate.WithMockContext(mockCtx), w.paymentReq)
	w.paymentRes = resp
	w.paymentErr = err
	if err == nil {
		// debug log from DB
		var status string
		var amount float64
		var invoiceURL string
		_ = postgresdb.DB.QueryRow(`SELECT status, amount, invoice_url FROM payments WHERE id = $1`, w.paymentKey).Scan(&status, &amount, &invoiceURL)
		w.debugf("payment ok: payment_id=%s status=%s amount=%.2f invoice_url=%s", w.paymentKey, status, amount, invoiceURL)
	}
	return err
}

func (w *PipelineWorld) assertPaymentRecord(expectedStatus string) error {
	if w.paymentErr != nil {
		return fmt.Errorf("payment failed: %w", w.paymentErr)
	}

	var status string
	var amount float64
	var invoiceURL string

	err := postgresdb.DB.QueryRow(`
		SELECT status, amount, invoice_url FROM payments WHERE id = $1
	`, w.paymentKey).Scan(&status, &amount, &invoiceURL)
	if err != nil {
		return fmt.Errorf("query payment: %w", err)
	}

	if status != expectedStatus {
		return fmt.Errorf("expected status %s got %s", expectedStatus, status)
	}
	if math.Abs(amount-w.paymentReq.Amount) > 0.0001 {
		return fmt.Errorf("expected amount %.2f got %.2f", w.paymentReq.Amount, amount)
	}
	if invoiceURL == "" {
		return fmt.Errorf("expected invoice URL to be set")
	}
	return nil
}

func (w *PipelineWorld) assertPaymentInvoice() error {
	if w.paymentRes == nil {
		return fmt.Errorf("payment response missing")
	}
	if w.paymentRes.InvoiceUrl == "" {
		return fmt.Errorf("expected invoice URL in response")
	}
	if w.paymentRes.PaymentId != w.paymentKey {
		return fmt.Errorf("expected payment id %s got %s", w.paymentKey, w.paymentRes.PaymentId)
	}
	return nil
}
