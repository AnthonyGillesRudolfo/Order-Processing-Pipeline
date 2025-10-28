package postgres

import (
    "context"
    "database/sql"
    "fmt"
    "log"

    orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
)

// Repository is a thin wrapper around *sql.DB intended for dependency injection.
// Over time, prefer adding methods here instead of using package-level globals.
type Repository struct {
    DB *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
    return &Repository{DB: db}
}

// InsertOrder inserts or upserts an order row.
func (r *Repository) InsertOrder(ctx context.Context, orderID, customerID, merchantID string, status orderpb.OrderStatus, totalAmount float64) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        INSERT INTO orders (id, customer_id, status, total_amount)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (id) DO UPDATE SET
            customer_id = EXCLUDED.customer_id,
            status = EXCLUDED.status,
            total_amount = EXCLUDED.total_amount,
            updated_at = CURRENT_TIMESTAMP
    `
    if _, err := r.DB.ExecContext(ctx, query, orderID, customerID, status.String(), totalAmount); err != nil {
        return fmt.Errorf("failed to insert order: %w", err)
    }
    if merchantID != "" {
        updateQuery := `UPDATE orders SET merchant_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`
        if _, err := r.DB.ExecContext(ctx, updateQuery, merchantID, orderID); err != nil {
            return fmt.Errorf("failed to set order merchant_id: %w", err)
        }
    }
    log.Printf("[DB] Inserted/Updated order: %s", orderID)
    return nil
}

// InsertOrderItems inserts or upserts order items.
func (r *Repository) InsertOrderItems(orderID, merchantID string, items []*orderpb.OrderItems) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    if len(items) == 0 {
        return nil
    }
    for _, it := range items {
        var name string
        var unit float64 = 1.00
        if merchantID != "" {
            _ = r.DB.QueryRow(`SELECT name, price FROM merchant_items WHERE merchant_id = $1 AND item_id = $2`, merchantID, it.ProductId).
                Scan(&name, &unit)
        }
        if name == "" {
            name = it.ProductId
        }
        subtotal := float64(it.Quantity) * unit
        _, err := r.DB.Exec(`
            INSERT INTO order_items (order_id, item_id, merchant_id, name, quantity, unit_price, subtotal)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            ON CONFLICT (order_id, item_id) DO UPDATE SET
                merchant_id = EXCLUDED.merchant_id,
                name = EXCLUDED.name,
                quantity = EXCLUDED.quantity,
                unit_price = EXCLUDED.unit_price,
                subtotal = EXCLUDED.subtotal,
                updated_at = CURRENT_TIMESTAMP
        `, orderID, it.ProductId, merchantID, name, it.Quantity, unit, subtotal)
        if err != nil {
            return fmt.Errorf("failed to insert order item %s: %w", it.ProductId, err)
        }
    }
    log.Printf("[DB] Inserted/Updated %d order items for order: %s", len(items), orderID)
    return nil
}

// UpdateOrderStatus updates the order status.
func (r *Repository) UpdateOrderStatus(orderID string, status orderpb.OrderStatus) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        UPDATE orders
        SET status = $1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $2
    `
    res, err := r.DB.Exec(query, status.String(), orderID)
    if err != nil {
        return fmt.Errorf("failed to update order status: %w", err)
    }
    rows, _ := res.RowsAffected()
    if rows == 0 {
        return fmt.Errorf("order not found: %s", orderID)
    }
    log.Printf("[DB] Updated order status: %s -> %s", orderID, status.String())
    return nil
}

// UpdateOrderPayment sets the payment_id on the order.
func (r *Repository) UpdateOrderPayment(orderID, paymentID string) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        UPDATE orders
        SET payment_id = $1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $2
    `
    if _, err := r.DB.Exec(query, paymentID, orderID); err != nil {
        return fmt.Errorf("failed to update order payment: %w", err)
    }
    log.Printf("[DB] Updated order payment: %s -> %s", orderID, paymentID)
    return nil
}

// DeductStockFromItems atomically deducts stock for the items.
func (r *Repository) DeductStockFromItems(merchantID string, items []*orderpb.OrderItems) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    tx, err := r.DB.Begin()
    if err != nil {
        return fmt.Errorf("failed to start transaction: %w", err)
    }
    defer tx.Rollback()
    for _, item := range items {
        var currentStock int32
        err := tx.QueryRow(`
            SELECT quantity FROM merchant_items 
            WHERE merchant_id = $1 AND item_id = $2
        `, merchantID, item.ProductId).Scan(&currentStock)
        if err != nil {
            return fmt.Errorf("failed to check stock for item %s: %w", item.ProductId, err)
        }
        if currentStock < item.Quantity {
            return fmt.Errorf("insufficient stock for item %s: requested %d, available %d", item.ProductId, item.Quantity, currentStock)
        }
        if _, err := tx.Exec(`
            UPDATE merchant_items 
            SET quantity = quantity - $3, updated_at = CURRENT_TIMESTAMP
            WHERE merchant_id = $1 AND item_id = $2
        `, merchantID, item.ProductId, item.Quantity); err != nil {
            return fmt.Errorf("failed to deduct stock for item %s: %w", item.ProductId, err)
        }
    }
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit stock deduction transaction: %w", err)
    }
    log.Printf("[DB] Successfully deducted stock for %d items for merchant: %s", len(items), merchantID)
    return nil
}

// RestoreStockToItems atomically restores stock for the items.
func (r *Repository) RestoreStockToItems(merchantID string, items []*orderpb.OrderItems) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    tx, err := r.DB.Begin()
    if err != nil {
        return fmt.Errorf("failed to start transaction: %w", err)
    }
    defer tx.Rollback()
    for _, item := range items {
        if _, err := tx.Exec(`
            UPDATE merchant_items 
            SET quantity = quantity + $3, updated_at = CURRENT_TIMESTAMP
            WHERE merchant_id = $1 AND item_id = $2
        `, merchantID, item.ProductId, item.Quantity); err != nil {
            return fmt.Errorf("failed to restore stock for item %s: %w", item.ProductId, err)
        }
    }
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit stock restoration transaction: %w", err)
    }
    log.Printf("[DB] Successfully restored stock for %d items for merchant: %s", len(items), merchantID)
    return nil
}

// InsertShipment creates or upserts a shipment row for an order.
func (r *Repository) InsertShipment(shipmentID, orderID, trackingNumber, carrier, serviceType string, status orderpb.ShipmentStatus, currentLocation, estimatedDelivery string) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        INSERT INTO shipments (id, order_id, tracking_number, carrier, service_type, status, current_location, estimated_delivery)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (id) DO UPDATE SET
            order_id = EXCLUDED.order_id,
            tracking_number = EXCLUDED.tracking_number,
            carrier = EXCLUDED.carrier,
            service_type = EXCLUDED.service_type,
            status = EXCLUDED.status,
            current_location = EXCLUDED.current_location,
            estimated_delivery = EXCLUDED.estimated_delivery,
            updated_at = CURRENT_TIMESTAMP
    `
    if _, err := r.DB.Exec(query, shipmentID, orderID, trackingNumber, carrier, serviceType, status.String(), currentLocation, estimatedDelivery); err != nil {
        return fmt.Errorf("failed to insert shipment: %w", err)
    }
    log.Printf("[DB] Inserted/Updated shipment: %s", shipmentID)
    return nil
}

// UpdateOrderShipment sets shipment info on an order.
func (r *Repository) UpdateOrderShipment(orderID, shipmentID, trackingNumber string) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        UPDATE orders
        SET shipment_id = $1, tracking_number = $2, updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `
    if _, err := r.DB.Exec(query, shipmentID, trackingNumber, orderID); err != nil {
        return fmt.Errorf("failed to update order shipment: %w", err)
    }
    log.Printf("[DB] Updated order shipment: %s -> %s", orderID, shipmentID)
    return nil
}

// UpdateShipmentStatus updates the current location and status of a shipment.
func (r *Repository) UpdateShipmentStatus(shipmentID string, status orderpb.ShipmentStatus, currentLocation string) error {
    if r.DB == nil {
        return fmt.Errorf("database not initialized")
    }
    query := `
        UPDATE shipments
        SET status = $1, current_location = $2, updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `
    if _, err := r.DB.Exec(query, status.String(), currentLocation, shipmentID); err != nil {
        return fmt.Errorf("failed to update shipment status: %w", err)
    }
    log.Printf("[DB] Updated shipment status: %s -> %s", shipmentID, status.String())
    return nil
}

// GetOrderWithPayment returns consolidated order + payment details by order ID.
func (r *Repository) GetOrderWithPayment(orderID string) (map[string]any, error) {
    if r.DB == nil {
        return nil, fmt.Errorf("database not initialized")
    }
    row := r.DB.QueryRow(`
        SELECT o.id, o.customer_id, o.status, o.total_amount, o.payment_id, o.shipment_id, o.tracking_number, o.updated_at,
               COALESCE(p.status, '') AS payment_status, COALESCE(p.invoice_url, '') AS invoice_url
        FROM orders o
        LEFT JOIN payments p ON p.id = o.payment_id
        WHERE o.id = $1
    `, orderID)

    var (
        id, customerID, status, paymentID string
        shipmentID, trackingNumber        sql.NullString
        totalAmount                       sql.NullFloat64
        updatedAt                         string
        paymentStatus                     string
        invoiceURL                        string
    )
    if err := row.Scan(&id, &customerID, &status, &totalAmount, &paymentID, &shipmentID, &trackingNumber, &updatedAt, &paymentStatus, &invoiceURL); err != nil {
        return nil, err
    }

    return map[string]any{
        "order": map[string]any{
            "id":              id,
            "customer_id":     customerID,
            "status":          status,
            "total_amount":    totalAmount.Float64,
            "payment_id":      paymentID,
            "shipment_id":     shipmentID.String,
            "tracking_number": trackingNumber.String,
            "updated_at":      updatedAt,
        },
        "payment": map[string]any{
            "status":      paymentStatus,
            "invoice_url": invoiceURL,
        },
    }, nil
}

// GetOrderWithPaymentByPaymentID returns consolidated order + payment details by payment ID.
func (r *Repository) GetOrderWithPaymentByPaymentID(paymentID string) (map[string]any, error) {
    if r.DB == nil {
        return nil, fmt.Errorf("database not initialized")
    }
    row := r.DB.QueryRow(`
        SELECT o.id, o.customer_id, o.status, o.total_amount, o.payment_id, o.shipment_id, o.tracking_number, o.updated_at,
               COALESCE(p.status, '') AS payment_status, COALESCE(p.invoice_url, '') AS invoice_url
        FROM orders o
        LEFT JOIN payments p ON p.id = o.payment_id
        WHERE o.payment_id = $1
    `, paymentID)

    var (
        id, customerID, status, paymentIDResult string
        shipmentID, trackingNumber              sql.NullString
        totalAmount                             sql.NullFloat64
        updatedAt                               string
        paymentStatus                           string
        invoiceURL                              string
    )
    if err := row.Scan(&id, &customerID, &status, &totalAmount, &paymentIDResult, &shipmentID, &trackingNumber, &updatedAt, &paymentStatus, &invoiceURL); err != nil {
        return nil, err
    }

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
        },
        "payment": map[string]any{
            "status":      paymentStatus,
            "invoice_url": invoiceURL,
        },
    }, nil
}
