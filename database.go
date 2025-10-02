package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

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

	// Create tables
	if err := createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}

// createTables creates the necessary database tables
func createTables() error {
	// Create orders table
	ordersTable := `
	CREATE TABLE IF NOT EXISTS orders (
		id VARCHAR(255) PRIMARY KEY,
		customer_id VARCHAR(255) NOT NULL,
		status VARCHAR(50) NOT NULL,
		total_amount DECIMAL(10, 2),
		payment_id VARCHAR(255),
		shipment_id VARCHAR(255),
		tracking_number VARCHAR(255),
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_orders_customer_id ON orders(customer_id);
	CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
	`

	if _, err := DB.Exec(ordersTable); err != nil {
		return fmt.Errorf("failed to create orders table: %w", err)
	}

	// Create payments table
	paymentsTable := `
	CREATE TABLE IF NOT EXISTS payments (
		id VARCHAR(255) PRIMARY KEY,
		order_id VARCHAR(255) NOT NULL,
		amount DECIMAL(10, 2) NOT NULL,
		payment_method VARCHAR(50),
		status VARCHAR(50) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_payments_order_id ON payments(order_id);
	CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);
	`

	if _, err := DB.Exec(paymentsTable); err != nil {
		return fmt.Errorf("failed to create payments table: %w", err)
	}

	// Create shipments table
	shipmentsTable := `
	CREATE TABLE IF NOT EXISTS shipments (
		id VARCHAR(255) PRIMARY KEY,
		order_id VARCHAR(255) NOT NULL,
		tracking_number VARCHAR(255) NOT NULL,
		carrier VARCHAR(100),
		service_type VARCHAR(100),
		status VARCHAR(50) NOT NULL,
		current_location VARCHAR(255),
		estimated_delivery DATE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_shipments_order_id ON shipments(order_id);
	CREATE INDEX IF NOT EXISTS idx_shipments_tracking_number ON shipments(tracking_number);
	CREATE INDEX IF NOT EXISTS idx_shipments_status ON shipments(status);
	`

	if _, err := DB.Exec(shipmentsTable); err != nil {
		return fmt.Errorf("failed to create shipments table: %w", err)
	}

	log.Println("Database tables created successfully")
	return nil
}

// CloseDatabase closes the database connection
func CloseDatabase() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

// ============================================================================
// Order Database Operations
// ============================================================================

// InsertOrder inserts a new order into the database
func InsertOrder(orderID, customerID string, status orderpb.OrderStatus, totalAmount float64) error {
	query := `
		INSERT INTO orders (id, customer_id, status, total_amount)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			status = EXCLUDED.status,
			total_amount = EXCLUDED.total_amount,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := DB.Exec(query, orderID, customerID, status.String(), totalAmount)
	if err != nil {
		return fmt.Errorf("failed to insert order: %w", err)
	}

	log.Printf("[DB] Inserted/Updated order: %s", orderID)
	return nil
}

// UpdateOrderStatusDB updates the order status in the database
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

// UpdateOrderPayment updates the payment information for an order
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

// UpdateOrderShipment updates the shipment information for an order
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

// ============================================================================
// Payment Database Operations
// ============================================================================

// InsertPayment inserts a new payment into the database
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

// UpdatePaymentStatus updates the payment status in the database
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

// ============================================================================
// Shipment Database Operations
// ============================================================================

// InsertShipment inserts a new shipment into the database
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

// UpdateShipmentStatus updates the shipment status in the database
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

// ============================================================================
// Comprehensive Order Query Operations
// ============================================================================

// OrderDetails represents comprehensive order information from database
type OrderDetails struct {
	OrderID           string
	CustomerID        string
	OrderStatus       string
	TotalAmount       float64
	PaymentID         *string
	PaymentStatus     *string
	PaymentAmount     *float64
	PaymentMethod     *string
	ShipmentID        *string
	TrackingNumber    *string
	Carrier           *string
	ShipmentStatus    *string
	CurrentLocation   *string
	EstimatedDelivery *string
	OrderCreatedAt    string
	OrderUpdatedAt    string
}

// GetOrderDetails retrieves comprehensive order information with JOINs
func GetOrderDetails(orderID string) (*OrderDetails, error) {
	query := `
		SELECT
			o.id as order_id,
			o.customer_id,
			o.status as order_status,
			o.total_amount,
			o.created_at as order_created_at,
			o.updated_at as order_updated_at,
			p.id as payment_id,
			p.status as payment_status,
			p.amount as payment_amount,
			p.payment_method,
			s.id as shipment_id,
			s.tracking_number,
			s.carrier,
			s.status as shipment_status,
			s.current_location,
			s.estimated_delivery
		FROM orders o
		LEFT JOIN payments p ON o.payment_id = p.id
		LEFT JOIN shipments s ON o.shipment_id = s.id
		WHERE o.id = $1
	`

	var details OrderDetails
	var orderCreatedAt, orderUpdatedAt, estimatedDelivery interface{}

	err := DB.QueryRow(query, orderID).Scan(
		&details.OrderID,
		&details.CustomerID,
		&details.OrderStatus,
		&details.TotalAmount,
		&orderCreatedAt,
		&orderUpdatedAt,
		&details.PaymentID,
		&details.PaymentStatus,
		&details.PaymentAmount,
		&details.PaymentMethod,
		&details.ShipmentID,
		&details.TrackingNumber,
		&details.Carrier,
		&details.ShipmentStatus,
		&details.CurrentLocation,
		&estimatedDelivery,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("order not found: %s", orderID)
		}
		return nil, fmt.Errorf("failed to query order details: %w", err)
	}

	// Convert timestamps to strings
	if t, ok := orderCreatedAt.(time.Time); ok {
		details.OrderCreatedAt = t.Format(time.RFC3339)
	}
	if t, ok := orderUpdatedAt.(time.Time); ok {
		details.OrderUpdatedAt = t.Format(time.RFC3339)
	}
	if t, ok := estimatedDelivery.(time.Time); ok {
		deliveryStr := t.Format("2006-01-02")
		details.EstimatedDelivery = &deliveryStr
	}

	log.Printf("[DB] Retrieved order details: %s (Status: %s)", orderID, details.OrderStatus)
	return &details, nil
}

// GetOrdersByCustomer retrieves all orders for a specific customer
func GetOrdersByCustomer(customerID string) ([]OrderDetails, error) {
	query := `
		SELECT
			o.id as order_id,
			o.customer_id,
			o.status as order_status,
			o.total_amount,
			o.created_at as order_created_at,
			o.updated_at as order_updated_at,
			p.id as payment_id,
			p.status as payment_status,
			p.amount as payment_amount,
			p.payment_method,
			s.id as shipment_id,
			s.tracking_number,
			s.carrier,
			s.status as shipment_status,
			s.current_location,
			s.estimated_delivery
		FROM orders o
		LEFT JOIN payments p ON o.payment_id = p.id
		LEFT JOIN shipments s ON o.shipment_id = s.id
		WHERE o.customer_id = $1
		ORDER BY o.created_at DESC
	`

	rows, err := DB.Query(query, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query orders by customer: %w", err)
	}
	defer rows.Close()

	var orders []OrderDetails
	for rows.Next() {
		var details OrderDetails
		var orderCreatedAt, orderUpdatedAt, estimatedDelivery interface{}

		err := rows.Scan(
			&details.OrderID,
			&details.CustomerID,
			&details.OrderStatus,
			&details.TotalAmount,
			&orderCreatedAt,
			&orderUpdatedAt,
			&details.PaymentID,
			&details.PaymentStatus,
			&details.PaymentAmount,
			&details.PaymentMethod,
			&details.ShipmentID,
			&details.TrackingNumber,
			&details.Carrier,
			&details.ShipmentStatus,
			&details.CurrentLocation,
			&estimatedDelivery,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order row: %w", err)
		}

		// Convert timestamps
		if t, ok := orderCreatedAt.(time.Time); ok {
			details.OrderCreatedAt = t.Format(time.RFC3339)
		}
		if t, ok := orderUpdatedAt.(time.Time); ok {
			details.OrderUpdatedAt = t.Format(time.RFC3339)
		}
		if t, ok := estimatedDelivery.(time.Time); ok {
			deliveryStr := t.Format("2006-01-02")
			details.EstimatedDelivery = &deliveryStr
		}

		orders = append(orders, details)
	}

	log.Printf("[DB] Retrieved %d orders for customer: %s", len(orders), customerID)
	return orders, nil
}
