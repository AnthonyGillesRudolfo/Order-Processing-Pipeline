package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
)

// ============================================================================
// ORDER WORKFLOW - Multi-step order processing with exactly-once execution
// ============================================================================

// CreateOrder is the main workflow handler that orchestrates the order process
// The workflow ID (key) is the order ID, ensuring exactly-once execution per order
//
// Status Flow with Observable Delays:
//
//	PENDING (5s) → PROCESSING (5s) → SHIPPED (10s) → DELIVERED
//
// The workflow includes durable sleeps between status transitions to simulate:
// - Payment processing time (5 seconds in PENDING)
// - Order preparation time (5 seconds in PROCESSING)
// - Delivery time (10 seconds in SHIPPED)
//
// Total workflow time: ~20 seconds
func CreateOrder(ctx restate.WorkflowContext, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	// The workflow key is the order ID
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Creating order for customer: %s", orderId, req.CustomerId)

	// Step 1: Calculate total amount from items
	var totalAmount float64 = 0.0
	for _, item := range req.Items {
		totalAmount += float64(item.Quantity) * 1 // Assume $10 per item for demo
	}

	// Step 2: Store order details in workflow state
	restate.Set(ctx, "customer_id", req.CustomerId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)
	restate.Set(ctx, "total_amount", totalAmount)

	// Step 3: Persist order to database (durable execution)
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Workflow %s] Persisting order to database", orderId)
		return nil, InsertOrder(orderId, req.CustomerId, orderpb.OrderStatus_PENDING, totalAmount)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to persist order to database: %w", err)
	}

	log.Printf("[Workflow %s] Order is PENDING - simulating payment processing delay (5 seconds)", orderId)
	// Durable sleep: Simulate payment processing time (5 seconds)
	// This allows the order to be observable in PENDING status
	if err := restate.Sleep(ctx, 5*time.Second); err != nil {
		return nil, fmt.Errorf("sleep interrupted: %w", err)
	}

	// Step 4: Process payment via Payment Virtual Object
	paymentId := uuid.New().String()
	paymentReq := &orderpb.ProcessPaymentRequest{
		OrderId: orderId,
		PaymentMethod: &orderpb.PaymentMethod{
			Method: &orderpb.PaymentMethod_CreditCard{
				CreditCard: &orderpb.CreditCard{
					CardNumber:     "****-****-****-1234",
					CardholderName: "Customer",
				},
			},
		},
		Amount: totalAmount,
	}

	log.Printf("[Workflow %s] Step 2: Processing payment", orderId)
	paymentClient := restate.Object[*orderpb.ProcessPaymentResponse](
		ctx,
		"order.sv1.PaymentService",
		paymentId,
		"ProcessPayment",
		restate.WithProtoJSON,
	)
	paymentResp, err := paymentClient.Request(paymentReq)
	if err != nil {
		restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
		// Update database status
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED)
		})
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	log.Printf("[Workflow %s] Payment completed: %s", orderId, paymentResp.PaymentId)
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "payment_status", paymentResp.Status)
	restate.Set(ctx, "status", orderpb.OrderStatus_PROCESSING)

	// Update order with payment info in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := UpdateOrderPayment(orderId, paymentResp.PaymentId); err != nil {
			return nil, err
		}
		return nil, UpdateOrderStatusDB(orderId, orderpb.OrderStatus_PROCESSING)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update payment in database: %v", orderId, err)
	}

	// Durable sleep: Wait 5 seconds before creating shipment (simulates order preparation)
	log.Printf("[Workflow %s] Order is PROCESSING - preparing shipment (5 second delay)", orderId)
	if err := restate.Sleep(ctx, 5*time.Second); err != nil {
		return nil, fmt.Errorf("sleep interrupted: %w", err)
	}

	// Step 5: Create shipment via Shipping Virtual Object
	shipmentId := uuid.New().String()
	shipmentReq := &orderpb.CreateShipmentRequest{
		OrderId: orderId,
		ShippingAddress: &orderpb.ShippingAddress{
			Street:        "123 Main St",
			City:          "San Francisco",
			State:         "CA",
			PostalCode:    "94105",
			Country:       "USA",
			RecipientName: "Customer",
		},
		ShippingMethod: &orderpb.ShippingMethod{
			Carrier:       "FedEx",
			ServiceType:   "Ground",
			Cost:          15.0,
			EstimatedDays: 5,
		},
	}

	log.Printf("[Workflow %s] Step 3: Creating shipment", orderId)
	shippingClient := restate.Object[*orderpb.CreateShipmentResponse](
		ctx,
		"order.sv1.ShippingService",
		shipmentId,
		"CreateShipment",
		restate.WithProtoJSON,
	)
	shipmentResp, err := shippingClient.Request(shipmentReq)
	if err != nil {
		return nil, fmt.Errorf("shipment creation failed: %w", err)
	}

	log.Printf("[Workflow %s] Shipment created: %s", orderId, shipmentResp.ShipmentId)
	restate.Set(ctx, "shipment_id", shipmentResp.ShipmentId)
	restate.Set(ctx, "tracking_number", shipmentResp.TrackingNumber)
	restate.Set(ctx, "estimated_delivery", shipmentResp.EstimatedDelivery)
	restate.Set(ctx, "current_location", "In Transit")
	restate.Set(ctx, "status", orderpb.OrderStatus_SHIPPED)

	// Update order with shipment info in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := UpdateOrderShipment(orderId, shipmentResp.ShipmentId, shipmentResp.TrackingNumber); err != nil {
			return nil, err
		}
		return nil, UpdateOrderStatusDB(orderId, orderpb.OrderStatus_SHIPPED)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update shipment in database: %v", orderId, err)
	}

	log.Printf("[Workflow %s] Order is now SHIPPED and in transit", orderId)
	log.Printf("[Workflow %s] Tracking number: %s", orderId, shipmentResp.TrackingNumber)

	// Step 6: Simulate delivery time (10 seconds)
	log.Printf("[Workflow %s] Simulating delivery time (10 second delay)", orderId)
	if err := restate.Sleep(ctx, 10*time.Second); err != nil {
		return nil, fmt.Errorf("sleep interrupted: %w", err)
	}

	// Step 7: Mark order as delivered
	log.Printf("[Workflow %s] Order has been delivered", orderId)
	restate.Set(ctx, "status", orderpb.OrderStatus_DELIVERED)
	restate.Set(ctx, "current_location", "Delivered")

	// Update final status in database
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, UpdateOrderStatusDB(orderId, orderpb.OrderStatus_DELIVERED)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update final status in database: %v", orderId, err)
	}

	log.Printf("[Workflow %s] Order completed successfully - Status: DELIVERED", orderId)

	return &orderpb.CreateOrderResponse{
		OrderId: orderId,
	}, nil
}

// GetOrder retrieves comprehensive order information from workflow state and virtual objects
func GetOrder(ctx restate.WorkflowSharedContext, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Getting comprehensive order details", orderId)

	// Retrieve order state from workflow
	customerId, _ := restate.Get[string](ctx, "customer_id")
	status, _ := restate.Get[orderpb.OrderStatus](ctx, "status")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")
	paymentId, _ := restate.Get[string](ctx, "payment_id")
	shipmentId, _ := restate.Get[string](ctx, "shipment_id")
	trackingNumber, _ := restate.Get[string](ctx, "tracking_number")

	// Build order response
	response := &orderpb.GetOrderResponse{
		Order: &orderpb.Order{
			Id:         orderId,
			CustomerId: customerId,
			Status:     status,
		},
	}

	// Retrieve payment information from Payment Virtual Object if payment exists
	if paymentId != "" {
		log.Printf("[Workflow %s] Retrieving payment info for payment: %s", orderId, paymentId)

		// Call Payment Virtual Object to get payment details
		paymentStatusVal, err := restate.Get[orderpb.PaymentStatus](ctx, "payment_status")
		if err != nil {
			// If not in workflow state, we'll use a default
			paymentStatusVal = orderpb.PaymentStatus_PAYMENT_COMPLETED
		}

		response.PaymentInfo = &orderpb.PaymentInfo{
			PaymentId:     paymentId,
			Status:        paymentStatusVal,
			Amount:        totalAmount,
			PaymentMethod: "CREDIT_CARD", // Could be stored in state
		}
	}

	// Retrieve shipment information from Shipping Virtual Object if shipment exists
	if shipmentId != "" {
		log.Printf("[Workflow %s] Retrieving shipment info for shipment: %s", orderId, shipmentId)

		// Get shipment details from workflow state
		currentLocation, _ := restate.Get[string](ctx, "current_location")
		estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")

		// Determine shipment status based on order status
		var shipmentStatus orderpb.ShipmentStatus
		switch status {
		case orderpb.OrderStatus_SHIPPED:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_IN_TRANSIT
		case orderpb.OrderStatus_DELIVERED:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_DELIVERED
		default:
			shipmentStatus = orderpb.ShipmentStatus_SHIPMENT_CREATED
		}

		response.ShipmentInfo = &orderpb.ShipmentInfo{
			ShipmentId:        shipmentId,
			TrackingNumber:    trackingNumber,
			Carrier:           "FedEx", // Could be stored in state
			Status:            shipmentStatus,
			CurrentLocation:   currentLocation,
			EstimatedDelivery: estimatedDelivery,
		}
	}

	log.Printf("[Workflow %s] Order details retrieved - Status: %v, Payment: %v, Shipment: %v",
		orderId, status, paymentId != "", shipmentId != "")

	return response, nil
}

// UpdateOrderStatus updates the order status in workflow state and database
func UpdateOrderStatus(ctx restate.WorkflowContext, req *orderpb.UpdateOrderStatusRequest) (*orderpb.UpdateOrderStatusResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Updating status to: %v", orderId, req.Status)

	// Update workflow state
	restate.Set(ctx, "status", req.Status)

	// Persist to database (durable execution)
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, UpdateOrderStatusDB(orderId, req.Status)
	})
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update status in database: %v", orderId, err)
		return &orderpb.UpdateOrderStatusResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to update status in database: %v", err),
		}, nil
	}

	return &orderpb.UpdateOrderStatusResponse{
		Success: true,
		Message: "Order status updated successfully",
	}, nil
}

// ============================================================================
// PAYMENT VIRTUAL OBJECT - Stateful payment processing keyed by payment ID
// ============================================================================

// ProcessPayment is a virtual object handler for processing payments
// Each payment has its own isolated state, keyed by payment ID
func ProcessPayment(ctx restate.ObjectContext, req *orderpb.ProcessPaymentRequest) (*orderpb.ProcessPaymentResponse, error) {
	// The key for this virtual object is the payment ID
	paymentId := restate.Key(ctx)
	log.Printf("[Payment Object %s] Processing payment for order: %s, amount: %.2f", paymentId, req.OrderId, req.Amount)

	// Get current payment state
	status, err := restate.Get[orderpb.PaymentStatus](ctx, "status")
	if err == nil {
		log.Printf("[Payment Object %s] Payment already processed with status: %v", paymentId, status)
		return &orderpb.ProcessPaymentResponse{
			PaymentId: paymentId,
			Status:    status,
		}, nil
	}

	// Determine payment method string for database
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

	// Insert initial payment record in database (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Creating payment record in database", paymentId)
		return nil, InsertPayment(paymentId, req.OrderId, req.Amount, paymentMethod, orderpb.PaymentStatus_PAYMENT_PROCESSING)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to create payment record: %v", paymentId, err)
	}

	// Process payment (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Starting payment transaction processing...", paymentId)
		log.Printf("[Payment Object %s] Simulating payment gateway call (5 second delay)", paymentId)

		// Simulate payment processing time (5 seconds)
		time.Sleep(5 * time.Second)

		// Simulate 40% failure rate to test Restate's retry mechanism
		// Generate random number between 0 and 99
		randomValue := rand.Intn(100)

		// 40% chance of failure (0-39), 60% chance of success (40-99)
		if randomValue < 40 {
			log.Printf("[Payment Object %s] ❌ Payment attempt FAILED (random=%d < 40) - Restate will retry", paymentId, randomValue)
			return nil, fmt.Errorf("payment gateway error: transaction declined (simulated failure)")
		}

		log.Printf("[Payment Object %s] ✅ Payment attempt SUCCEEDED (random=%d >= 40)", paymentId, randomValue)
		log.Printf("[Payment Object %s] Payment transaction completed successfully", paymentId)
		return nil, nil
	})
	if err != nil {
		status = orderpb.PaymentStatus_PAYMENT_FAILED
		restate.Set(ctx, "status", status)

		// Update database with failed status
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
			return nil, UpdatePaymentStatus(paymentId, status)
		})

		log.Printf("[Payment Object %s] Payment processing failed after all retries: %v", paymentId, err)
		return &orderpb.ProcessPaymentResponse{
			PaymentId: paymentId,
			Status:    status,
		}, fmt.Errorf("payment processing failed: %w", err)
	}

	// Update state: payment completed
	status = orderpb.PaymentStatus_PAYMENT_COMPLETED
	restate.Set(ctx, "status", status)
	restate.Set(ctx, "order_id", req.OrderId)
	restate.Set(ctx, "amount", req.Amount)

	// Update database with completed status (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, UpdatePaymentStatus(paymentId, status)
	})
	if err != nil {
		log.Printf("[Payment Object %s] Warning: Failed to update payment status in database: %v", paymentId, err)
	}

	log.Printf("[Payment Object %s] Payment completed successfully", paymentId)

	return &orderpb.ProcessPaymentResponse{
		PaymentId: paymentId,
		Status:    status,
	}, nil
}

// ============================================================================
// SHIPPING VIRTUAL OBJECT - Stateful shipment tracking keyed by shipment ID
// ============================================================================

// CreateShipment is a virtual object handler for creating shipments
// Each shipment has its own isolated state, keyed by shipment ID
func CreateShipment(ctx restate.ObjectContext, req *orderpb.CreateShipmentRequest) (*orderpb.CreateShipmentResponse, error) {
	// The key for this virtual object is the shipment ID
	shipmentId := restate.Key(ctx)
	log.Printf("[Shipping Object %s] Creating shipment for order: %s", shipmentId, req.OrderId)

	// Check if shipment already exists
	existingStatus, err := restate.Get[orderpb.ShipmentStatus](ctx, "status")
	if err == nil {
		log.Printf("[Shipping Object %s] Shipment already exists with status: %v", shipmentId, existingStatus)
		trackingNumber, _ := restate.Get[string](ctx, "tracking_number")
		estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")
		return &orderpb.CreateShipmentResponse{
			ShipmentId:        shipmentId,
			TrackingNumber:    trackingNumber,
			EstimatedDelivery: estimatedDelivery,
		}, nil
	}

	// Generate tracking number and estimated delivery
	trackingNumber := fmt.Sprintf("TRACK-%s", uuid.New().String()[:8])
	estimatedDelivery := "2025-10-10"
	currentLocation := "Warehouse"

	// Create shipment with external provider (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Shipping Object %s] Creating shipment with carrier: %s", shipmentId, req.ShippingMethod.Carrier)
		log.Printf("[Shipping Object %s] Shipment creation with provider completed", shipmentId)
		// TODO: Call external shipping provider API
		return nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("shipment creation failed: %w", err)
	}

	// Update state: shipment created
	status := orderpb.ShipmentStatus_SHIPMENT_CREATED
	restate.Set(ctx, "status", status)
	restate.Set(ctx, "order_id", req.OrderId)
	restate.Set(ctx, "tracking_number", trackingNumber)
	restate.Set(ctx, "estimated_delivery", estimatedDelivery)
	restate.Set(ctx, "current_location", currentLocation)

	// Persist shipment to database (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Shipping Object %s] Persisting shipment to database", shipmentId)
		return nil, InsertShipment(
			shipmentId,
			req.OrderId,
			trackingNumber,
			req.ShippingMethod.Carrier,
			req.ShippingMethod.ServiceType,
			status,
			currentLocation,
			estimatedDelivery,
		)
	})
	if err != nil {
		log.Printf("[Shipping Object %s] Warning: Failed to persist shipment to database: %v", shipmentId, err)
	}

	log.Printf("[Shipping Object %s] Shipment created successfully", shipmentId)

	return &orderpb.CreateShipmentResponse{
		ShipmentId:        shipmentId,
		TrackingNumber:    trackingNumber,
		EstimatedDelivery: estimatedDelivery,
	}, nil
}

// TrackShipment is a shared (read-only) handler for tracking shipments
func TrackShipment(ctx restate.ObjectSharedContext, req *orderpb.TrackShipmentRequest) (*orderpb.TrackShipmentResponse, error) {
	// The key for this virtual object is the shipment ID
	shipmentId := restate.Key(ctx)
	log.Printf("[Shipping Object %s] Tracking shipment", shipmentId)

	// Get shipment state
	status, err := restate.Get[orderpb.ShipmentStatus](ctx, "status")
	if err != nil {
		return nil, fmt.Errorf("shipment not found: %w", err)
	}

	currentLocation, _ := restate.Get[string](ctx, "current_location")
	estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")

	return &orderpb.TrackShipmentResponse{
		ShipmentId:        shipmentId,
		Status:            status,
		CurrentLocation:   currentLocation,
		EstimatedDelivery: estimatedDelivery,
		Events:            []*orderpb.ShipmentEvent{}, // TODO: Store and retrieve events
	}, nil
}

// ============================================================================
// MAIN - Server initialization and service binding
// ============================================================================

func main() {
	log.Println("Starting Order Processing Pipeline...")

	// Initialize database connection
	dbConfig := DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "orderpipeline",
		User:     "orderpipelineadmin",
		Password: "asdf", // Empty password as specified
	}

	log.Println("Connecting to PostgreSQL database...")
	if err := InitDatabase(dbConfig); err != nil {
		log.Printf("WARNING: Failed to connect to database: %v", err)
		log.Println("Continuing without database persistence...")
	} else {
		log.Println("Database connection established successfully")
		// Ensure database is closed on exit
		defer func() {
			if err := CloseDatabase(); err != nil {
				log.Printf("Error closing database: %v", err)
			}
		}()
	}

	// Create Restate server
	srv := server.NewRestate()

	// Bind OrderService as a Workflow
	orderWorkflow := restate.NewWorkflow("order.sv1.OrderService", restate.WithProtoJSON).
		Handler("CreateOrder", restate.NewWorkflowHandler(CreateOrder)).
		Handler("GetOrder", restate.NewWorkflowSharedHandler(GetOrder)).
		Handler("UpdateOrderStatus", restate.NewWorkflowHandler(UpdateOrderStatus))
	srv = srv.Bind(orderWorkflow)

	// Bind PaymentService as a Virtual Object
	paymentVirtualObject := restate.NewObject("order.sv1.PaymentService", restate.WithProtoJSON).
		Handler("ProcessPayment", restate.NewObjectHandler(ProcessPayment))
	srv = srv.Bind(paymentVirtualObject)

	// Bind ShippingService as a Virtual Object
	shippingVirtualObject := restate.NewObject("order.sv1.ShippingService", restate.WithProtoJSON).
		Handler("CreateShipment", restate.NewObjectHandler(CreateShipment)).
		Handler("TrackShipment", restate.NewObjectSharedHandler(TrackShipment))
	srv = srv.Bind(shippingVirtualObject)

	// Start the server on port 9080
	log.Println("Restate server listening on :9081")
	log.Println("")
	log.Println("Service Architecture:")
	log.Println("  - OrderService: WORKFLOW (keyed by order ID)")
	log.Println("  - PaymentService: VIRTUAL OBJECT (keyed by payment ID)")
	log.Println("  - ShippingService: VIRTUAL OBJECT (keyed by shipment ID)")
	log.Println("")
	log.Println("Register with Restate:")
	log.Println("  restate deployments register http://localhost:9081")
	log.Println("")

	if err := srv.Start(context.Background(), ":9081"); err != nil {
		log.Printf("Server error: %v", err)
		os.Exit(1)
	}
}
