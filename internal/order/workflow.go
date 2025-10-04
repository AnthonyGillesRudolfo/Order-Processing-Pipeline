package order

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/google/uuid"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// CreateOrder orchestrates the order process
func CreateOrder(ctx restate.WorkflowContext, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	orderId := restate.Key(ctx)
    log.Printf("[Workflow %s] Creating order for customer: %s merchant: %s", orderId, req.CustomerId, req.MerchantId)

	var totalAmount float64
	for _, item := range req.Items {
		_ = item // For now assume price lookup external; using placeholder $1 per qty unit
		totalAmount += float64(item.Quantity) * 1
	}

    restate.Set(ctx, "customer_id", req.CustomerId)
	restate.Set(ctx, "status", orderpb.OrderStatus_PENDING)
	restate.Set(ctx, "total_amount", totalAmount)
    if req.MerchantId != "" { restate.Set(ctx, "merchant_id", req.MerchantId) }

    _, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
        log.Printf("[Workflow %s] Persisting order and items to database", orderId)
        if err := postgres.InsertOrder(orderId, req.CustomerId, req.MerchantId, orderpb.OrderStatus_PENDING, totalAmount); err != nil { return nil, err }
        return nil, postgres.InsertOrderItems(orderId, req.MerchantId, req.Items)
    })
	if err != nil {
		return nil, fmt.Errorf("failed to persist order to database: %w", err)
	}

	log.Printf("[Workflow %s] Order is PENDING - simulating payment processing delay (5 seconds)", orderId)
	if err := restate.Sleep(ctx, 5*time.Second); err != nil {
		return nil, fmt.Errorf("sleep interrupted: %w", err)
	}

	paymentId := uuid.New().String()
	paymentReq := &orderpb.ProcessPaymentRequest{
		OrderId: orderId,
		PaymentMethod: &orderpb.PaymentMethod{
			Method: &orderpb.PaymentMethod_CreditCard{
				CreditCard: &orderpb.CreditCard{CardNumber: "****-****-****-1234", CardholderName: "Customer"},
			},
		},
		Amount: totalAmount,
	}

	log.Printf("[Workflow %s] Step 2: Processing payment", orderId)
	paymentClient := restate.Object[*orderpb.ProcessPaymentResponse](ctx, "order.sv1.PaymentService", paymentId, "ProcessPayment", restate.WithProtoJSON)
	paymentResp, err := paymentClient.Request(paymentReq)
	if err != nil {
		restate.Set(ctx, "status", orderpb.OrderStatus_CANCELLED)
		_, _ = restate.Run(ctx, func(ctx restate.RunContext) (any, error) { return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_CANCELLED) })
		return nil, fmt.Errorf("payment failed: %w", err)
	}

	log.Printf("[Workflow %s] Payment completed: %s", orderId, paymentResp.PaymentId)
	restate.Set(ctx, "payment_id", paymentResp.PaymentId)
	restate.Set(ctx, "payment_status", paymentResp.Status)
	restate.Set(ctx, "status", orderpb.OrderStatus_PROCESSING)

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := postgres.UpdateOrderPayment(orderId, paymentResp.PaymentId); err != nil { return nil, err }
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_PROCESSING)
	})
	if err != nil { log.Printf("[Workflow %s] Warning: Failed to update payment in database: %v", orderId, err) }

	log.Printf("[Workflow %s] Order is PROCESSING - preparing shipment (5 second delay)", orderId)
	if err := restate.Sleep(ctx, 5*time.Second); err != nil { return nil, fmt.Errorf("sleep interrupted: %w", err) }

	shipmentId := uuid.New().String()
	shipmentReq := &orderpb.CreateShipmentRequest{
		OrderId: orderId,
		ShippingAddress: &orderpb.ShippingAddress{Street: "123 Main St", City: "San Francisco", State: "CA", PostalCode: "94105", Country: "USA", RecipientName: "Customer"},
		ShippingMethod: &orderpb.ShippingMethod{Carrier: "FedEx", ServiceType: "Ground", Cost: 15.0, EstimatedDays: 5},
	}

	log.Printf("[Workflow %s] Step 3: Creating shipment", orderId)
	shippingClient := restate.Object[*orderpb.CreateShipmentResponse](ctx, "order.sv1.ShippingService", shipmentId, "CreateShipment", restate.WithProtoJSON)
	shipmentResp, err := shippingClient.Request(shipmentReq)
	if err != nil { return nil, fmt.Errorf("shipment creation failed: %w", err) }

	log.Printf("[Workflow %s] Shipment created: %s", orderId, shipmentResp.ShipmentId)
	restate.Set(ctx, "shipment_id", shipmentResp.ShipmentId)
	restate.Set(ctx, "tracking_number", shipmentResp.TrackingNumber)
	restate.Set(ctx, "estimated_delivery", shipmentResp.EstimatedDelivery)
	restate.Set(ctx, "current_location", "In Transit")
	restate.Set(ctx, "status", orderpb.OrderStatus_SHIPPED)

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		if err := postgres.UpdateOrderShipment(orderId, shipmentResp.ShipmentId, shipmentResp.TrackingNumber); err != nil { return nil, err }
		return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_SHIPPED)
	})
	if err != nil { log.Printf("[Workflow %s] Warning: Failed to update shipment in database: %v", orderId, err) }

	log.Printf("[Workflow %s] Order is now SHIPPED and in transit", orderId)
	log.Printf("[Workflow %s] Tracking number: %s", orderId, shipmentResp.TrackingNumber)

	log.Printf("[Workflow %s] Simulating delivery time (10 second delay)", orderId)
	if err := restate.Sleep(ctx, 10*time.Second); err != nil { return nil, fmt.Errorf("sleep interrupted: %w", err) }

	log.Printf("[Workflow %s] Order has been delivered", orderId)
	restate.Set(ctx, "status", orderpb.OrderStatus_DELIVERED)
	restate.Set(ctx, "current_location", "Delivered")

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) { return nil, postgres.UpdateOrderStatusDB(orderId, orderpb.OrderStatus_DELIVERED) })
	if err != nil { log.Printf("[Workflow %s] Warning: Failed to update final status in database: %v", orderId, err) }

	log.Printf("[Workflow %s] Order completed successfully - Status: DELIVERED", orderId)
	return &orderpb.CreateOrderResponse{OrderId: orderId}, nil
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
		if err != nil { paymentStatusVal = orderpb.PaymentStatus_PAYMENT_COMPLETED }
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
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) { return nil, postgres.UpdateOrderStatusDB(orderId, req.Status) })
	if err != nil {
		log.Printf("[Workflow %s] Warning: Failed to update status in database: %v", orderId, err)
		return &orderpb.UpdateOrderStatusResponse{Success: false, Message: fmt.Sprintf("Failed to update status in database: %v", err)}, nil
	}
	return &orderpb.UpdateOrderStatusResponse{Success: true, Message: "Order status updated successfully"}, nil
}

// Keep a tiny server import linker reference to avoid unused import errors when building only package
var _ = server.NewRestate

// Dummy ref to rand to match original imports where used in payment; remove if unnecessary later
var _ = rand.Intn
