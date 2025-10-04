package shipping

import (
	"fmt"
	"log"

	"github.com/google/uuid"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	restate "github.com/restatedev/sdk-go"

	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// CreateShipment is a virtual object handler for creating shipments
func CreateShipment(ctx restate.ObjectContext, req *orderpb.CreateShipmentRequest) (*orderpb.CreateShipmentResponse, error) {
	shipmentId := restate.Key(ctx)
	log.Printf("[Shipping Object %s] Creating shipment for order: %s", shipmentId, req.OrderId)

	existingStatus, err := restate.Get[orderpb.ShipmentStatus](ctx, "status")
	if err == nil {
		log.Printf("[Shipping Object %s] Shipment already exists with status: %v", shipmentId, existingStatus)
		trackingNumber, _ := restate.Get[string](ctx, "tracking_number")
		estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")
		return &orderpb.CreateShipmentResponse{ShipmentId: shipmentId, TrackingNumber: trackingNumber, EstimatedDelivery: estimatedDelivery}, nil
	}

	trackingNumber := fmt.Sprintf("TRACK-%s", uuid.New().String()[:8])
	estimatedDelivery := "2025-10-10"
	currentLocation := "Warehouse"

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Shipping Object %s] Creating shipment with carrier: %s", shipmentId, req.ShippingMethod.Carrier)
		log.Printf("[Shipping Object %s] Shipment creation with provider completed", shipmentId)
		return nil, nil
	})
	if err != nil { return nil, fmt.Errorf("shipment creation failed: %w", err) }

	status := orderpb.ShipmentStatus_SHIPMENT_CREATED
	restate.Set(ctx, "status", status)
	restate.Set(ctx, "order_id", req.OrderId)
	restate.Set(ctx, "tracking_number", trackingNumber)
	restate.Set(ctx, "estimated_delivery", estimatedDelivery)
	restate.Set(ctx, "current_location", currentLocation)

	_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		log.Printf("[Shipping Object %s] Persisting shipment to database", shipmentId)
		return nil, postgres.InsertShipment(shipmentId, req.OrderId, trackingNumber, req.ShippingMethod.Carrier, req.ShippingMethod.ServiceType, status, currentLocation, estimatedDelivery)
	})
	if err != nil { log.Printf("[Shipping Object %s] Warning: Failed to persist shipment to database: %v", shipmentId, err) }

	log.Printf("[Shipping Object %s] Shipment created successfully", shipmentId)
	return &orderpb.CreateShipmentResponse{ShipmentId: shipmentId, TrackingNumber: trackingNumber, EstimatedDelivery: estimatedDelivery}, nil
}

// TrackShipment is a shared (read-only) handler for tracking shipments
func TrackShipment(ctx restate.ObjectSharedContext, req *orderpb.TrackShipmentRequest) (*orderpb.TrackShipmentResponse, error) {
	shipmentId := restate.Key(ctx)
	log.Printf("[Shipping Object %s] Tracking shipment", shipmentId)

	status, err := restate.Get[orderpb.ShipmentStatus](ctx, "status")
	if err != nil { return nil, fmt.Errorf("shipment not found: %w", err) }
	currentLocation, _ := restate.Get[string](ctx, "current_location")
	estimatedDelivery, _ := restate.Get[string](ctx, "estimated_delivery")
	return &orderpb.TrackShipmentResponse{ShipmentId: shipmentId, Status: status, CurrentLocation: currentLocation, EstimatedDelivery: estimatedDelivery, Events: []*orderpb.ShipmentEvent{}}, nil
}
