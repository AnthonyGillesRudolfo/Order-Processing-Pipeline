package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	internalmerchant "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/merchant"
	internalorder "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
	internalpayment "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	internalshipping "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/shipping"
	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
)

func main() {
	log.Println("Starting Order Processing Pipeline (modular server)...")

	// Load .env if present
	_ = godotenv.Load()

	// Load DB config from environment variables with sensible defaults
	// ORDER_DB_HOST, ORDER_DB_PORT, ORDER_DB_NAME, ORDER_DB_USER, ORDER_DB_PASSWORD
	dbHost := getenv("ORDER_DB_HOST", "localhost")
	dbPortStr := getenv("ORDER_DB_PORT", "5432")
	dbPort, err := strconv.Atoi(dbPortStr)
	if err != nil {
		log.Printf("Invalid ORDER_DB_PORT '%s', defaulting to 5432", dbPortStr)
		dbPort = 5432
	}
	dbName := getenv("ORDER_DB_NAME", "orderpipeline")
	dbUser := getenv("ORDER_DB_USER", "orderpipelineadmin")
	dbPassword := getenv("ORDER_DB_PASSWORD", "")

	dbConfig := postgres.DatabaseConfig{
		Host:     dbHost,
		Port:     dbPort,
		Database: dbName,
		User:     dbUser,
		Password: dbPassword,
	}

	log.Println("Connecting to PostgreSQL database...")
	if err := postgres.InitDatabase(dbConfig); err != nil {
		log.Printf("WARNING: Failed to connect to database: %v", err)
		log.Println("Continuing without database persistence...")
	} else {
		log.Println("Database connection established successfully")
		defer func() {
			if err := postgres.CloseDatabase(); err != nil {
				log.Printf("Error closing database: %v", err)
			}
		}()
	}

	// Start simple web UI + API on :3000
	go func() {
		if err := startWebUIAndAPI(); err != nil {
			log.Printf("Web UI/API server error: %v", err)
		}
	}()

	// Create Restate server
	srv := server.NewRestate()

	// Bind OrderService as a Workflow
	orderWorkflow := restate.NewWorkflow("order.sv1.OrderService", restate.WithProtoJSON).
		Handler("CreateOrder", restate.NewWorkflowHandler(internalorder.CreateOrder)).
		Handler("GetOrder", restate.NewWorkflowSharedHandler(internalorder.GetOrder)).
		Handler("UpdateOrderStatus", restate.NewWorkflowHandler(internalorder.UpdateOrderStatus)).
		Handler("ContinueAfterPayment", restate.NewWorkflowHandler(internalorder.ContinueAfterPayment))
	srv = srv.Bind(orderWorkflow)

	// Bind OrderManagementService as a Virtual Object for order operations
	orderManagementObject := restate.NewObject("order.sv1.OrderManagementService", restate.WithProtoJSON).
		Handler("CancelOrder", restate.NewObjectHandler(internalorder.CancelOrder)).
		Handler("ShipOrder", restate.NewObjectHandler(internalorder.ShipOrder)).
		Handler("DeliverOrder", restate.NewObjectHandler(internalorder.DeliverOrder)).
		Handler("ConfirmOrder", restate.NewObjectHandler(internalorder.ConfirmOrder)).
		Handler("ReturnOrder", restate.NewObjectHandler(internalorder.ReturnOrder))
	srv = srv.Bind(orderManagementObject)

	// Bind PaymentService as a Virtual Object
	paymentVirtualObject := restate.NewObject("order.sv1.PaymentService", restate.WithProtoJSON).
		Handler("ProcessPayment", restate.NewObjectHandler(internalpayment.ProcessPayment)).
		Handler("MarkPaymentCompleted", restate.NewObjectHandler(internalpayment.MarkPaymentCompleted)).
		Handler("ProcessRefund", restate.NewObjectHandler(internalpayment.ProcessRefund)).
		Handler("MarkPaymentExpired", restate.NewObjectHandler(internalpayment.MarkPaymentExpired))
	srv = srv.Bind(paymentVirtualObject)

	// Bind ShippingService as a Virtual Object
	shippingVirtualObject := restate.NewObject("order.sv1.ShippingService", restate.WithProtoJSON).
		Handler("CreateShipment", restate.NewObjectHandler(internalshipping.CreateShipment)).
		Handler("TrackShipment", restate.NewObjectSharedHandler(internalshipping.TrackShipment))
	srv = srv.Bind(shippingVirtualObject)

	// Bind MerchantService as a Virtual Object
	merchantVirtualObject := restate.NewObject("merchant.sv1.MerchantService", restate.WithProtoJSON).
		Handler("GetMerchant", restate.NewObjectSharedHandler(internalmerchant.GetMerchant)).
		Handler("ListItems", restate.NewObjectSharedHandler(internalmerchant.ListItems)).
		Handler("GetItem", restate.NewObjectSharedHandler(internalmerchant.GetItem)).
		Handler("UpdateStock", restate.NewObjectHandler(internalmerchant.UpdateStock)).
		Handler("AddItem", restate.NewObjectHandler(internalmerchant.AddItem)).
		Handler("UpdateItem", restate.NewObjectHandler(internalmerchant.UpdateItem)).
		Handler("DeleteItem", restate.NewObjectHandler(internalmerchant.DeleteItem))
	srv = srv.Bind(merchantVirtualObject)

	// Start the server on port 9081
	log.Println("Restate server listening on :9081")
	log.Println("")
	log.Println("Service Architecture:")
	log.Println("  - OrderService: WORKFLOW (keyed by order ID)")
	log.Println("  - PaymentService: VIRTUAL OBJECT (keyed by payment ID)")
	log.Println("  - ShippingService: VIRTUAL OBJECT (keyed by shipment ID)")
	log.Println("  - MerchantService: VIRTUAL OBJECT (keyed by merchant ID)")
	log.Println("")
	log.Println("Register with Restate:")
	log.Println("  restate deployments register http://localhost:9081")
	log.Println("")
	log.Println("Open the test UI:")
	log.Println("  http://localhost:3000")
	log.Println("")

	if err := srv.Start(context.Background(), ":9081"); err != nil {
		log.Printf("Server error: %v", err)
		os.Exit(1)
	}
}

// getenv returns the value of the environment variable key if set, otherwise defaultVal
func getenv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

/*** Simple Web UI + API ***/

type checkoutItem struct {
	ProductID string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

type checkoutRequest struct {
	CustomerID string         `json:"customer_id"`
	Items      []checkoutItem `json:"items"`
	MerchantID string         `json:"merchant_id"`
}

func startWebUIAndAPI() error {
	mux := http.NewServeMux()

	// Static web files under / (index.html) and /static/*
	_, src, _, _ := runtime.Caller(0)
	base := filepath.Dir(src) // cmd/server
	webDir := filepath.Join(base, "..", "..", "web")
	webDir, _ = filepath.Abs(webDir)

	fileServer := http.FileServer(http.Dir(webDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.Handle("/", fileServer)

	// API
	mux.HandleFunc("/api/checkout", handleCheckout)
	mux.HandleFunc("/api/orders", handleOrdersList)
	mux.HandleFunc("/api/orders/", handleOrders)
	mux.HandleFunc("/api/merchants/", handleMerchantAPI)
	mux.HandleFunc("/api/debug/fix-orders", handleFixOrders)
	mux.HandleFunc("/api/webhooks/xendit", handleXenditWebhook)

	log.Println("Web UI available on :3000 (serving ./web)")
	return http.ListenAndServe(":3000", withCORS(mux))
}

// GET /api/orders → list recent orders with payment + items
func handleOrdersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if postgres.DB == nil {
		http.Error(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := postgres.DB.Query(`
        SELECT o.id, o.customer_id, o.status, o.total_amount, o.payment_id, o.updated_at,
               COALESCE(p.status,''), COALESCE(p.invoice_url,''),
               COALESCE(oi.items_json,'[]')
        FROM orders o
        LEFT JOIN payments p ON p.id = o.payment_id
        LEFT JOIN (
          SELECT oi.order_id,
                 json_agg(
                   json_build_object(
                     'product_id', oi.item_id,
                     'name', COALESCE(NULLIF(oi.name, ''), mi.name, oi.item_id),
                     'quantity', oi.quantity,
                     'unit_price', oi.unit_price
                   )
                 ) AS items_json
          FROM order_items oi
          LEFT JOIN merchant_items mi ON mi.merchant_id = oi.merchant_id AND mi.item_id = oi.item_id
          GROUP BY oi.order_id
        ) oi ON oi.order_id = o.id
        ORDER BY o.updated_at DESC
        LIMIT 50`)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var list []map[string]any
	for rows.Next() {
		var id, customerID, status, paymentID, updatedAt string
		var totalAmount sql.NullFloat64
		var payStatus, invoiceURL, itemsJSON string
		if err := rows.Scan(&id, &customerID, &status, &totalAmount, &paymentID, &updatedAt, &payStatus, &invoiceURL, &itemsJSON); err != nil {
			continue
		}
		var items any
		_ = json.Unmarshal([]byte(itemsJSON), &items)
		list = append(list, map[string]any{
			"id":             id,
			"customer_id":    customerID,
			"status":         status,
			"total_amount":   totalAmount.Float64,
			"payment_id":     paymentID,
			"payment_status": payStatus,
			"invoice_url":    invoiceURL,
			"updated_at":     updatedAt,
			"items":          items,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"orders": list})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simple permissive CORS for local testing
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.CustomerID == "" || len(req.Items) == 0 || req.MerchantID == "" {
		http.Error(w, "customer_id, items, and merchant_id are required", http.StatusBadRequest)
		return
	}

	orderID := "web-" + strings.ReplaceAll(uuid.New().String()[:8], "-", "")

	in := map[string]any{
		"customer_id": req.CustomerID,
		"items":       req.Items,
		"merchant_id": req.MerchantID,
	}
	inBytes, _ := json.Marshal(in)
	log.Printf("[DEBUG] Checkout request data: %s", string(inBytes))

	// Call Restate runtime HTTP endpoint directly
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
	url := fmt.Sprintf("%s/order.sv1.OrderService/%s/CreateOrder", runtimeURL, orderID)

	resp, err := http.Post(url, "application/json", bytes.NewReader(inBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "workflow start failed", "detail": detail})
		return
	}

	// Decode response from Restate to bubble up invoice_url
	var wfResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&wfResp)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"order_id": orderID, "invoice_url": wfResp["invoice_url"]})
}

func handleOrders(w http.ResponseWriter, r *http.Request) {
	// Routes:
	// GET  /api/orders/{id}
	// POST /api/orders/{id}/simulate_payment_success
	path := strings.TrimPrefix(r.URL.Path, "/api/orders/")
	if path == "" {
		http.Error(w, "order id required", http.StatusBadRequest)
		return
	}

	// Handle ship order endpoint
	if strings.HasSuffix(path, "/ship") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/ship")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		var reqBody struct {
			TrackingNumber string `json:"tracking_number"`
			Carrier        string `json:"carrier"`
			ServiceType    string `json:"service_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		// Set defaults if not provided
		if reqBody.TrackingNumber == "" {
			reqBody.TrackingNumber = fmt.Sprintf("TRK%08d", time.Now().Unix()%100000000)
		}
		if reqBody.Carrier == "" {
			reqBody.Carrier = "FedEx"
		}
		if reqBody.ServiceType == "" {
			reqBody.ServiceType = "Ground"
		}

		// Call Restate ShipOrder endpoint
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ShipOrder", runtimeURL, orderID)

		shipReq := map[string]any{
			"order_id":        orderID,
			"tracking_number": reqBody.TrackingNumber,
			"carrier":         reqBody.Carrier,
			"service_type":    reqBody.ServiceType,
		}

		if err := postJSON(url, shipReq); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to ship order", "detail": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order shipped successfully"})
		return
	}

	// Handle deliver order endpoint
	if strings.HasSuffix(path, "/deliver") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/deliver")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		// Call Restate DeliverOrder endpoint
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/DeliverOrder", runtimeURL, orderID)

		deliverReq := map[string]any{
			"order_id": orderID,
		}

		if err := postJSON(url, deliverReq); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to deliver order", "detail": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order delivered successfully"})
		return
	}

	// Handle cancel order endpoint
	if strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/cancel")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		var reqBody struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		// Call Restate CancelOrder endpoint
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/CancelOrder", runtimeURL, orderID)

		cancelReq := map[string]any{
			"order_id": orderID,
			"reason":   reqBody.Reason,
		}

		if err := postJSON(url, cancelReq); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to cancel order", "detail": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order cancelled successfully"})
		return
	}

	// Handle confirm order endpoint
	if strings.HasSuffix(path, "/confirm") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/confirm")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		// Call Restate ConfirmOrder endpoint
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ConfirmOrder", runtimeURL, orderID)

		confirmReq := map[string]any{
			"order_id": orderID,
		}

		if err := postJSON(url, confirmReq); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to confirm order", "detail": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Order confirmed successfully"})
		return
	}

	// Handle return order endpoint
	if strings.HasSuffix(path, "/return") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/return")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		var reqBody struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		// Call Restate ReturnOrder endpoint
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		url := fmt.Sprintf("%s/order.sv1.OrderManagementService/%s/ReturnOrder", runtimeURL, orderID)

		returnReq := map[string]any{
			"order_id": orderID,
			"reason":   reqBody.Reason,
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(func() []byte {
			b, _ := json.Marshal(returnReq)
			return b
		}()))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to return order", "detail": err.Error()})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to return order", "detail": fmt.Sprintf("status %d", resp.StatusCode)})
			return
		}

		var returnResp map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&returnResp)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(returnResp)
		return
	}

	// Handle simulate_payment_success endpoint
	if strings.HasSuffix(path, "/simulate_payment_success") && r.Method == http.MethodPost {
		orderID := strings.TrimSuffix(path, "/simulate_payment_success")
		if orderID == "" {
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}
		// Lookup payment_id from DB
		info, err := getOrderFromDB(orderID)
		if err != nil {
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}
		orderMap, _ := info["order"].(map[string]any)
		paymentID, _ := orderMap["payment_id"].(string)
		if paymentID == "" {
			// Fallback: find latest payment by order_id
			if postgres.DB != nil {
				row := postgres.DB.QueryRow(`SELECT id FROM payments WHERE order_id = $1 ORDER BY updated_at DESC LIMIT 1`, orderID)
				var pid string
				if err := row.Scan(&pid); err == nil && pid != "" {
					paymentID = pid
				}
			}
			if paymentID == "" {
				http.Error(w, "payment_id not set", http.StatusBadRequest)
				return
			}
		}

		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		// Mark payment completed
		payURL := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentCompleted", runtimeURL, paymentID)
		if err := postJSON(payURL, map[string]any{"payment_id": paymentID, "order_id": orderID}); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to mark payment completed", "detail": err.Error(), "payment_id": paymentID})
			return
		}

		// Get the awakeable ID from the database
		var awakeableID string
		if postgres.DB != nil {
			row := postgres.DB.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderID)
			if err := row.Scan(&awakeableID); err != nil {
				log.Printf("[DEBUG] Failed to get awakeable ID for order %s: %v", orderID, err)
			}
		}

		if awakeableID != "" {
			// Resolve the awakeable using Restate's built-in awakeable resolution API
			awakeableURL := fmt.Sprintf("%s/restate/awakeables/%s/resolve", runtimeURL, awakeableID)
			// Send a simple string value, not a JSON object
			awakeableBody := "payment_completed"
			awakeableBytes := []byte(fmt.Sprintf(`"%s"`, awakeableBody))

			awakeableResp, err := http.Post(awakeableURL, "application/json", bytes.NewReader(awakeableBytes))
			if err != nil {
				log.Printf("[DEBUG] Failed to resolve awakeable: %v", err)
			} else {
				defer awakeableResp.Body.Close()
				if awakeableResp.StatusCode >= 200 && awakeableResp.StatusCode < 300 {
					log.Printf("[DEBUG] Successfully resolved awakeable %s for order %s", awakeableID, orderID)
				} else {
					log.Printf("[DEBUG] Failed to resolve awakeable, status: %d", awakeableResp.StatusCode)
				}
			}
		} else {
			log.Printf("[DEBUG] No awakeable ID found for order %s", orderID)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		return
	}

	// Handle GET request for individual order
	if r.Method == http.MethodGet {
		orderID := path
		resp, err := getOrderFromDB(orderID)
		if err != nil {
			http.Error(w, "order not found or DB unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	http.NotFound(w, r)
}

func postJSON(url string, body map[string]any) error {
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func getOrderFromDB(orderID string) (map[string]any, error) {
	if postgres.DB == nil {
		return nil, sql.ErrConnDone
	}

	row := postgres.DB.QueryRow(`
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
		log.Printf("[DEBUG] getOrderFromDB failed for order %s: %v", orderID, err)
		return nil, err
	}
	log.Printf("[DEBUG] getOrderFromDB found order %s: customer=%s, status=%s, payment_id=%s", id, customerID, status, paymentID)
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

func handleMerchantAPI(w http.ResponseWriter, r *http.Request) {
	// Parse URL path: /api/merchants/{merchant_id}/items[/{item_id}]
	path := strings.TrimPrefix(r.URL.Path, "/api/merchants/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 || parts[0] == "" || parts[1] != "items" {
		http.Error(w, "invalid path, expected: /api/merchants/{merchant_id}/items[/{item_id}]", http.StatusBadRequest)
		return
	}

	merchantID := parts[0]
	itemID := ""
	if len(parts) > 2 {
		itemID = parts[2]
	}

	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")

	switch r.Method {
	case http.MethodGet:
		// GET /api/merchants/{merchant_id}/items - List all items
		handleListMerchantItems(w, r, runtimeURL, merchantID)
	case http.MethodPost:
		// POST /api/merchants/{merchant_id}/items/{item_id} - Add new item
		handleAddMerchantItem(w, r, runtimeURL, merchantID, itemID)
	case http.MethodPut:
		// PUT /api/merchants/{merchant_id}/items/{item_id} - Update existing item
		handleUpdateMerchantItem(w, r, runtimeURL, merchantID, itemID)
	case http.MethodDelete:
		// DELETE /api/merchants/{merchant_id}/items/{item_id} - Delete item
		handleDeleteMerchantItem(w, r, runtimeURL, merchantID, itemID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleListMerchantItems(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID string) {
	// Call Restate runtime HTTP endpoint directly
	url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/ListItems", runtimeURL, merchantID)

	// Create request body for ListItems
	reqBody := map[string]any{
		"merchant_id": merchantID,
		"page_size":   100, // Get all items for now
		"page_token":  "",
	}
	reqBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to fetch merchant items", "detail": detail})
		return
	}

	var response map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to decode response", "detail": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func handleAddMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
	var reqBody map[string]any
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// Call Restate AddItem endpoint
	url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/AddItem", runtimeURL, merchantID)
	reqBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to add merchant item", "detail": detail})
		return
	}

	var response map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&response)
	_ = json.NewEncoder(w).Encode(response)
}

func handleUpdateMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
	var reqBody map[string]any
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// Call Restate UpdateItem endpoint
	url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/UpdateItem", runtimeURL, merchantID)
	reqBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to update merchant item", "detail": detail})
		return
	}

	var response map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&response)
	_ = json.NewEncoder(w).Encode(response)
}

func handleDeleteMerchantItem(w http.ResponseWriter, r *http.Request, runtimeURL, merchantID, itemID string) {
	if itemID == "" {
		http.Error(w, "item ID required for deletion", http.StatusBadRequest)
		return
	}

	// Call Restate DeleteItem endpoint
	url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/DeleteItem", runtimeURL, merchantID)
	reqBody := map[string]any{
		"merchant_id": merchantID,
		"item_id":     itemID,
	}
	reqBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to reach Restate runtime", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to delete merchant item", "detail": detail})
		return
	}

	var response map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&response)
	_ = json.NewEncoder(w).Encode(response)
}

func handleFixOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if postgres.DB == nil {
		http.Error(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	// Update order_items that have empty item_id or name
	// This fixes orders that were created before merchant_items data was available
	_, err := postgres.DB.Exec(`
		UPDATE order_items 
		SET item_id = 'i_001', 
		    name = 'Apple',
		    merchant_id = 'm_001'
		WHERE item_id = '' OR name = ''
	`)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fix orders: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "Orders fixed successfully"})
}

// handleXenditWebhook handles Xendit payment callbacks
func handleXenditWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify the callback token
	expectedToken := os.Getenv("XENDIT_CALLBACK_TOKEN")
	if expectedToken == "" {
		log.Printf("[Xendit Webhook] WARNING: XENDIT_CALLBACK_TOKEN not set, skipping verification")
	} else {
		receivedToken := r.Header.Get("x-callback-token")
		if receivedToken != expectedToken {
			log.Printf("[Xendit Webhook] Invalid callback token: expected=%s, received=%s", expectedToken, receivedToken)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Parse the webhook payload
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		log.Printf("[Xendit Webhook] Failed to decode payload: %v", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[Xendit Webhook] Received payload: %+v", payload)

	// Extract key fields from Xendit webhook
	externalID, _ := payload["external_id"].(string)
	status, _ := payload["status"].(string)
	invoiceID, _ := payload["id"].(string)

	if externalID == "" {
		log.Printf("[Xendit Webhook] Missing external_id in payload")
		http.Error(w, "missing external_id", http.StatusBadRequest)
		return
	}

	log.Printf("[Xendit Webhook] Processing callback: external_id=%s, status=%s, invoice_id=%s", externalID, status, invoiceID)

	// Handle different payment statuses
	switch status {
	case "PAID":
		log.Printf("[Xendit Webhook] Payment completed for external_id: %s", externalID)

		// Get order information from database
		orderInfo, err := getOrderFromDBByPaymentID(externalID)
		if err != nil {
			log.Printf("[Xendit Webhook] Failed to find order for payment_id %s: %v", externalID, err)
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}

		orderMap, _ := orderInfo["order"].(map[string]any)
		orderID, _ := orderMap["id"].(string)

		if orderID == "" {
			log.Printf("[Xendit Webhook] Order ID not found for payment_id %s", externalID)
			http.Error(w, "order id not found", http.StatusInternalServerError)
			return
		}

		// Mark payment as completed via Restate
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		payURL := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentCompleted", runtimeURL, externalID)

		payReq := map[string]any{
			"payment_id": externalID,
			"order_id":   orderID,
		}

		if err := postJSON(payURL, payReq); err != nil {
			log.Printf("[Xendit Webhook] Failed to mark payment completed: %v", err)
			http.Error(w, "failed to mark payment completed", http.StatusInternalServerError)
			return
		}

		// Resolve the awakeable to continue the order workflow
		var awakeableID string
		if postgres.DB != nil {
			row := postgres.DB.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderID)
			if err := row.Scan(&awakeableID); err != nil {
				log.Printf("[Xendit Webhook] Failed to get awakeable ID for order %s: %v", orderID, err)
			}
		}

		if awakeableID != "" {
			// Resolve the awakeable using Restate's built-in awakeable resolution API
			awakeableURL := fmt.Sprintf("%s/restate/awakeables/%s/resolve", runtimeURL, awakeableID)
			awakeableBody := "payment_completed"
			awakeableBytes := []byte(fmt.Sprintf(`"%s"`, awakeableBody))

			awakeableResp, err := http.Post(awakeableURL, "application/json", bytes.NewReader(awakeableBytes))
			if err != nil {
				log.Printf("[Xendit Webhook] Failed to resolve awakeable: %v", err)
			} else {
				defer awakeableResp.Body.Close()
				if awakeableResp.StatusCode >= 200 && awakeableResp.StatusCode < 300 {
					log.Printf("[Xendit Webhook] Successfully resolved awakeable %s for order %s", awakeableID, orderID)
				} else {
					log.Printf("[Xendit Webhook] Failed to resolve awakeable, status: %d", awakeableResp.StatusCode)
				}
			}
		} else {
			log.Printf("[Xendit Webhook] No awakeable ID found for order %s", orderID)
		}

		log.Printf("[Xendit Webhook] Successfully processed payment completion for order %s", orderID)

	case "EXPIRED":
		log.Printf("[Xendit Webhook] Payment expired for external_id: %s", externalID)

		// Get order information from database
		orderInfo, err := getOrderFromDBByPaymentID(externalID)
		if err != nil {
			log.Printf("[Xendit Webhook] Failed to find order for payment_id %s: %v", externalID, err)
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}

		orderMap, _ := orderInfo["order"].(map[string]any)
		orderID, _ := orderMap["id"].(string)

		if orderID == "" {
			log.Printf("[Xendit Webhook] Order ID not found for payment_id %s", externalID)
			http.Error(w, "order id not found", http.StatusInternalServerError)
			return
		}

		// Mark payment as expired via Restate
		runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
		expiredURL := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentExpired", runtimeURL, externalID)

		expiredReq := map[string]any{
			"payment_id": externalID,
			"order_id":   orderID,
		}

		if err := postJSON(expiredURL, expiredReq); err != nil {
			log.Printf("[Xendit Webhook] Failed to mark payment expired: %v", err)
			http.Error(w, "failed to mark payment expired", http.StatusInternalServerError)
			return
		}

		log.Printf("[Xendit Webhook] Successfully processed payment expiration for order %s", orderID)

	case "FAILED":
		log.Printf("[Xendit Webhook] Payment failed for external_id: %s", externalID)
		// Could implement payment failure logic here if needed

	default:
		log.Printf("[Xendit Webhook] Unhandled status: %s for external_id: %s", status, externalID)
	}

	// Always respond with 200 OK to acknowledge receipt
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "received"})
}

// getOrderFromDBByPaymentID retrieves order information by payment ID
func getOrderFromDBByPaymentID(paymentID string) (map[string]any, error) {
	if postgres.DB == nil {
		return nil, sql.ErrConnDone
	}

	row := postgres.DB.QueryRow(`
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
		log.Printf("[Xendit Webhook] getOrderFromDBByPaymentID failed for payment_id %s: %v", paymentID, err)
		return nil, err
	}

	log.Printf("[Xendit Webhook] getOrderFromDBByPaymentID found order %s: customer=%s, status=%s, payment_id=%s", id, customerID, status, paymentIDResult)
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
