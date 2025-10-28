package postgres

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "time"
    "sync"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/merchant/v1"
	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
	_ "github.com/lib/pq"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
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
var dbMu sync.Mutex

// tracer for database operations
var tracer trace.Tracer

// initTracer initializes the database tracer
func initTracer() {
	tracer = otel.Tracer("order-processing-pipeline/database")
}

// traceDBOperation creates a span for database operations
func traceDBOperation(ctx context.Context, operation, query string) (context.Context, trace.Span) {
	if tracer == nil {
		initTracer()
	}

	spanName := fmt.Sprintf("db.%s", operation)
	ctx, span := tracer.Start(ctx, spanName)

	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", operation),
		attribute.String("db.statement", query),
		semconv.DBSystemPostgreSQL,
	)

	return ctx, span
}

// InitDatabase initializes the database connection pool and creates tables
func InitDatabase(config DatabaseConfig) error {
    // Initialize tracer
    initTracer()

    // Use the shared open helper
    _, err := OpenDatabase(config)
    return err
}

// createTables removed; schema is managed via migrations

// CloseDatabase closes the database connection
func CloseDatabase() error {
    dbMu.Lock()
    defer dbMu.Unlock()
    if DB != nil {
        err := DB.Close()
        DB = nil
        return err
    }
    return nil
}

// OpenDatabase opens and configures a PostgreSQL connection, assigns it to the
// package global DB for backward compatibility, and returns the *sql.DB handle.
func OpenDatabase(config DatabaseConfig) (*sql.DB, error) {
    dbMu.Lock()
    defer dbMu.Unlock()

    if DB != nil {
        return DB, nil
    }

    // Initialize tracer
    initTracer()

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
    db, err := sql.Open("postgres", connStr)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    // Configure connection pool
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(5 * time.Minute)

    // Test connection
    if err := db.Ping(); err != nil {
        _ = db.Close()
        return nil, fmt.Errorf("failed to ping database: %w", err)
    }

    // Assign to package-level for existing call sites
    DB = db

    log.Printf("Successfully connected to PostgreSQL database: %s", config.Database)
    return db, nil
}

// Order ops
func InsertOrder(ctx context.Context, orderID, customerID, merchantID string, status orderpb.OrderStatus, totalAmount float64) error {
	query := `
		INSERT INTO orders (id, customer_id, status, total_amount)
        VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			status = EXCLUDED.status,
			total_amount = EXCLUDED.total_amount,
			updated_at = CURRENT_TIMESTAMP
	`

	ctx, span := traceDBOperation(ctx, "insert", query)
	defer span.End()

	// Update merchant_id separately to keep backward compatibility with existing params order
	_, err := DB.ExecContext(ctx, query, orderID, customerID, status.String(), totalAmount)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to insert order: %w", err)
	}
	if merchantID != "" {
		updateQuery := `UPDATE orders SET merchant_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`
		ctx, updateSpan := traceDBOperation(ctx, "update", updateQuery)
		_, err := DB.ExecContext(ctx, updateQuery, merchantID, orderID)
		updateSpan.End()
		if err != nil {
			updateSpan.RecordError(err)
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
		// Try to fetch item name and price from merchant_items table
		var name string
		var unit float64 = 1.00
		if DB != nil && merchantID != "" {
			_ = DB.QueryRow(`SELECT name, price FROM merchant_items WHERE merchant_id = $1 AND item_id = $2`, merchantID, it.ProductId).
				Scan(&name, &unit)
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

// GetPaymentByOrderID retrieves payment information by order ID
func GetPaymentByOrderID(orderID string) (*struct {
	ID            string
	OrderID       string
	Amount        float64
	PaymentMethod string
	Status        string
	InvoiceURL    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, orderIDResult, paymentMethod, status, invoiceURL string
	var amount float64
	var createdAt, updatedAt time.Time

	err := DB.QueryRow(`
		SELECT id, order_id, amount, payment_method, status, invoice_url, created_at, updated_at
		FROM payments
		WHERE order_id = $1
	`, orderID).Scan(&id, &orderIDResult, &amount, &paymentMethod, &status, &invoiceURL, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	payment := &struct {
		ID            string
		OrderID       string
		Amount        float64
		PaymentMethod string
		Status        string
		InvoiceURL    string
		CreatedAt     time.Time
		UpdatedAt     time.Time
	}{
		ID:            id,
		OrderID:       orderIDResult,
		Amount:        amount,
		PaymentMethod: paymentMethod,
		Status:        status,
		InvoiceURL:    invoiceURL,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	log.Printf("[DB] Retrieved payment: %s for order: %s", id, orderID)
	return payment, nil
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

// AddMerchantItem adds a new item to a merchant's inventory
func AddMerchantItem(merchantID, itemID, name string, price float64, quantity int32) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
		INSERT INTO merchant_items (merchant_id, item_id, name, price, quantity)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (merchant_id, item_id) DO UPDATE SET
			name = EXCLUDED.name,
			price = EXCLUDED.price,
			quantity = EXCLUDED.quantity,
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := DB.Exec(query, merchantID, itemID, name, price, quantity)
	if err != nil {
		return fmt.Errorf("failed to add merchant item: %w", err)
	}
	log.Printf("[DB] Added/Updated merchant item: %s/%s", merchantID, itemID)
	return nil
}

// UpdateMerchantItem updates an existing merchant item
func UpdateMerchantItem(merchantID, itemID, name string, price float64, quantity int32) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
		UPDATE merchant_items 
		SET name = $3, price = $4, quantity = $5, updated_at = CURRENT_TIMESTAMP
		WHERE merchant_id = $1 AND item_id = $2
	`
	result, err := DB.Exec(query, merchantID, itemID, name, price, quantity)
	if err != nil {
		return fmt.Errorf("failed to update merchant item: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("merchant item not found: %s/%s", merchantID, itemID)
	}
	log.Printf("[DB] Updated merchant item: %s/%s", merchantID, itemID)
	return nil
}

// DeleteMerchantItem removes an item from a merchant's inventory
func DeleteMerchantItem(merchantID, itemID string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `DELETE FROM merchant_items WHERE merchant_id = $1 AND item_id = $2`
	result, err := DB.Exec(query, merchantID, itemID)
	if err != nil {
		return fmt.Errorf("failed to delete merchant item: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("merchant item not found: %s/%s", merchantID, itemID)
	}
	log.Printf("[DB] Deleted merchant item: %s/%s", merchantID, itemID)
	return nil
}

// DeductStockFromItems reduces stock for multiple items (used during order creation)
func DeductStockFromItems(merchantID string, items []*orderpb.OrderItems) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Start a transaction to ensure all stock deductions happen atomically
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Check stock availability and deduct
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
			return fmt.Errorf("insufficient stock for item %s: requested %d, available %d",
				item.ProductId, item.Quantity, currentStock)
		}

		// Deduct the stock
		_, err = tx.Exec(`
			UPDATE merchant_items 
			SET quantity = quantity - $3, updated_at = CURRENT_TIMESTAMP
			WHERE merchant_id = $1 AND item_id = $2
		`, merchantID, item.ProductId, item.Quantity)

		if err != nil {
			return fmt.Errorf("failed to deduct stock for item %s: %w", item.ProductId, err)
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit stock deduction transaction: %w", err)
	}

	log.Printf("[DB] Successfully deducted stock for %d items for merchant: %s", len(items), merchantID)
	return nil
}

// RestoreStockToItems restores stock for multiple items (used during order cancellation)
func RestoreStockToItems(merchantID string, items []*orderpb.OrderItems) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Start a transaction to ensure all stock restorations happen atomically
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Restore stock for each item
	for _, item := range items {
		_, err = tx.Exec(`
			UPDATE merchant_items 
			SET quantity = quantity + $3, updated_at = CURRENT_TIMESTAMP
			WHERE merchant_id = $1 AND item_id = $2
		`, merchantID, item.ProductId, item.Quantity)

		if err != nil {
			return fmt.Errorf("failed to restore stock for item %s: %w", item.ProductId, err)
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit stock restoration transaction: %w", err)
	}

	log.Printf("[DB] Successfully restored stock for %d items for merchant: %s", len(items), merchantID)
	return nil
}

// GetOrderItems retrieves all items for a specific order
func GetOrderItems(orderID string) ([]*orderpb.OrderItems, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := DB.Query(`
		SELECT item_id, quantity 
		FROM order_items 
		WHERE order_id = $1
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query order items: %w", err)
	}
	defer rows.Close()

	var items []*orderpb.OrderItems
	for rows.Next() {
		var itemID string
		var quantity int32
		if err := rows.Scan(&itemID, &quantity); err != nil {
			return nil, fmt.Errorf("failed to scan order item: %w", err)
		}
		items = append(items, &orderpb.OrderItems{
			ProductId: itemID,
			Quantity:  quantity,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating order items: %w", err)
	}

	log.Printf("[DB] Retrieved %d items for order: %s", len(items), orderID)
	return items, nil
}

// AP2 Database Functions

// InsertMandate inserts a new AP2 mandate into the database
func InsertMandate(mandate interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get mandate fields
	m, ok := mandate.(*struct {
		ID          string
		CustomerID  string
		Scope       string
		AmountLimit float64
		ExpiresAt   time.Time
		Status      string
		CreatedAt   time.Time
	})
	if !ok {
		return fmt.Errorf("invalid mandate type")
	}

	_, err := DB.Exec(`
		INSERT INTO ap2_mandates (id, customer_id, scope, amount_limit, expires_at, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, m.ID, m.CustomerID, m.Scope, m.AmountLimit, m.ExpiresAt, m.Status, m.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert mandate: %w", err)
	}

	log.Printf("[DB] Inserted mandate: %s for customer: %s", m.ID, m.CustomerID)
	return nil
}

// GetMandate retrieves a mandate by ID
func GetMandate(mandateID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, customerID, scope, status string
	var amountLimit float64
	var expiresAt, createdAt time.Time

	err := DB.QueryRow(`
		SELECT id, customer_id, scope, amount_limit, expires_at, status, created_at
		FROM ap2_mandates
		WHERE id = $1
	`, mandateID).Scan(&id, &customerID, &scope, &amountLimit, &expiresAt, &status, &createdAt)

	if err != nil {
		return nil, err
	}

	mandate := struct {
		ID          string
		CustomerID  string
		Scope       string
		AmountLimit float64
		ExpiresAt   time.Time
		Status      string
		CreatedAt   time.Time
	}{
		ID:          id,
		CustomerID:  customerID,
		Scope:       scope,
		AmountLimit: amountLimit,
		ExpiresAt:   expiresAt,
		Status:      status,
		CreatedAt:   createdAt,
	}

	return &mandate, nil
}

// InsertIntent inserts a new AP2 intent into the database
func InsertIntent(intent interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get intent fields
	i, ok := intent.(*struct {
		ID          string
		MandateID   string
		CustomerID  string
		CartID      string
		TotalAmount float64
		Status      string
		CreatedAt   time.Time
	})
	if !ok {
		return fmt.Errorf("invalid intent type")
	}

	_, err := DB.Exec(`
		INSERT INTO ap2_intents (id, mandate_id, customer_id, cart_id, total_amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, i.ID, i.MandateID, i.CustomerID, i.CartID, i.TotalAmount, i.Status, i.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert intent: %w", err)
	}

	log.Printf("[DB] Inserted intent: %s for customer: %s", i.ID, i.CustomerID)
	return nil
}

// GetIntent retrieves an intent by ID
func GetIntent(intentID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, mandateID, customerID, cartID, status string
	var totalAmount float64
	var createdAt time.Time

	err := DB.QueryRow(`
		SELECT id, mandate_id, customer_id, cart_id, total_amount, status, created_at
		FROM ap2_intents
		WHERE id = $1
	`, intentID).Scan(&id, &mandateID, &customerID, &cartID, &totalAmount, &status, &createdAt)

	if err != nil {
		return nil, err
	}

	intent := struct {
		ID          string
		MandateID   string
		CustomerID  string
		CartID      string
		TotalAmount float64
		Status      string
		CreatedAt   time.Time
	}{
		ID:          id,
		MandateID:   mandateID,
		CustomerID:  customerID,
		CartID:      cartID,
		TotalAmount: totalAmount,
		Status:      status,
		CreatedAt:   createdAt,
	}

	return &intent, nil
}

// UpdateIntentStatus updates the status of an intent
func UpdateIntentStatus(intentID, status string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := DB.Exec(`
		UPDATE ap2_intents
		SET status = $1
		WHERE id = $2
	`, status, intentID)

	if err != nil {
		return fmt.Errorf("failed to update intent status: %w", err)
	}

	log.Printf("[DB] Updated intent %s status to: %s", intentID, status)
	return nil
}

// InsertAuthorization inserts a new AP2 authorization into the database
func InsertAuthorization(authorization interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get authorization fields
	a, ok := authorization.(*struct {
		ID         string
		IntentID   string
		MandateID  string
		Authorized bool
		Message    string
		CreatedAt  time.Time
	})
	if !ok {
		return fmt.Errorf("invalid authorization type")
	}

	_, err := DB.Exec(`
		INSERT INTO ap2_authorizations (id, intent_id, mandate_id, authorized, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, a.ID, a.IntentID, a.MandateID, a.Authorized, a.Message, a.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert authorization: %w", err)
	}

	log.Printf("[DB] Inserted authorization: %s for intent: %s", a.ID, a.IntentID)
	return nil
}

// GetAuthorization retrieves an authorization by ID
func GetAuthorization(authorizationID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, intentID, mandateID, message string
	var authorized bool
	var createdAt time.Time

	err := DB.QueryRow(`
		SELECT id, intent_id, mandate_id, authorized, message, created_at
		FROM ap2_authorizations
		WHERE id = $1
	`, authorizationID).Scan(&id, &intentID, &mandateID, &authorized, &message, &createdAt)

	if err != nil {
		return nil, err
	}

	authorization := struct {
		ID         string
		IntentID   string
		MandateID  string
		Authorized bool
		Message    string
		CreatedAt  time.Time
	}{
		ID:         id,
		IntentID:   intentID,
		MandateID:  mandateID,
		Authorized: authorized,
		Message:    message,
		CreatedAt:  createdAt,
	}

	return &authorization, nil
}

// InsertExecution inserts a new AP2 execution into the database
func InsertExecution(execution interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get execution fields
	e, ok := execution.(*struct {
		ID              string
		IntentID        string
		AuthorizationID string
		OrderID         string
		PaymentID       string
		Status          string
		InvoiceURL      string
		CreatedAt       time.Time
	})
	if !ok {
		return fmt.Errorf("invalid execution type")
	}

	_, err := DB.Exec(`
		INSERT INTO ap2_executions (id, intent_id, authorization_id, order_id, payment_id, status, invoice_url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.ID, e.IntentID, e.AuthorizationID, e.OrderID, e.PaymentID, e.Status, e.InvoiceURL, e.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert execution: %w", err)
	}

	log.Printf("[DB] Inserted execution: %s for intent: %s", e.ID, e.IntentID)
	return nil
}

// GetExecution retrieves an execution by ID
func GetExecution(executionID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, intentID, authorizationID, orderID, paymentID, status, invoiceURL string
	var createdAt time.Time

	err := DB.QueryRow(`
		SELECT id, intent_id, authorization_id, order_id, payment_id, status, invoice_url, created_at
		FROM ap2_executions
		WHERE id = $1
	`, executionID).Scan(&id, &intentID, &authorizationID, &orderID, &paymentID, &status, &invoiceURL, &createdAt)

	if err != nil {
		return nil, err
	}

	execution := struct {
		ID              string
		IntentID        string
		AuthorizationID string
		OrderID         string
		PaymentID       string
		Status          string
		InvoiceURL      string
		CreatedAt       time.Time
	}{
		ID:              id,
		IntentID:        intentID,
		AuthorizationID: authorizationID,
		OrderID:         orderID,
		PaymentID:       paymentID,
		Status:          status,
		InvoiceURL:      invoiceURL,
		CreatedAt:       createdAt,
	}

	return &execution, nil
}

// UpdateExecutionStatus updates the status of an execution
func UpdateExecutionStatus(executionID, status string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := DB.Exec(`
		UPDATE ap2_executions
		SET status = $1
		WHERE id = $2
	`, status, executionID)

	if err != nil {
		return fmt.Errorf("failed to update execution status: %w", err)
	}

	log.Printf("[DB] Updated execution %s status to: %s", executionID, status)
	return nil
}

// InsertRefund inserts a new AP2 refund into the database
func InsertRefund(refund interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get refund fields
	r, ok := refund.(*struct {
		ID          string
		ExecutionID string
		Amount      float64
		Reason      string
		Status      string
		RefundID    string
		CreatedAt   time.Time
	})
	if !ok {
		return fmt.Errorf("invalid refund type")
	}

	_, err := DB.Exec(`
		INSERT INTO ap2_refunds (id, execution_id, amount, reason, status, refund_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.ID, r.ExecutionID, r.Amount, r.Reason, r.Status, r.RefundID, r.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert refund: %w", err)
	}

	log.Printf("[DB] Inserted refund: %s for execution: %s", r.ID, r.ExecutionID)
	return nil
}

// GetRefund retrieves a refund by ID
func GetRefund(refundID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var id, executionID, reason, status, refundIDResult string
	var amount float64
	var createdAt time.Time

	err := DB.QueryRow(`
		SELECT id, execution_id, amount, reason, status, refund_id, created_at
		FROM ap2_refunds
		WHERE id = $1
	`, refundID).Scan(&id, &executionID, &amount, &reason, &status, &refundIDResult, &createdAt)

	if err != nil {
		return nil, err
	}

	refund := struct {
		ID          string
		ExecutionID string
		Amount      float64
		Reason      string
		Status      string
		RefundID    string
		CreatedAt   time.Time
	}{
		ID:          id,
		ExecutionID: executionID,
		Amount:      amount,
		Reason:      reason,
		Status:      status,
		RefundID:    refundIDResult,
		CreatedAt:   createdAt,
	}

	return &refund, nil
}

// InsertShippingPreferences inserts or updates shipping preferences for a customer
func InsertShippingPreferences(customerID string, preferences interface{}) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Type assertion to get preferences fields
	p, ok := preferences.(*struct {
		AddressLine1   string
		AddressLine2   string
		City           string
		State          string
		PostalCode     string
		Country        string
		DeliveryMethod string
	})
	if !ok {
		return fmt.Errorf("invalid preferences type")
	}

	_, err := DB.Exec(`
		INSERT INTO shipping_preferences (customer_id, address_line1, address_line2, city, state, postal_code, country, delivery_method, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (customer_id) DO UPDATE SET
			address_line1 = EXCLUDED.address_line1,
			address_line2 = EXCLUDED.address_line2,
			city = EXCLUDED.city,
			state = EXCLUDED.state,
			postal_code = EXCLUDED.postal_code,
			country = EXCLUDED.country,
			delivery_method = EXCLUDED.delivery_method,
			updated_at = EXCLUDED.updated_at
	`, customerID, p.AddressLine1, p.AddressLine2, p.City, p.State, p.PostalCode, p.Country, p.DeliveryMethod, time.Now(), time.Now())

	if err != nil {
		return fmt.Errorf("failed to insert/update shipping preferences: %w", err)
	}

	log.Printf("[DB] Inserted/updated shipping preferences for customer: %s", customerID)
	return nil
}

// GetShippingPreferences retrieves shipping preferences for a customer
func GetShippingPreferences(customerID string) (interface{}, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var addressLine1, addressLine2, city, state, postalCode, country, deliveryMethod string
	var createdAt, updatedAt time.Time

	err := DB.QueryRow(`
		SELECT address_line1, address_line2, city, state, postal_code, country, delivery_method, created_at, updated_at
		FROM shipping_preferences
		WHERE customer_id = $1
	`, customerID).Scan(&addressLine1, &addressLine2, &city, &state, &postalCode, &country, &deliveryMethod, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	preferences := struct {
		CustomerID     string
		AddressLine1   string
		AddressLine2   string
		City           string
		State          string
		PostalCode     string
		Country        string
		DeliveryMethod string
		CreatedAt      time.Time
		UpdatedAt      time.Time
	}{
		CustomerID:     customerID,
		AddressLine1:   addressLine1,
		AddressLine2:   addressLine2,
		City:           city,
		State:          state,
		PostalCode:     postalCode,
		Country:        country,
		DeliveryMethod: deliveryMethod,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}

	return &preferences, nil
}
