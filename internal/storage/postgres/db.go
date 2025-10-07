package postgres

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/merchant/v1"
	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	_ "github.com/lib/pq"
)

// DatabaseConfig holds the database connection configuration
type DatabaseConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// DB is the global database connection pool
var DB *sql.DB

// InitDatabase initializes the database connection pool and creates tables
func InitDatabase(config DatabaseConfig) error {
	// Build connection string
	connStr := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		config.Host,
		config.Port,
		config.Database,
		config.User,
		config.Password,
	)

	// Open database connection
	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Successfully connected to PostgreSQL database: %s", config.Database)

	// Schema is managed by migrations; do not create tables at runtime

	return nil
}

// createTables removed; schema is managed via migrations

// CloseDatabase closes the database connection
func CloseDatabase() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

// Order ops
func InsertOrder(orderID, customerID, merchantID string, status orderpb.OrderStatus, totalAmount float64) error {
	query := `
		INSERT INTO orders (id, customer_id, status, total_amount)
        VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			status = EXCLUDED.status,
			total_amount = EXCLUDED.total_amount,
			updated_at = CURRENT_TIMESTAMP
	`
	// Update merchant_id separately to keep backward compatibility with existing params order
	_, err := DB.Exec(query, orderID, customerID, status.String(), totalAmount)
	if err != nil {
		return fmt.Errorf("failed to insert order: %w", err)
	}
	if merchantID != "" {
		if _, err := DB.Exec(`UPDATE orders SET merchant_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, merchantID, orderID); err != nil {
			return fmt.Errorf("failed to set order merchant_id: %w", err)
		}
	}
	log.Printf("[DB] Inserted/Updated order: %s", orderID)
	return nil
}

func InsertOrderItems(orderID, merchantID string, items []*orderpb.OrderItems) error {
	if len(items) == 0 {
		return nil
	}
	for _, it := range items {
		qty := it.Quantity
		log.Printf("[DEBUG] InsertOrderItems: processing item - ProductId='%s', Quantity=%d, merchantID='%s'", it.ProductId, qty, merchantID)
		// Try to fetch item name and price from merchant_items table
		var name string
		var unit float64 = 1.00
		if DB != nil && merchantID != "" {
			_ = DB.QueryRow(`SELECT name, price FROM merchant_items WHERE merchant_id = $1 AND item_id = $2`, merchantID, it.ProductId).
				Scan(&name, &unit)
			log.Printf("[DEBUG] InsertOrderItems: lookup result - name='%s', price=%.2f", name, unit)
		}
		if name == "" {
			name = it.ProductId
		}
		subtotal := float64(qty) * unit
		_, err := DB.Exec(`
            INSERT INTO order_items (order_id, item_id, merchant_id, name, quantity, unit_price, subtotal)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            ON CONFLICT (order_id, item_id) DO UPDATE SET
                merchant_id = EXCLUDED.merchant_id,
                name = EXCLUDED.name,
                quantity = EXCLUDED.quantity,
                unit_price = EXCLUDED.unit_price,
                subtotal = EXCLUDED.subtotal,
                updated_at = CURRENT_TIMESTAMP
        `, orderID, it.ProductId, merchantID, name, qty, unit, subtotal)
		if err != nil {
			return fmt.Errorf("failed to insert order item %s: %w", it.ProductId, err)
		}
	}
	log.Printf("[DB] Inserted/Updated %d order items for order: %s", len(items), orderID)
	return nil
}

func UpdateOrderStatusDB(orderID string, status orderpb.OrderStatus) error {
	query := `
		UPDATE orders
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`
	result, err := DB.Exec(query, status.String(), orderID)
	if err != nil {
		return fmt.Errorf("failed to update order status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("order not found: %s", orderID)
	}
	log.Printf("[DB] Updated order status: %s -> %s", orderID, status.String())
	return nil
}

func UpdateOrderPayment(orderID, paymentID string) error {
	query := `
		UPDATE orders
		SET payment_id = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`
	_, err := DB.Exec(query, paymentID, orderID)
	if err != nil {
		return fmt.Errorf("failed to update order payment: %w", err)
	}
	log.Printf("[DB] Updated order payment: %s -> %s", orderID, paymentID)
	return nil
}

func UpdateOrderShipment(orderID, shipmentID, trackingNumber string) error {
	query := `
		UPDATE orders
		SET shipment_id = $1, tracking_number = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`
	_, err := DB.Exec(query, shipmentID, trackingNumber, orderID)
	if err != nil {
		return fmt.Errorf("failed to update order shipment: %w", err)
	}
	log.Printf("[DB] Updated order shipment: %s -> %s", orderID, shipmentID)
	return nil
}

// Payment ops
func InsertPayment(paymentID, orderID string, amount float64, paymentMethod string, status orderpb.PaymentStatus) error {
	query := `
		INSERT INTO payments (id, order_id, amount, payment_method, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			order_id = EXCLUDED.order_id,
			amount = EXCLUDED.amount,
			payment_method = EXCLUDED.payment_method,
			status = EXCLUDED.status,
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := DB.Exec(query, paymentID, orderID, amount, paymentMethod, status.String())
	if err != nil {
		return fmt.Errorf("failed to insert payment: %w", err)
	}
	log.Printf("[DB] Inserted/Updated payment: %s", paymentID)
	return nil
}

func UpdatePaymentStatus(paymentID string, status orderpb.PaymentStatus) error {
	query := `
		UPDATE payments
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`
	_, err := DB.Exec(query, status.String(), paymentID)
	if err != nil {
		return fmt.Errorf("failed to update payment status: %w", err)
	}
	log.Printf("[DB] Updated payment status: %s -> %s", paymentID, status.String())
	return nil
}

// Optional helpers for invoice persistence
func UpdatePaymentInvoiceInfo(paymentID, invoiceURL, xenditInvoiceID string) error {
	query := `
        UPDATE payments
        SET invoice_url = $1, xendit_invoice_id = $2, updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `
	_, err := DB.Exec(query, invoiceURL, xenditInvoiceID, paymentID)
	if err != nil {
		return fmt.Errorf("failed to update payment invoice info: %w", err)
	}
	log.Printf("[DB] Updated payment invoice info: %s", paymentID)
	return nil
}

// Shipment ops
func InsertShipment(shipmentID, orderID, trackingNumber, carrier, serviceType string, status orderpb.ShipmentStatus, currentLocation, estimatedDelivery string) error {
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
	_, err := DB.Exec(query, shipmentID, orderID, trackingNumber, carrier, serviceType, status.String(), currentLocation, estimatedDelivery)
	if err != nil {
		return fmt.Errorf("failed to insert shipment: %w", err)
	}
	log.Printf("[DB] Inserted/Updated shipment: %s", shipmentID)
	return nil
}

func UpdateShipmentStatus(shipmentID string, status orderpb.ShipmentStatus, currentLocation string) error {
	query := `
		UPDATE shipments
		SET status = $1, current_location = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`
	_, err := DB.Exec(query, status.String(), currentLocation, shipmentID)
	if err != nil {
		return fmt.Errorf("failed to update shipment status: %w", err)
	}
	log.Printf("[DB] Updated shipment status: %s -> %s", shipmentID, status.String())
	return nil
}

// Merchant ops
func GetMerchantItems(merchantID string) ([]*merchantpb.Item, error) {
	if DB == nil {
		return []*merchantpb.Item{}, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT item_id, name, quantity, price
		FROM merchant_items
		WHERE merchant_id = $1
		ORDER BY item_id
	`
	rows, err := DB.Query(query, merchantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query merchant items: %w", err)
	}
	defer rows.Close()

	var items []*merchantpb.Item
	for rows.Next() {
		var item merchantpb.Item
		err := rows.Scan(&item.ItemId, &item.Name, &item.Quantity, &item.Price)
		if err != nil {
			return nil, fmt.Errorf("failed to scan merchant item: %w", err)
		}
		items = append(items, &item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating merchant items: %w", err)
	}

	log.Printf("[DB] Retrieved %d items for merchant: %s", len(items), merchantID)
	return items, nil
}
