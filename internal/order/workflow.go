package order

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/merchant/v1"
	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// StockValidationError represents a non-retryable stock validation error
type StockValidationError struct {
	ItemID    string
	Requested int32
	Available int32
	Message   string
}

func (e *StockValidationError) Error() string {
	return e.Message
}

// IsTerminalError indicates this is a terminal error that should not be retried
func (e *StockValidationError) IsTerminalError() bool {
	return true
}

// Checkout orchestrates the order process and returns immediately after creating invoice
func Checkout(ctx restate.WorkflowContext, req *orderpb.CheckoutRequest) (*orderpb.CheckoutResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Starting checkout for customer: %s merchant: %s", orderId, req.CustomerId, req.MerchantId)

	// Step 1: Validate stock availability and calculate total amount
	log.Printf("[Workflow %s] Step 1: Validating stock availability", orderId)
	var totalAmount float64
	for _, item := range req.Items {
		// Get item details from merchant service to calculate proper total
		merchantClient := restate.Object[*merchantpb.Item](ctx, "merchant.sv1.MerchantService", req.MerchantId, "GetItem")

		// Create proper protobuf request using the generated types
		itemReq := &merchantpb.GetItemRequest{
			MerchantId: req.MerchantId,
			ItemId:     item.ProductId,
		}

		itemProto, err := merchantClient.Request(itemReq)
		if err != nil {
			log.Printf("[Workflow %s] Item %s not found: %v", orderId, item.ProductId, err)
			return nil, &StockValidationError{
				ItemID:    item.ProductId,
				Requested: item.Quantity,
				Available: 0,
				Message:   fmt.Sprintf("Item '%s' not found or unavailable", item.ProductId),
			}
		}

		// Check stock availability
		if itemProto.Quantity < item.Quantity {
			return nil, &StockValidationError{
				ItemID:    item.ProductId,
				Requested: item.Quantity,
				Available: itemProto.Quantity,
				Message: fmt.Sprintf("Insufficient stock for item '%s': requested %d, available %d",
					itemProto.Name, item.Quantity, itemProto.Quantity),
			}
		}

		totalAmount += float64(item.Quantity) * itemProto.Price
		log.Printf("[Workflow %s] Item %s validated: price=%.2f, stock=%d, quantity=%d",
			orderId, item.ProductId, itemProto.Price, itemProto.Quantity, item.Quantity)
	}

	log.Printf("[Workflow %s] Stock validation passed, total amount: %.2f", orderId, totalAmount)

	// Step 2: Deduct stock from inventory (using database transaction)
	log.Printf("[Workflow %s] Step 2: Deducting stock from inventory", orderId)
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.DeductStockFromItems(req.MerchantId, req.Items)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to deduct stock: %w", err)
	}

	// Step 3: Create order in database
	log.Printf("[Workflow %s] Step 3: Creating order in database", orderId)
	restate.Set(ctx, "customer_id", req.CustomerId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)
	restate.Set(ctx, "total_amount", totalAmount)
	if req.MerchantId != "" {
		restate.Set(ctx, "merchant_id", req.MerchantId)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Workflow %s] Persisting order and items to database", orderId)
		if err := postgres.InsertOrder(orderId, req.CustomerId, req.MerchantId, orderpb.OrderStatus_PENDING, totalAmount); err != nil {
			return nil, err
		}
		return nil, postgres.InsertOrderItems(orderId, req.MerchantId, req.Items)
	})
	if err != nil {
		// If order creation fails, we need to restore the deducted stock
		log.Printf("[Workflow %s] Order creation failed, restoring stock", orderId)
		_, restoreErr := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.RestoreStockToItems(req.MerchantId, req.Items)
		})
		if restoreErr != nil {
			log.Printf("[Workflow %s] CRITICAL: Failed to restore stock after order creation failure: %v", orderId, restoreErr)
		}
		return nil, fmt.Errorf("failed to persist order to database: %w", err)
	}

	// Use Restate's deterministic random for payment ID to ensure workflow determinism
	paymentId := restate.Rand(ctx).UUID().String()
	paymentReq := &orderpb.ProcessPaymentRequest{
		OrderId: orderId,
		Amount:  totalAmount,
		// No payment method - Xendit invoice handles all payment methods
	}

	log.Printf("[Workflow %s] Step 4: Processing payment", orderId)
	paymentClient := restate.Object[*orderpb.ProcessPaymentResponse](ctx, "order.sv1.PaymentService", paymentId, "ProcessPayment", restate.WithProtoJSON)
	paymentResp, err := paymentClient.Request(paymentReq)
	if err != nil {
		restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED)
		})
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	log.Printf("[Workflow %s] Payment initiated: %s", orderId, paymentResp.PaymentId)
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "payment_status", paymentResp.Status)
	if paymentResp.InvoiceUrl != "" {
		restate.Set(ctx, "invoice_url", paymentResp.InvoiceUrl)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := postgres.UpdateOrderPayment(orderId, paymentResp.PaymentId); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update payment in database: %v", orderId, err)
	}

	// Create an awakeable to wait for payment completion signal later
	log.Printf("[Workflow %s] Creating awakeable for future payment completion", orderId)
	awakeable := restate.Awakeable[any](ctx)
	awakeableId := awakeable.Id()
	restate.Set(ctx, "payment_awakeable_id", awakeableId)

	// Store the awakeable ID in the database so the webhook can access it
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		// Store awakeable ID in orders table
		_, err := postgres.DB.Exec(`UPDATE orders SET awakeable_id = $1 WHERE id = $2`, awakeableId, orderId)
		return nil, err
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to store awakeable ID in database: %v", orderId, err)
	}
	log.Printf("[Workflow %s] Awakeable ID stored: %s", orderId, awakeableId)

	// Store payment and order IDs for webhook lookup
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "order_id", orderId)

	// Return immediately with invoice link - do not await payment completion
	log.Printf("[Workflow %s] Returning immediately with invoice link: %s", orderId, paymentResp.InvoiceUrl)
	return &orderpb.CheckoutResponse{
		OrderId:     orderId,
		PaymentId:   paymentResp.PaymentId,
		InvoiceLink: paymentResp.InvoiceUrl,
		Status:      "pending",
	}, nil
}

// CreateOrder orchestrates the order process (legacy method - kept for backward compatibility)
func CreateOrder(ctx restate.WorkflowContext, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Creating order for customer: %s merchant: %s", orderId, req.CustomerId, req.MerchantId)

	// Step 1: Validate stock availability and calculate total amount
	log.Printf("[Workflow %s] Step 1: Validating stock availability", orderId)
	var totalAmount float64
	for _, item := range req.Items {
		// Get item details from merchant service to calculate proper total
		// We need to use the proper protobuf message type for the request
		merchantClient := restate.Object[*merchantpb.Item](ctx, "merchant.sv1.MerchantService", req.MerchantId, "GetItem")

		// Create proper protobuf request using the generated types
		itemReq := &merchantpb.GetItemRequest{
			MerchantId: req.MerchantId,
			ItemId:     item.ProductId,
		}

		itemProto, err := merchantClient.Request(itemReq)
		if err != nil {
			log.Printf("[Workflow %s] Item %s not found: %v", orderId, item.ProductId, err)
			return nil, &StockValidationError{
				ItemID:    item.ProductId,
				Requested: item.Quantity,
				Available: 0,
				Message:   fmt.Sprintf("Item '%s' not found or unavailable", item.ProductId),
			}
		}

		// Check stock availability
		if itemProto.Quantity < item.Quantity {
			return nil, &StockValidationError{
				ItemID:    item.ProductId,
				Requested: item.Quantity,
				Available: itemProto.Quantity,
				Message: fmt.Sprintf("Insufficient stock for item '%s': requested %d, available %d",
					itemProto.Name, item.Quantity, itemProto.Quantity),
			}
		}

		totalAmount += float64(item.Quantity) * itemProto.Price
		log.Printf("[Workflow %s] Item %s validated: price=%.2f, stock=%d, quantity=%d",
			orderId, item.ProductId, itemProto.Price, itemProto.Quantity, item.Quantity)
	}

	log.Printf("[Workflow %s] Stock validation passed, total amount: %.2f", orderId, totalAmount)

	// Step 2: Deduct stock from inventory (using database transaction)
	log.Printf("[Workflow %s] Step 2: Deducting stock from inventory", orderId)
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.DeductStockFromItems(req.MerchantId, req.Items)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to deduct stock: %w", err)
	}

	// Step 3: Create order in database
	log.Printf("[Workflow %s] Step 3: Creating order in database", orderId)
	restate.Set(ctx, "customer_id", req.CustomerId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)
	restate.Set(ctx, "total_amount", totalAmount)
	if req.MerchantId != "" {
		restate.Set(ctx, "merchant_id", req.MerchantId)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Workflow %s] Persisting order and items to database", orderId)
		if err := postgres.InsertOrder(orderId, req.CustomerId, req.MerchantId, orderpb.OrderStatus_PENDING, totalAmount); err != nil {
			return nil, err
		}
		return nil, postgres.InsertOrderItems(orderId, req.MerchantId, req.Items)
	})
	if err != nil {
		// If order creation fails, we need to restore the deducted stock
		log.Printf("[Workflow %s] Order creation failed, restoring stock", orderId)
		_, restoreErr := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.RestoreStockToItems(req.MerchantId, req.Items)
		})
		if restoreErr != nil {
			log.Printf("[Workflow %s] CRITICAL: Failed to restore stock after order creation failure: %v", orderId, restoreErr)
		}
		return nil, fmt.Errorf("failed to persist order to database: %w", err)
	}

	// No artificial delay; proceed to creating payment and invoice

	// Use Restate's deterministic random for payment ID to ensure workflow determinism
	paymentId := restate.Rand(ctx).UUID().String()
	paymentReq := &orderpb.ProcessPaymentRequest{
		OrderId: orderId,
		Amount:  totalAmount,
		// No payment method - Xendit invoice handles all payment methods
	}

	log.Printf("[Workflow %s] Step 2: Processing payment", orderId)
	paymentClient := restate.Object[*orderpb.ProcessPaymentResponse](ctx, "order.sv1.PaymentService", paymentId, "ProcessPayment", restate.WithProtoJSON)
	paymentResp, err := paymentClient.Request(paymentReq)
	if err != nil {
		restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED)
		})
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	log.Printf("[Workflow %s] Payment initiated: %s", orderId, paymentResp.PaymentId)
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "payment_status", paymentResp.Status)
	if paymentResp.InvoiceUrl != "" {
		restate.Set(ctx, "invoice_url", paymentResp.InvoiceUrl)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := postgres.UpdateOrderPayment(orderId, paymentResp.PaymentId); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update payment in database: %v", orderId, err)
	}

	// Set order status to PENDING and wait for payment completion
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)
	log.Printf("[Workflow %s] Order created successfully, payment pending", orderId)

	// Create an awakeable to wait for payment completion signal
	// This is the proper Restate pattern for waiting for external events
	log.Printf("[Workflow %s] Waiting for payment completion using awakeable...", orderId)
	awakeable := restate.Awakeable[any](ctx)
	awakeableId := awakeable.Id()
	restate.Set(ctx, "payment_awakeable_id", awakeableId)

	// Store the awakeable ID in the database so the API endpoint can access it
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		// Store awakeable ID in orders table (we need to add this column)
		_, err := postgres.DB.Exec(`UPDATE orders SET awakeable_id = $1 WHERE id = $2`, awakeableId, orderId)
		return nil, err
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to store awakeable ID in database: %v", orderId, err)
	}
	log.Printf("[Workflow %s] Awakeable ID stored: %s", orderId, awakeableId)

	// Wait for the awakeable to be resolved (payment completion signal)
	result, err := awakeable.Result()
	if err != nil {
		log.Printf("[Workflow %s] Awakeable failed: %v", orderId, err)
		return nil, fmt.Errorf("payment completion failed: %w", err)
	}
	log.Printf("[Workflow %s] Payment completion signal received: %v", orderId, result)

	// After sleep interruption (payment completion), continue to shipping
	log.Printf("[Workflow %s] Payment completed, proceeding to shipping", orderId)

	// Mark order as PROCESSING (payment completed) - stop here for manual processing
	restate.Set(ctx, "status", orderpb.OrderStatus_PROCESSING)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_PROCESSING)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to set PROCESSING: %v", orderId, err)
	}

	log.Printf("[Workflow %s] Order processing completed - waiting for manual status updates", orderId)
	return &orderpb.CreateOrderResponse{OrderId: orderId, InvoiceUrl: paymentResp.InvoiceUrl}, nil
}

// GetOrder retrieves comprehensive order information from workflow state
func GetOrder(ctx restate.WorkflowSharedContext, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Getting comprehensive order details", orderId)

	customerId, _ := restate.Get[string](ctx, "customer_id")
	status, _ := restate.Get[orderpb.OrderStatus](ctx, "status")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")
	paymentId, _ := restate.Get[string](ctx, "payment_id")
	shipmentId, _ := restate.Get[string](ctx, "shipment_id")
	trackingNumber, _ := restate.Get[string](ctx, "tracking_number")

	response := &orderpb.GetOrderResponse{Order: &orderpb.Order{Id: orderId, CustomerId: customerId, Status: status}}

	if paymentId != "" {
		log.Printf("[Workflow %s] Retrieving payment info for payment: %s", orderId, paymentId)
		paymentStatusVal, err := restate.Get[orderpb.PaymentStatus](ctx, "payment_status")
		if err != nil {
			paymentStatusVal = orderpb.PaymentStatus_PAYMENT_COMPLETED
		}
		response.PaymentInfo = &orderpb.PaymentInfo{PaymentId: paymentId, Status: paymentStatusVal, Amount: totalAmount, PaymentMethod: "CREDIT_CARD"}
	}

	if shipmentId != "" {
		log.Printf("[Workflow %s] Retrieving shipment info for shipment: %s", orderId, shipmentId)
		currentLocation, _ := restate.Get[string](ctx, "current_location")
		estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")
		var shipmentStatus orderpb.ShipmentStatus
		switch status {
		case orderpb.OrderStatus_SHIPPED:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_IN_TRANSIT
		case orderpb.OrderStatus_DELIVERED:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_DELIVERED
		default:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_CREATED
		}
		response.ShipmentInfo = &orderpb.ShipmentInfo{ShipmentId: shipmentId, TrackingNumber: trackingNumber, Carrier: "FedEx", Status: shipmentStatus, CurrentLocation: currentLocation, EstimatedDelivery: estimatedDelivery}
	}

	log.Printf("[Workflow %s] Order details retrieved - Status: %v, Payment: %v, Shipment: %v", orderId, status, paymentId != "", shipmentId != "")
	return response, nil
}

// UpdateOrderStatus updates the order status in workflow state and database
func UpdateOrderStatus(ctx restate.WorkflowContext, req *orderpb.UpdateOrderStatusRequest) (*orderpb.UpdateOrderStatusResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Updating status to: %v", orderId, req.Status)

	restate.Set(ctx, "status", req.Status)
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateOrderStatusDB(orderId, req.Status)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update status in database: %v", orderId, err)
		return &orderpb.UpdateOrderStatusResponse{Success: false, Message: fmt.Sprintf("Failed to update status in database: %v", err)}, nil
	}
	return &orderpb.UpdateOrderStatusResponse{Success: true, Message: "Order status updated successfully"}, nil
}

// OnPaymentUpdate handles payment status updates from webhooks
func OnPaymentUpdate(ctx restate.WorkflowContext, req *orderpb.OnPaymentUpdateRequest) (*orderpb.ContinueAfterPaymentResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] OnPaymentUpdate invoked for payment: %s with status: %s", orderId, req.PaymentId, req.Status)

	// Map Xendit status to internal status using existing mapping function
	ap2Status := payment.MapXenditStatusToAP2(req.Status)

	// Get the awakeable ID from the database (stored by Checkout workflow)
	var awakeableId string
	if postgres.DB != nil {
		err := postgres.DB.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderId).Scan(&awakeableId)
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to get awakeable ID from database: %v", orderId, err)
			return &orderpb.ContinueAfterPaymentResponse{Ok: false}, fmt.Errorf("awakeable ID not found in database: %w", err)
		}
	}

	if awakeableId == "" {
		log.Printf("[Workflow %s] Warning: Awakeable ID is empty", orderId)
		return &orderpb.ContinueAfterPaymentResponse{Ok: false}, fmt.Errorf("awakeable ID is empty")
	}

	// Resolve the awakeable to signal payment completion/failure
	// This will unblock any workflow waiting on the awakeable
	restate.ResolveAwakeable(ctx, awakeableId, ap2Status)
	log.Printf("[Workflow %s] Awakeable resolved with status: %s", orderId, ap2Status)

	// Update order status based on payment result
	if ap2Status == "completed" {
		restate.Set(ctx, "status", orderpb.OrderStatus_PROCESSING)
		_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_PROCESSING)
		})
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to set PROCESSING: %v", orderId, err)
		}
		log.Printf("[Workflow %s] Order marked as PROCESSING after payment completion", orderId)
	} else if ap2Status == "failed" {
		restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
		_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED)
		})
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to set CANCELLED: %v", orderId, err)
		}
		log.Printf("[Workflow %s] Order marked as CANCELLED after payment failure", orderId)
	}

	return &orderpb.ContinueAfterPaymentResponse{Ok: true}, nil
}

// ContinueAfterPayment resumes the order once payment is completed (simulated)
func ContinueAfterPayment(ctx restate.WorkflowContext, req *orderpb.ContinueAfterPaymentRequest) (*orderpb.ContinueAfterPaymentResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] ContinueAfterPayment invoked - resolving awakeable to continue workflow", orderId)

	// Get the awakeable ID that was saved during CreateOrder
	awakeableId, err := restate.Get[string](ctx, "payment_awakeable_id")
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to get awakeable ID: %v", orderId, err)
		return &orderpb.ContinueAfterPaymentResponse{Ok: false}, fmt.Errorf("awakeable ID not found: %w", err)
	}

	// Resolve the awakeable to signal payment completion
	// This will unblock the CreateOrder workflow waiting on the awakeable
	restate.ResolveAwakeable(ctx, awakeableId, "payment_completed")
	log.Printf("[Workflow %s] Awakeable resolved, workflow will continue", orderId)

	return &orderpb.ContinueAfterPaymentResponse{Ok: true}, nil
}

// CancelOrder implements a saga pattern for order cancellation with compensation
func CancelOrder(ctx restate.ObjectContext, req *orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Starting order cancellation saga for reason: %s", orderId, req.Reason)

	// Check current order status
	status, err := restate.Get[orderpb.OrderStatus](ctx, "status")
	if err != nil {
		return nil, fmt.Errorf("order not found: %w", err)
	}

	// Only allow cancellation for orders that are not already delivered
	if status == orderpb.OrderStatus_DELIVERED {
		return &orderpb.CancelOrderResponse{
			Success: false,
			Message: "Cannot cancel order that has already been delivered",
		}, nil
	}

	if status == orderpb.OrderStatus_CANCELLED {
		return &orderpb.CancelOrderResponse{
			Success: true,
			Message: "Order already cancelled",
		}, nil
	}

	// Get order details for compensation
	paymentId, _ := restate.Get[string](ctx, "payment_id")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")
	paymentStatus, _ := restate.Get[orderpb.PaymentStatus](ctx, "payment_status")

	// Get merchant_id from database instead of workflow state
	var merchantId string
	if postgres.DB != nil {
		var nullableMerchantId sql.NullString
		err := postgres.DB.QueryRow(`SELECT merchant_id FROM orders WHERE id = $1`, orderId).Scan(&nullableMerchantId)
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to get merchant_id from database: %v", orderId, err)
			// Fallback to workflow state
			merchantId, _ = restate.Get[string](ctx, "merchant_id")
		} else if nullableMerchantId.Valid {
			merchantId = nullableMerchantId.String
		} else {
			log.Printf("[Workflow %s] Warning: merchant_id is NULL in database, trying workflow state", orderId)
			// Fallback to workflow state if database value is NULL
			merchantId, _ = restate.Get[string](ctx, "merchant_id")
		}
	} else {
		// Fallback to workflow state if database is not available
		merchantId, _ = restate.Get[string](ctx, "merchant_id")
	}

	if merchantId == "" {
		return nil, fmt.Errorf("missing merchant_id for cancellation")
	}

	log.Printf("[Workflow %s] Order details - Merchant: %s, Payment: %s, Amount: %.2f, Payment Status: %v",
		orderId, merchantId, paymentId, totalAmount, paymentStatus)

	var refundId string

	// Step 1: Process refund only if payment was completed
	if status == orderpb.OrderStatus_PROCESSING || status == orderpb.OrderStatus_SHIPPED {
		if paymentId == "" {
			return nil, fmt.Errorf("payment_id required for refund processing")
		}

		log.Printf("[Workflow %s] Step 1: Processing refund for paid order", orderId)
		refundClient := restate.Object[*orderpb.ProcessRefundResponse](ctx, "order.sv1.PaymentService", paymentId, "ProcessRefund")
		refundReq := &orderpb.ProcessRefundRequest{
			PaymentId: paymentId,
			OrderId:   orderId,
			Amount:    totalAmount,
			Reason:    req.Reason,
		}

		refundResp, err := refundClient.Request(refundReq)
		if err != nil {
			log.Printf("[Workflow %s] Refund failed: %v", orderId, err)
			return &orderpb.CancelOrderResponse{
				Success: false,
				Message: fmt.Sprintf("Refund failed: %v", err),
			}, nil
		}

		refundId = refundResp.RefundId
		log.Printf("[Workflow %s] Refund processed successfully: %s", orderId, refundId)
	} else {
		log.Printf("[Workflow %s] No refund needed for order status: %v", orderId, status)
	}

	// Step 2: Restore stock to merchant inventory (compensation)
	log.Printf("[Workflow %s] Step 2: Restoring stock to inventory", orderId)

	// Get order items from database for stock restoration
	var orderItems []*orderpb.OrderItems
	if postgres.DB != nil {
		rows, err := postgres.DB.Query(`
			SELECT item_id, quantity 
			FROM order_items 
			WHERE order_id = $1 AND merchant_id = $2
		`, orderId, merchantId)
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to get order items for stock restoration: %v", orderId, err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var item orderpb.OrderItems
				if err := rows.Scan(&item.ProductId, &item.Quantity); err == nil {
					orderItems = append(orderItems, &item)
				}
			}
		}
	} else {
		log.Printf("[Workflow %s] Warning: Database not available for stock restoration", orderId)
	}

	if len(orderItems) > 0 {
		_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.RestoreStockToItems(merchantId, orderItems)
		})
		if err != nil {
			log.Printf("[Workflow %s] CRITICAL: Failed to restore stock during cancellation: %v", orderId, err)
			// This is a critical error - the refund was processed but stock wasn't restored
			// In a real system, this would require manual intervention
		} else {
			log.Printf("[Workflow %s] Stock restored successfully for %d items", orderId, len(orderItems))
		}
	} else {
		log.Printf("[Workflow %s] Warning: No order items found for stock restoration", orderId)
	}

	// Step 3: Update order status to CANCELLED
	log.Printf("[Workflow %s] Step 3: Updating order status to CANCELLED", orderId)
	restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
	restate.Set(ctx, "cancellation_reason", req.Reason)
	if refundId != "" {
		restate.Set(ctx, "refund_id", refundId)
	}

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update order status in database: %v", orderId, err)
	}

	log.Printf("[Workflow %s] Order cancellation saga completed successfully", orderId)

	message := "Order cancelled successfully with stock restored"
	if refundId != "" {
		message += fmt.Sprintf(" (Refund ID: %s)", refundId)
	}

	return &orderpb.CancelOrderResponse{
		Success:  true,
		Message:  message,
		RefundId: refundId,
	}, nil
}

// ShipOrder initiates shipping and triggers automatic delivery simulation
func ShipOrder(ctx restate.ObjectContext, req *orderpb.ShipOrderRequest) (*orderpb.ShipOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Starting shipping process", orderId)

	// Debug: Check if database is connected and list all orders
	if postgres.DB == nil {
		log.Printf("[Workflow %s] ERROR: Database connection is nil", orderId)
		return &orderpb.ShipOrderResponse{
			Success: false,
			Message: "Database not connected",
		}, nil
	}

	// Test database connection
	if err := postgres.DB.Ping(); err != nil {
		log.Printf("[Workflow %s] ERROR: Database ping failed: %v", orderId, err)
		return &orderpb.ShipOrderResponse{
			Success: false,
			Message: "Database connection failed",
		}, nil
	}
	log.Printf("[Workflow %s] Database connection verified", orderId)

	// Debug: List all orders to see what's in the database
	rows, err := postgres.DB.Query(`SELECT id, status FROM orders ORDER BY created_at DESC LIMIT 5`)
	if err == nil {
		log.Printf("[Workflow %s] Recent orders in database:", orderId)
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err == nil {
				log.Printf("[Workflow %s] Order: %s, Status: %s", orderId, id, status)
			}
		}
		rows.Close()
	}

	// Get current order status from database since this is an object handler
	var currentStatus string
	err = postgres.DB.QueryRow(`SELECT status FROM orders WHERE id = $1`, orderId).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[Workflow %s] Order not found in database", orderId)
			return &orderpb.ShipOrderResponse{
				Success: false,
				Message: "Order not found",
			}, nil
		}
		log.Printf("[Workflow %s] Database error getting order status: %v", orderId, err)
		return nil, fmt.Errorf("failed to get order status: %w", err)
	}
	log.Printf("[Workflow %s] Current order status from database: %s", orderId, currentStatus)

	// Convert string status to enum
	var status orderpb.OrderStatus
	switch currentStatus {
	case "PENDING":
		status = orderpb.OrderStatus_PENDING
	case "PROCESSING":
		status = orderpb.OrderStatus_PROCESSING
	case "SHIPPED":
		status = orderpb.OrderStatus_SHIPPED
	case "DELIVERED":
		status = orderpb.OrderStatus_DELIVERED
	case "CANCELLED":
		status = orderpb.OrderStatus_CANCELLED
	default:
		return &orderpb.ShipOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Unknown order status: %s", currentStatus),
		}, nil
	}

	if status != orderpb.OrderStatus_PROCESSING {
		return &orderpb.ShipOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Order must be in PROCESSING status to ship, current status: %s", currentStatus),
		}, nil
	}

	// Generate shipment ID
	shipmentId := restate.Rand(ctx).UUID().String()
	log.Printf("[Workflow %s] Generated shipment ID: %s", orderId, shipmentId)

	// Update the database using restate.Run to ensure proper transaction handling
	// ALL database operations must be inside restate.Run to persist properly
	log.Printf("[Workflow %s] Updating database in restate.Run context", orderId)

	// Compute an ISO-8601 date for estimated delivery to satisfy DATE column type
	estimatedDeliveryDate := time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		// Create shipment record
		if err := postgres.InsertShipment(
			shipmentId, orderId, req.TrackingNumber, req.Carrier, req.ServiceType,
			orderpb.ShipmentStatus_SHIPMENT_IN_TRANSIT, "Warehouse", estimatedDeliveryDate,
		); err != nil {
			log.Printf("[Workflow %s] Failed to create shipment: %v", orderId, err)
			return nil, err
		}
		log.Printf("[Workflow %s] Shipment record created", orderId)

		// Update shipment info in orders table
		if err := postgres.UpdateOrderShipment(orderId, shipmentId, req.TrackingNumber); err != nil {
			log.Printf("[Workflow %s] Failed to update order shipment: %v", orderId, err)
			return nil, err
		}
		log.Printf("[Workflow %s] Order shipment info updated", orderId)

		// Update order status to SHIPPED
		if err := postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_SHIPPED); err != nil {
			log.Printf("[Workflow %s] Failed to update status: %v", orderId, err)
			return nil, err
		}
		log.Printf("[Workflow %s] Order status updated to SHIPPED", orderId)

		return nil, nil
	})

	if err != nil {
		log.Printf("[Workflow %s] CRITICAL: Database update failed: %v", orderId, err)
		return &orderpb.ShipOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to update database: %v", err),
		}, nil
	}

	log.Printf("[Workflow %s] Successfully shipped order", orderId)

	log.Printf("[Workflow %s] Order shipped successfully", orderId)
	return &orderpb.ShipOrderResponse{
		Success:    true,
		Message:    "Order shipped successfully",
		ShipmentId: shipmentId,
	}, nil
}

// DeliverOrder manually moves an order from SHIPPED to DELIVERED status
func DeliverOrder(ctx restate.ObjectContext, req *orderpb.DeliverOrderRequest) (*orderpb.DeliverOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Manually delivering order", orderId)

	// Get current order status from database (object handlers can't access workflow state)
	var currentStatus string
	var shipmentId sql.NullString
	err := postgres.DB.QueryRow(`SELECT status, shipment_id FROM orders WHERE id = $1`, orderId).Scan(&currentStatus, &shipmentId)
	if err != nil {
		if err == sql.ErrNoRows {
			return &orderpb.DeliverOrderResponse{
				Success: false,
				Message: "Order not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	if currentStatus != "SHIPPED" {
		return &orderpb.DeliverOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Order must be in SHIPPED status to deliver, current status: %s", currentStatus),
		}, nil
	}

	// Update order status to DELIVERED in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		// Update shipment status to delivered if we have a shipment ID
		if shipmentId.Valid && shipmentId.String != "" {
			if err := postgres.UpdateShipmentStatus(shipmentId.String, orderpb.ShipmentStatus_SHIPMENT_DELIVERED, "Delivered"); err != nil {
				log.Printf("[Workflow %s] Warning: Failed to update shipment status: %v", orderId, err)
			}
		}
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_DELIVERED)
	})
	if err != nil {
		return &orderpb.DeliverOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to update order status: %v", err),
		}, nil
	}

	log.Printf("[Workflow %s] Order delivered successfully", orderId)
	return &orderpb.DeliverOrderResponse{
		Success: true,
		Message: "Order delivered successfully",
	}, nil
}

// ConfirmOrder moves an order from DELIVERED to COMPLETED status
func ConfirmOrder(ctx restate.ObjectContext, req *orderpb.ConfirmOrderRequest) (*orderpb.ConfirmOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Confirming order after delivery", orderId)

	// Get current order status from database
	var currentStatus string
	err := postgres.DB.QueryRow(`SELECT status FROM orders WHERE id = $1`, orderId).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return &orderpb.ConfirmOrderResponse{
				Success: false,
				Message: "Order not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	if currentStatus != "DELIVERED" {
		return &orderpb.ConfirmOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Order must be in DELIVERED status to confirm, current status: %s", currentStatus),
		}, nil
	}

	// Update order status to COMPLETED in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_COMPLETED)
	})
	if err != nil {
		return &orderpb.ConfirmOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to update order status: %v", err),
		}, nil
	}

	log.Printf("[Workflow %s] Order confirmed successfully", orderId)
	return &orderpb.ConfirmOrderResponse{
		Success: true,
		Message: "Order confirmed successfully",
	}, nil
}

// ReturnOrder implements a saga pattern for order return with refund and stock restoration
func ReturnOrder(ctx restate.ObjectContext, req *orderpb.ReturnOrderRequest) (*orderpb.ReturnOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Starting order return saga for reason: %s", orderId, req.Reason)

	// Get current order status from database
	var currentStatus string
	var merchantId sql.NullString
	var totalAmount float64
	var paymentId sql.NullString
	err := postgres.DB.QueryRow(`SELECT status, merchant_id, total_amount, payment_id FROM orders WHERE id = $1`, orderId).Scan(&currentStatus, &merchantId, &totalAmount, &paymentId)
	if err != nil {
		if err == sql.ErrNoRows {
			return &orderpb.ReturnOrderResponse{
				Success: false,
				Message: "Order not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	if currentStatus != "DELIVERED" {
		return &orderpb.ReturnOrderResponse{
			Success: false,
			Message: fmt.Sprintf("Order must be in DELIVERED status to return, current status: %s", currentStatus),
		}, nil
	}

	if !merchantId.Valid || merchantId.String == "" {
		return &orderpb.ReturnOrderResponse{
			Success: false,
			Message: "Merchant ID not found for order",
		}, nil
	}

	log.Printf("[Workflow %s] Order details - Merchant: %s, Payment: %s, Amount: %.2f",
		orderId, merchantId.String, paymentId.String, totalAmount)

	var refundId string

	// Step 1: Process refund if payment exists
	if paymentId.Valid && paymentId.String != "" {
		log.Printf("[Workflow %s] Step 1: Processing refund for returned order", orderId)
		refundClient := restate.Object[*orderpb.ProcessRefundResponse](ctx, "order.sv1.PaymentService", paymentId.String, "ProcessRefund")
		refundReq := &orderpb.ProcessRefundRequest{
			PaymentId: paymentId.String,
			OrderId:   orderId,
			Amount:    totalAmount,
			Reason:    req.Reason,
		}

		refundResp, err := refundClient.Request(refundReq)
		if err != nil {
			log.Printf("[Workflow %s] Refund failed: %v", orderId, err)
			return &orderpb.ReturnOrderResponse{
				Success: false,
				Message: fmt.Sprintf("Refund failed: %v", err),
			}, nil
		}

		refundId = refundResp.RefundId
		log.Printf("[Workflow %s] Refund processed successfully: %s", orderId, refundId)
	} else {
		log.Printf("[Workflow %s] No payment found for refund", orderId)
	}

	// Step 2: Restore stock to merchant inventory
	log.Printf("[Workflow %s] Step 2: Restoring stock to inventory", orderId)

	// Get order items from database for stock restoration
	var orderItems []*orderpb.OrderItems
	if postgres.DB != nil {
		rows, err := postgres.DB.Query(`
			SELECT item_id, quantity 
			FROM order_items 
			WHERE order_id = $1 AND merchant_id = $2
		`, orderId, merchantId.String)
		if err != nil {
			log.Printf("[Workflow %s] Warning: Failed to get order items for stock restoration: %v", orderId, err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var item orderpb.OrderItems
				if err := rows.Scan(&item.ProductId, &item.Quantity); err == nil {
					orderItems = append(orderItems, &item)
				}
			}
		}
	}

	if len(orderItems) > 0 {
		_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, postgres.RestoreStockToItems(merchantId.String, orderItems)
		})
		if err != nil {
			log.Printf("[Workflow %s] CRITICAL: Failed to restore stock during return: %v", orderId, err)
			// This is a critical error - the refund was processed but stock wasn't restored
			// In a real system, this would require manual intervention
		} else {
			log.Printf("[Workflow %s] Stock restored successfully for %d items", orderId, len(orderItems))
		}
	} else {
		log.Printf("[Workflow %s] Warning: No order items found for stock restoration", orderId)
	}

	// Step 3: Update order status to RETURNED
	log.Printf("[Workflow %s] Step 3: Updating order status to RETURNED", orderId)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_RETURNED)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update order status in database: %v", orderId, err)
	}

	log.Printf("[Workflow %s] Order return saga completed successfully", orderId)

	message := "Order returned successfully with stock restored"
	if refundId != "" {
		message += fmt.Sprintf(" (Refund ID: %s)", refundId)
	}

	return &orderpb.ReturnOrderResponse{
		Success:  true,
		Message:  message,
		RefundId: refundId,
	}, nil
}

// Keep a tiny server import linker reference to avoid unused import errors when building only package
var _ = server.NewRestate

// Dummy ref removed
