package order

import (
    "context"
    "database/sql"

    orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
    postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

// repo is a package-level pointer to support gradual migration to DI without
// rewriting all handler signatures. It is set from main via SetRepository.
var repo *postgres.Repository

func SetRepository(r *postgres.Repository) { repo = r }

func getDB() *sql.DB {
    if repo != nil {
        return repo.DB
    }
    return nil
}

func getRepo() *postgres.Repository { return repo }

// Wrapper helpers: use injected repository when available, otherwise fall back
// to legacy package-level functions that operate on postgres.DB. These keep
// tests and non-Fx contexts working.

func repoDeductStock(merchantID string, items []*orderpb.OrderItems) error {
    if repo != nil {
        return repo.DeductStockFromItems(merchantID, items)
    }
    return postgres.DeductStockFromItems(merchantID, items)
}

func repoRestoreStock(merchantID string, items []*orderpb.OrderItems) error {
    if repo != nil {
        return repo.RestoreStockToItems(merchantID, items)
    }
    return postgres.RestoreStockToItems(merchantID, items)
}

func repoInsertOrder(ctx context.Context, orderID, customerID, merchantID string, status orderpb.OrderStatus, totalAmount float64) error {
    if repo != nil {
        return repo.InsertOrder(ctx, orderID, customerID, merchantID, status, totalAmount)
    }
    return postgres.InsertOrder(ctx, orderID, customerID, merchantID, status, totalAmount)
}

func repoInsertOrderItems(orderID, merchantID string, items []*orderpb.OrderItems) error {
    if repo != nil {
        return repo.InsertOrderItems(orderID, merchantID, items)
    }
    return postgres.InsertOrderItems(orderID, merchantID, items)
}

func repoUpdateOrderPayment(orderID, paymentID string) error {
    if repo != nil {
        return repo.UpdateOrderPayment(orderID, paymentID)
    }
    return postgres.UpdateOrderPayment(orderID, paymentID)
}

func repoUpdateOrderStatus(orderID string, status orderpb.OrderStatus) error {
    if repo != nil {
        return repo.UpdateOrderStatus(orderID, status)
    }
    return postgres.UpdateOrderStatusDB(orderID, status)
}

// Shipment helpers
func repoInsertShipment(shipmentID, orderID, trackingNumber, carrier, serviceType string, status orderpb.ShipmentStatus, currentLocation, estimatedDelivery string) error {
    if repo != nil {
        return repo.InsertShipment(shipmentID, orderID, trackingNumber, carrier, serviceType, status, currentLocation, estimatedDelivery)
    }
    return postgres.InsertShipment(shipmentID, orderID, trackingNumber, carrier, serviceType, status, currentLocation, estimatedDelivery)
}

func repoUpdateOrderShipment(orderID, shipmentID, trackingNumber string) error {
    if repo != nil {
        return repo.UpdateOrderShipment(orderID, shipmentID, trackingNumber)
    }
    return postgres.UpdateOrderShipment(orderID, shipmentID, trackingNumber)
}

func repoUpdateShipmentStatus(shipmentID string, status orderpb.ShipmentStatus, currentLocation string) error {
    if repo != nil {
        return repo.UpdateShipmentStatus(shipmentID, status, currentLocation)
    }
    return postgres.UpdateShipmentStatus(shipmentID, status, currentLocation)
}
