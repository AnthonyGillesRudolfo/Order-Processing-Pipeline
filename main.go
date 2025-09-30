package main

import (
	"context"
	"fmt"
	"log"
	"os"

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
func CreateOrder(ctx restate.WorkflowContext, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	// The workflow key is the order ID
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Creating order for customer: %s", orderId, req.CustomerId)

	// Step 1: Store order details in workflow state
	restate.Set(ctx, "customer_id", req.CustomerId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)

	// Step 2: Calculate total amount from items
	var totalAmount float64 = 0.0
	for _, item := range req.Items {
		totalAmount += float64(item.Quantity) * 10.0 // Assume $10 per item for demo
	}
	restate.Set(ctx, "total_amount", totalAmount)

	// Step 3: Process payment via Payment Virtual Object
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

	log.Printf("[Workflow %s] Step 1: Processing payment", orderId)
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
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	log.Printf("[Workflow %s] Payment completed: %s", orderId, paymentResp.PaymentId)
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PAID)

	// Step 4: Create shipment via Shipping Virtual Object
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

	log.Printf("[Workflow %s] Step 2: Creating shipment", orderId)
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
	restate.Set(ctx, "status", orderpb.OrderStatus_SHIPPED)

	// Step 5: Mark order as completed
	restate.Set(ctx, "status", orderpb.OrderStatus_COMPLETED)
	log.Printf("[Workflow %s] Order completed successfully", orderId)

	return &orderpb.CreateOrderResponse{
		OrderId: orderId,
	}, nil
}

// GetOrder retrieves order information from workflow state
func GetOrder(ctx restate.WorkflowSharedContext, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Getting order details", orderId)

	// Retrieve order state
	customerId, _ := restate.Get[string](ctx, "customer_id")
	status, _ := restate.Get[orderpb.OrderStatus](ctx, "status")

	return &orderpb.GetOrderResponse{
		Order: &orderpb.Order{
			Id:         orderId,
			CustomerId: customerId,
			Status:     status,
		},
	}, nil
}

// UpdateOrderStatus updates the order status in workflow state
func UpdateOrderStatus(ctx restate.WorkflowContext, req *orderpb.UpdateOrderStatusRequest) (*orderpb.UpdateOrderStatusResponse, error) {
	orderId := restate.Key(ctx)
	log.Printf("[Workflow %s] Updating status to: %v", orderId, req.Status)

	restate.Set(ctx, "status", req.Status)

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

	// Process payment (durable execution)
	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Payment Object %s] Executing payment transaction", paymentId)
		// TODO: Call external payment gateway
		// Simulate payment processing
		return nil, nil
	})
	if err != nil {
		status = orderpb.PaymentStatus_PAYMENT_FAILED
		restate.Set(ctx, "status", status)
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

	// Create shipment (durable execution)
	trackingNumber := fmt.Sprintf("TRACK-%s", uuid.New().String()[:8])
	estimatedDelivery := "2025-10-10"

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Shipping Object %s] Creating shipment with carrier: %s", shipmentId, req.ShippingMethod.Carrier)
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
	restate.Set(ctx, "current_location", "Warehouse")

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
