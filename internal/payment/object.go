package payment

import (
	"fmt"
	"log"
	"math/rand"
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
		log.Printf("[Payment Object %s] Payment already processed with status: %v", paymentId, status)
		return &orderpb.ProcessPaymentResponse{PaymentId: paymentId, Status: status}, nil
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
		log.Printf("[Payment Object %s] Creating payment record in database", paymentId)
		return nil, postgres.InsertPayment(paymentId, req.OrderId, req.Amount, paymentMethod, orderpb.PaymentStatus_PAYMENT_PROCESSING)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to create payment record: %v", paymentId, err)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Starting payment transaction processing...", paymentId)
		log.Printf("[Payment Object %s] Simulating payment gateway call (5 second delay)", paymentId)
		time.Sleep(5 * time.Second)
		randomValue := rand.Intn(100)
		if randomValue < 40 {
			log.Printf("[Payment Object %s] ❌ Payment attempt FAILED (random=%d < 40) - Restate will retry", paymentId, randomValue)
			return nil, fmt.Errorf("payment gateway error: transaction declined (simulated failure)")
		}
		log.Printf("[Payment Object %s] ✅ Payment attempt SUCCEEDED (random=%d >= 40)", paymentId, randomValue)
		return nil, nil
	})
	if err != nil {
		status = orderpb.PaymentStatus_PAYMENT_FAILED
		restate.Set(ctx, "status", status)
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) { return nil, postgres.UpdatePaymentStatus(paymentId, status) })
		log.Printf("[Payment Object %s] Payment processing failed after all retries: %v", paymentId, err)
		return &orderpb.ProcessPaymentResponse{PaymentId: paymentId, Status: status}, fmt.Errorf("payment processing failed: %w", err)
	}

	status = orderpb.PaymentStatus_PAYMENT_COMPLETED
	restate.Set(ctx, "status", status)
	restate.Set(ctx, "order_id", req.OrderId)
	restate.Set(ctx, "amount", req.Amount)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) { return nil, postgres.UpdatePaymentStatus(paymentId, status) })
	if err != nil { log.Printf("[Payment Object %s] Warning: Failed to update payment status in database: %v", paymentId, err) }
	log.Printf("[Payment Object %s] Payment completed successfully", paymentId)
	return &orderpb.ProcessPaymentResponse{PaymentId: paymentId, Status: status}, nil
}
