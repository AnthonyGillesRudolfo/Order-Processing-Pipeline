package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"runtime"

	"github.com/google/uuid"
	internalorder "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
	internalpayment "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	internalshipping "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/shipping"
	internalmerchant "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/merchant"
	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
)

func main() {
	log.Println("Starting Order Processing Pipeline (modular server)...")

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
		Handler("UpdateOrderStatus", restate.NewWorkflowHandler(internalorder.UpdateOrderStatus))
	srv = srv.Bind(orderWorkflow)

	// Bind PaymentService as a Virtual Object
	paymentVirtualObject := restate.NewObject("order.sv1.PaymentService", restate.WithProtoJSON).
		Handler("ProcessPayment", restate.NewObjectHandler(internalpayment.ProcessPayment))
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
		Handler("UpdateStock", restate.NewObjectHandler(internalmerchant.UpdateStock))
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
	mux.HandleFunc("/api/orders/", handleGetOrderStatus)
	mux.HandleFunc("/api/merchants/", handleGetMerchantItems)

	log.Println("Web UI available on :3000 (serving ./web)")
	return http.ListenAndServe(":3000", withCORS(mux))
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"order_id": orderID})
}

func handleGetOrderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// /api/orders/{id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orders/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "order id required", http.StatusBadRequest)
		return
	}
	orderID := parts[0]

	resp, err := getOrderFromDB(orderID)
	if err != nil {
		http.Error(w, "order not found or DB unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func getOrderFromDB(orderID string) (map[string]any, error) {
	if postgres.DB == nil {
		return nil, sql.ErrConnDone
	}

	row := postgres.DB.QueryRow(`
		SELECT id, customer_id, status, total_amount, payment_id, shipment_id, tracking_number, updated_at
		FROM orders WHERE id = $1
	`, orderID)

	var (
		id, customerID, status, paymentID, shipmentID, trackingNumber string
		totalAmount                                                    sql.NullFloat64
		updatedAt                                                      string
	)
	if err := row.Scan(&id, &customerID, &status, &totalAmount, &paymentID, &shipmentID, &trackingNumber, &updatedAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"order": map[string]any{
			"id":              id,
			"customer_id":     customerID,
			"status":          status,
			"total_amount":    totalAmount.Float64,
			"payment_id":      paymentID,
			"shipment_id":     shipmentID,
			"tracking_number": trackingNumber,
			"updated_at":      updatedAt,
		},
	}, nil
}

func handleGetMerchantItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// /api/merchants/{merchant_id}/items
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/merchants/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "items" {
		http.Error(w, "merchant id required, expected format: /api/merchants/{merchant_id}/items", http.StatusBadRequest)
		return
	}
	merchantID := parts[0]

	// Call Restate runtime HTTP endpoint directly
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
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