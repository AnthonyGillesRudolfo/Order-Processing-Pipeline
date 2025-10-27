package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
	internalap2 "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/ap2"
	internalcart "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/cart"
	appconfig "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/config"
	internalmerchant "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/merchant"
	internalorder "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
	internalpayment "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	internalshipping "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/shipping"
	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"

	events "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/events"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
	kafka "github.com/segmentio/kafka-go"

	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
)

// AP2 response types (copied from internal/ap2/handlers.go)
type AP2ExecuteResult struct {
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`      // "pending" | "completed" | "failed" | "refunded"
	InvoiceLink string `json:"invoiceLink"` // rename from invoice_url
	PaymentID   string `json:"paymentId"`
	OrderID     string `json:"orderId"`
}

type AP2Envelope[T any] struct {
	Result T `json:"result"`
}

func newLogger(cfg appconfig.Config) *log.Logger {
	prefix := ""
	if cfg.ServiceName != "" {
		prefix = fmt.Sprintf("[%s] ", cfg.ServiceName)
	}
	logger := log.New(os.Stdout, prefix, log.LstdFlags|log.Lmicroseconds)
	log.SetOutput(os.Stdout)
	log.SetFlags(logger.Flags())
	log.SetPrefix(prefix)
	return logger
}

func setupTelemetry(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger) {
	var cleanup func()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cleanup = telemetry.InitTracer(cfg.ServiceName)
			return nil
		},
		OnStop: func(context.Context) error {
			if cleanup != nil {
				cleanup()
			}
			return nil
		},
	})
}

// newSQLDB provides a shared *sql.DB via Fx and also sets the postgres.DB global
// for backward compatibility with existing call sites. It closes the DB on stop.
func newSQLDB(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger) (*sql.DB, error) {
    logger.Printf("Connecting to PostgreSQL database %s@%s:%d", cfg.Database.Database, cfg.Database.Host, cfg.Database.Port)
    db, err := postgres.OpenDatabase(cfg.Database)
    if err != nil {
        logger.Printf("WARNING: failed to connect to database: %v", err)
        // Keep app running without DB; callers check postgres.DB nil
        return nil, nil
    }
    logger.Printf("Database connection established successfully")
    lc.Append(fx.Hook{
        OnStop: func(context.Context) error {
            return postgres.CloseDatabase()
        },
    })
    return db, nil
}

func registerWebServer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner, prod *events.Producer, db *sql.DB) {
	httpServer := newWebServer(cfg.HTTP.Addr, prod, db)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				logger.Printf("Web UI available on %s (serving ./web)", cfg.HTTP.Addr)
				if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Printf("Web UI/API server error: %v", err)
					_ = shutdowner.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	})
}

func registerOrderConsumer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Kafka.Brokers,
		Topic:    cfg.Kafka.OrdersTopic,
		GroupID:  cfg.Kafka.OrdersGroup,
		MinBytes: 1e3, MaxBytes: 10e6,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := runOrdersConsumer(ctx, reader, cfg.Kafka.OrdersTopic, cfg.Kafka.OrdersGroup, logger); err != nil {
					logger.Printf("orders consumer stopped with error: %v", err)
					_ = shutdowner.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			_ = reader.Close()
			<-done
			return nil
		},
	})
}

func registerPaymentsConsumer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner, db *sql.DB) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Kafka.Brokers,
		Topic:    cfg.Kafka.PaymentsTopic,
		GroupID:  cfg.Kafka.PaymentsGroup,
		MinBytes: 1e3, MaxBytes: 10e6,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := runPaymentsConsumer(ctx, reader, cfg.Kafka.PaymentsTopic, cfg.Kafka.PaymentsGroup, cfg.Restate.RuntimeURL, logger, db); err != nil {
					logger.Printf("payments consumer stopped with error: %v", err)
					_ = shutdowner.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			_ = reader.Close()
			<-done
			return nil
		},
	})
}

func registerRestateServer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner, srv *server.Restate) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			logger.Println("Restate server listening on", cfg.Restate.ListenAddr)
			logger.Println("")
			logger.Println("Service Architecture:")
			logger.Println("  - OrderService: WORKFLOW (keyed by order ID)")
			logger.Println("  - PaymentService: VIRTUAL OBJECT (keyed by payment ID)")
			logger.Println("  - ShippingService: VIRTUAL OBJECT (keyed by shipment ID)")
			logger.Println("  - MerchantService: VIRTUAL OBJECT (keyed by merchant ID)")
			logger.Println("  - CartService: VIRTUAL OBJECT (keyed by customer ID)")
			logger.Println("  - AP2 Endpoints: HTTP API (/ap2/mandates, /ap2/intents, /ap2/authorize, /ap2/execute, /ap2/refunds)")
			logger.Println("")
			logger.Println("Register with Restate:")
			displayRestateAddr := cfg.Restate.ListenAddr
			if strings.HasPrefix(displayRestateAddr, ":") {
				displayRestateAddr = "localhost" + displayRestateAddr
			}
			logger.Printf("  restate deployments register http://%s", displayRestateAddr)
			logger.Println("")
			logger.Println("Open the test UI:")
			displayHTTPAddr := cfg.HTTP.Addr
			if strings.HasPrefix(displayHTTPAddr, ":") {
				displayHTTPAddr = "localhost" + displayHTTPAddr
			}
			logger.Printf("  http://%s", displayHTTPAddr)
			logger.Println("")

			go func() {
				defer close(done)
				if err := srv.Start(ctx, cfg.Restate.ListenAddr); err != nil && !errors.Is(err, context.Canceled) {
					logger.Printf("Server error: %v", err)
					_ = shutdowner.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			<-done
			return nil
		},
	})
}

func buildRestateServer() *server.Restate {
	srv := server.NewRestate()

	// Bind OrderService as a Workflow
	orderWorkflow := restate.NewWorkflow("order.sv1.OrderService", restate.WithProtoJSON).
		Handler("CreateOrder", restate.NewWorkflowHandler(internalorder.CreateOrder)).
		Handler("Checkout", restate.NewWorkflowHandler(internalorder.Checkout)).
		Handler("GetOrder", restate.NewWorkflowSharedHandler(internalorder.GetOrder)).
		Handler("UpdateOrderStatus", restate.NewWorkflowHandler(internalorder.UpdateOrderStatus)).
		Handler("ContinueAfterPayment", restate.NewWorkflowHandler(internalorder.ContinueAfterPayment)).
		Handler("OnPaymentUpdate", restate.NewWorkflowHandler(internalorder.OnPaymentUpdate))
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

	// Bind CartService as a Virtual Object
	cartVirtualObject := restate.NewObject("cart.sv1.CartService").
		Handler("AddToCart", restate.NewObjectHandler(internalcart.AddToCart)).
		Handler("ViewCart", restate.NewObjectSharedHandler(internalcart.ViewCart)).
		Handler("UpdateCartItem", restate.NewObjectHandler(internalcart.UpdateCartItem)).
		Handler("RemoveFromCart", restate.NewObjectHandler(internalcart.RemoveFromCart)).
		Handler("ClearCart", restate.NewObjectHandler(internalcart.ClearCart)).
		Handler("GetCart", restate.NewObjectSharedHandler(internalcart.GetCart))
	srv = srv.Bind(cartVirtualObject)

	return srv
}

func runOrdersConsumer(ctx context.Context, reader *kafka.Reader, topic, group string, logger *log.Logger) error {
	logger.Printf("[%s] consumer started (group=%s)", topic, group)
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("[%s] read error: %w", topic, err)
		}

		var evt events.Envelope
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			logger.Printf("[%s] bad JSON: %v; payload=%s", topic, err, string(msg.Value))
			continue
		}

		switch evt.EventType {
		case "OrderCreated":
			handleOrderCreated(evt)
		default:
			logger.Printf("[%s] ignored eventType=%s key=%s", topic, evt.EventType, string(msg.Key))
		}
	}
}

func runPaymentsConsumer(ctx context.Context, reader *kafka.Reader, topic, group, runtimeURL string, logger *log.Logger, db *sql.DB) error {
	logger.Printf("[%s] consumer started (group=%s)", topic, group)
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("[%s] read error: %w", topic, err)
		}

		var evt events.Envelope
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			logger.Printf("[%s] bad JSON: %v; payload=%s", topic, err, string(msg.Value))
			if commitErr := reader.CommitMessages(ctx, msg); commitErr != nil {
				logger.Printf("[%s] commit error after bad JSON: %v", topic, commitErr)
			}
			continue
		}

		success := true

		switch evt.EventType {
		case "PaymentCompleted":
			if err := handlePaymentCompletedEventWithRuntime(evt, runtimeURL, db); err != nil {
				logger.Printf("[%s] PaymentCompleted error: %v", topic, err)
				success = false
			}
		case "PaymentExpired":
			if err := handlePaymentExpiredEventWithRuntime(evt, runtimeURL, db); err != nil {
				logger.Printf("[%s] PaymentExpired error: %v", topic, err)
				success = false
			}
		case "OrderCreated":
			handleOrderCreated(evt)
		default:
			logger.Printf("[%s] ignored eventType=%s key=%s", topic, evt.EventType, string(msg.Key))
		}

		if success {
			if err := reader.CommitMessages(ctx, msg); err != nil {
				logger.Printf("[%s] commit error: %v", topic, err)
			}
		} else {
			time.Sleep(time.Second)
		}
	}
}

// Your first handler: make it idempotent when you add side effects
func handleOrderCreated(evt events.Envelope) {
	// Example: just log for now (prove consumption works)
	data := toMap(evt.Data)
	log.Printf("[OrderCreated] orderId=%s total=%.2f invoice=%s",
		evt.AggregateID,
		toFloat(data["totalAmount"]),
		toString(data["invoiceUrl"]),
	)
}

// small helpers for robust logging
func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	default:
		return 0
	}
}

func handlePaymentCompletedEventWithRuntime(evt events.Envelope, runtimeURL string, db *sql.DB) error {
    data := toMap(evt.Data)
    paymentID := toString(data["paymentId"])
	if paymentID == "" {
		log.Printf("[payments.v1] PaymentCompleted event missing paymentId: %+v", data)
		return nil
	}

	orderID := toString(data["orderId"])
	if orderID == "" {
		info, err := getOrderFromDBByPaymentID(db, paymentID)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Printf("[payments.v1] PaymentCompleted order not found for payment %s; skipping", paymentID)
				return nil
			}
			return fmt.Errorf("lookup order for payment %s: %w", paymentID, err)
		}
		if ord, ok := info["order"].(map[string]any); ok {
			orderID = toString(ord["id"])
		}
	}

	if orderID == "" {
		log.Printf("[payments.v1] PaymentCompleted event missing orderId for payment %s; skipping", paymentID)
		return nil
	}

	payURL := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentCompleted", runtimeURL, paymentID)

	payReq := map[string]any{
		"payment_id": paymentID,
		"order_id":   orderID,
	}

	if err := postJSON(payURL, payReq); err != nil {
		return fmt.Errorf("mark payment completed: %w", err)
	}

    if err := resolvePaymentAwakeable(db, orderID, runtimeURL); err != nil {
        return fmt.Errorf("resolve awakeable: %w", err)
    }

    // Best-effort: clear the customer's cart after payment completion as a fallback
    // Try to get customer_id from DB and call ClearCart; ignore failures.
    if db != nil {
        if info, err := getOrderFromDB(db, orderID); err == nil {
            if ord, ok := info["order"].(map[string]any); ok {
                if cid, ok := ord["customer_id"].(string); ok && cid != "" {
                    clearURL := fmt.Sprintf("%s/cart.sv1.CartService/%s/ClearCart", runtimeURL, cid)
                    clearReq := map[string]any{"customer_id": cid}
                    if b, err := json.Marshal(clearReq); err == nil {
                        if resp2, err := http.Post(clearURL, "application/json", bytes.NewReader(b)); err == nil {
                            defer resp2.Body.Close()
                            if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
                                log.Printf("[payments.v1] warning: ClearCart returned status %d for %s", resp2.StatusCode, cid)
                            }
                        } else {
                            log.Printf("[payments.v1] warning: failed to ClearCart for %s: %v", cid, err)
                        }
                    }
                }
            }
        }
    }

    log.Printf("[payments.v1] processed PaymentCompleted for order %s (payment %s)", orderID, paymentID)
    return nil
}

func handlePaymentExpiredEventWithRuntime(evt events.Envelope, runtimeURL string, db *sql.DB) error {
	data := toMap(evt.Data)
	paymentID := toString(data["paymentId"])
	if paymentID == "" {
		log.Printf("[payments.v1] PaymentExpired event missing paymentId: %+v", data)
		return nil
	}

	orderID := toString(data["orderId"])
	if orderID == "" {
		info, err := getOrderFromDBByPaymentID(db, paymentID)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Printf("[payments.v1] PaymentExpired order not found for payment %s; skipping", paymentID)
				return nil
			}
			return fmt.Errorf("lookup order for payment %s: %w", paymentID, err)
		}
		if ord, ok := info["order"].(map[string]any); ok {
			orderID = toString(ord["id"])
		}
	}

	if orderID == "" {
		log.Printf("[payments.v1] PaymentExpired event missing orderId for payment %s; skipping", paymentID)
		return nil
	}

	expiredURL := fmt.Sprintf("%s/order.sv1.PaymentService/%s/MarkPaymentExpired", runtimeURL, paymentID)

	expiredReq := map[string]any{
		"payment_id": paymentID,
		"order_id":   orderID,
	}

	if err := postJSON(expiredURL, expiredReq); err != nil {
		return fmt.Errorf("mark payment expired: %w", err)
	}

	log.Printf("[payments.v1] processed PaymentExpired for order %s (payment %s)", orderID, paymentID)
	return nil
}

func resolvePaymentAwakeable(db *sql.DB, orderID, runtimeURL string) error {
	if db == nil {
		log.Printf("[payments.v1] database unavailable, skipping awakeable resolve for order %s", orderID)
		return nil
	}

	row := db.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderID)
	var awakeableID sql.NullString
	if err := row.Scan(&awakeableID); err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[payments.v1] no awakeable found for order %s", orderID)
			return updateOrderStatusProcessing(orderID)
		}
		return fmt.Errorf("query awakeable id for order %s: %w", orderID, err)
	}

	if !awakeableID.Valid || awakeableID.String == "" {
		log.Printf("[payments.v1] awakeable id empty for order %s", orderID)
		return updateOrderStatusProcessing(orderID)
	}

	awakeableURL := fmt.Sprintf("%s/restate/awakeables/%s/resolve", runtimeURL, awakeableID.String)
	awakeableBody := []byte(`"payment_completed"`)

	resp, err := http.Post(awakeableURL, "application/json", bytes.NewReader(awakeableBody))
	if err != nil {
		return fmt.Errorf("resolve awakeable for order %s: %w", orderID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("resolve awakeable for order %s: status %d", orderID, resp.StatusCode)
	}

	log.Printf("[payments.v1] resolved awakeable %s for order %s", awakeableID.String, orderID)
	return updateOrderStatusProcessing(orderID)
}

func updateOrderStatusProcessing(orderID string) error {
	if err := postgres.UpdateOrderStatusDB(orderID, orderpb.OrderStatus_PROCESSING); err != nil {
		return fmt.Errorf("update order status for order %s: %w", orderID, err)
	}
	log.Printf("[payments.v1] updated order %s status to PROCESSING", orderID)
	return nil
}

func emitPaymentEvent(ctx context.Context, prod *events.Producer, status, orderID, paymentID, customerID, invoiceURL string, totalAmount float64, invoiceID string) (string, error) {
	normalizedStatus := strings.ToUpper(status)

	var eventType string
	switch normalizedStatus {
	case "PAID":
		eventType = "PaymentCompleted"
	case "EXPIRED":
		eventType = "PaymentExpired"
	default:
		return "", nil
	}

	key := orderID
	if key == "" {
		key = paymentID
	}

    // Producer is injected via DI and managed by lifecycle.

	data := map[string]any{
		"orderId":     orderID,
		"paymentId":   paymentID,
		"customerId":  customerID,
		"invoiceURL":  invoiceURL,
		"totalAmount": totalAmount,
		"provider":    "xendit",
		"status":      normalizedStatus,
	}

	if invoiceID != "" {
		data["invoiceId"] = invoiceID
	}

	evt := events.Envelope{
		EventType:    eventType,
		EventVersion: "v1",
		AggregateID:  key,
		Data:         data,
	}

	if err := prod.Publish(ctx, "payments.v1", key, evt); err != nil {
		return "", err
	}

	return eventType, nil
}

func main() {
    _ = godotenv.Load()

    app := fx.New(
        fx.Provide(
            appconfig.Load,
            newLogger,
            buildRestateServer,
            newKafkaProducer,
            newSQLDB,
            func(db *sql.DB) *postgres.Repository { return postgres.NewRepository(db) },
        ),
        fx.Invoke(
            func(logger *log.Logger, cfg appconfig.Config) {
                logger.Printf("Starting %s...", cfg.ServiceName)
            },
            func(r *postgres.Repository) { internalorder.SetRepository(r) },
            setupTelemetry,
            registerWebServer,
            registerOrderConsumer,
            registerPaymentsConsumer,
            registerRestateServer,
        ),
    )

	app.Run()
}

// newKafkaProducer constructs a shared Kafka producer and binds its lifecycle to Fx.
func newKafkaProducer(cfg appconfig.Config, lc fx.Lifecycle) *events.Producer {
    prod := events.NewProducerWithBrokers(cfg.Kafka.Brokers)
    lc.Append(fx.Hook{
        OnStop: func(context.Context) error {
            return prod.Close()
        },
    })
    return prod
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

func newWebServer(addr string, prod *events.Producer, db *sql.DB) *http.Server {
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
	mux.Handle("/api/checkout", otelhttp.NewHandler(http.HandlerFunc(handleCheckout), "checkout"))
	mux.Handle("/api/orders", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleOrdersList(db, w, r) }), "orders-list"))
	mux.Handle("/api/orders/", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleOrders(db, w, r) }), "orders"))
	mux.Handle("/api/merchants/", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleMerchantAPI(db, w, r) }), "merchant-api"))
	mux.Handle("/api/cart/", otelhttp.NewHandler(http.HandlerFunc(handleCartAPI), "cart-api"))
	mux.Handle("/api/debug/fix-orders", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleFixOrders(db, w, r) }), "fix-orders"))
	mux.Handle("/api/webhooks/xendit", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleXenditWebhook(prod, db, w, r)
	}), "xendit-webhook"))

	// AP2 Endpoints
	mux.Handle("/ap2/mandates", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleCreateMandate), "ap2-create-mandate"))
	mux.Handle("/ap2/mandates/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetMandate), "ap2-get-mandate"))
	mux.Handle("/ap2/intents", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleCreateIntent), "ap2-create-intent"))
	mux.Handle("/ap2/authorize", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleAuthorize), "ap2-authorize"))
	mux.Handle("/ap2/execute", otelhttp.NewHandler(http.HandlerFunc(handleAP2Execute), "ap2-execute"))
	mux.Handle("/ap2/status/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetStatus), "ap2-get-status"))
	mux.Handle("/webhooks/payment", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlePaymentWebhook(prod, db, w, r)
	}), "payment-webhook"))
	mux.Handle("/ap2/refunds", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleRefund), "ap2-refund"))
	mux.Handle("/ap2/refunds/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetRefund), "ap2-get-refund"))

	return &http.Server{
		Addr:    addr,
		Handler: withCORS(mux),
	}
}

// GET /api/orders → list recent orders with payment + items
func handleOrdersList(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if db == nil {
		http.Error(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := db.Query(`
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

// handleAP2Execute handles AP2 execute requests by calling the AP2 handlers directly
func handleAP2Execute(w http.ResponseWriter, r *http.Request) {
	// Use the AP2 handlers directly instead of trying to call Restate
	internalap2.HandleExecute(w, r)
}

// handlePaymentWebhook handles payment status updates from Xendit webhooks
func handlePaymentWebhook(prod *events.Producer, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var webhookPayload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&webhookPayload); err != nil {
		log.Printf("[Webhook] Failed to decode webhook payload: %v", err)
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	externalID, _ := webhookPayload["external_id"].(string)
	status, _ := webhookPayload["status"].(string)
	invoiceID, _ := webhookPayload["id"].(string)

	log.Printf("[Webhook] Received payment webhook: external_id=%s, status=%s", externalID, status)

	if externalID == "" {
		log.Printf("[Webhook] Missing external_id in webhook payload")
		http.Error(w, "missing external_id", http.StatusBadRequest)
		return
	}

	var (
		orderID     string
		customerID  string
		invoiceURL  string
		totalAmount float64
	)

	if info, err := getOrderFromDBByPaymentID(db, externalID); err == nil {
		if ord, ok := info["order"].(map[string]any); ok {
			if id, ok := ord["id"].(string); ok {
				orderID = id
			}
			if cid, ok := ord["customer_id"].(string); ok {
				customerID = cid
			}
			if v, ok := ord["total_amount"].(float64); ok {
				totalAmount = v
			}
		}
		if pay, ok := info["payment"].(map[string]any); ok {
			if v, ok := pay["invoice_url"].(string); ok {
				invoiceURL = v
			}
		}
	} else {
		if err != sql.ErrNoRows {
			log.Printf("[Webhook] Failed to load order info for payment %s: %v", externalID, err)
		} else {
			log.Printf("[Webhook] No order found for payment %s; proceeding with minimal event", externalID)
		}
	}

    eventType, err := emitPaymentEvent(r.Context(), prod, status, orderID, externalID, customerID, invoiceURL, totalAmount, invoiceID)
	if err != nil {
		log.Printf("[Webhook] Failed to publish Kafka event for payment %s: %v", externalID, err)
		http.Error(w, "failed to enqueue payment event", http.StatusInternalServerError)
		return
	}

	if eventType == "" {
		log.Printf("[Webhook] Ignoring payment status %s for payment %s", status, externalID)
	} else {
		log.Printf("[Webhook] Enqueued %s event for order %s (payment %s)", eventType, orderID, externalID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "received"})
}

// processCheckout processes the checkout request and returns the AP2 response
func processCheckout(db *sql.DB, w http.ResponseWriter, checkoutReq checkoutRequest) {
	// Create a new request to the checkout endpoint
	checkoutBody, _ := json.Marshal(checkoutReq)
	checkoutReqHTTP, _ := http.NewRequest("POST", "http://localhost:3000/api/checkout", bytes.NewReader(checkoutBody))
	checkoutReqHTTP.Header.Set("Content-Type", "application/json")

	// Create a response recorder to capture the checkout response
	checkoutResp := httptest.NewRecorder()

	// Call the checkout handler directly
	handleCheckout(checkoutResp, checkoutReqHTTP)

	if checkoutResp.Code != 200 {
		log.Printf("[AP2] Checkout failed with status: %d", checkoutResp.Code)
		http.Error(w, "checkout failed", http.StatusInternalServerError)
		return
	}

	// Parse the checkout response to get the order ID and invoice URL
	var checkoutResult struct {
		OrderID    string `json:"order_id"`
		InvoiceURL string `json:"invoice_url"`
	}

	if err := json.Unmarshal(checkoutResp.Body.Bytes(), &checkoutResult); err != nil {
		log.Printf("[AP2] Failed to parse checkout response: %v", err)
		http.Error(w, "failed to parse checkout response", http.StatusInternalServerError)
		return
	}

	executionID := "exec-" + uuid.New().String()[:8]
	paymentID := "payment-" + uuid.New().String()[:8]

	// Fetch actual payment data from database
	var invoiceURL string
	var actualPaymentID string
	if db != nil {
		var id, invoice string
		err := db.QueryRow(`SELECT id, COALESCE(invoice_url,'') FROM payments WHERE order_id = $1 ORDER BY updated_at DESC LIMIT 1`, checkoutResult.OrderID).Scan(&id, &invoice)
		if err != nil {
			log.Printf("[AP2] warning: failed to load payment for order %s: %v", checkoutResult.OrderID, err)
			invoiceURL = checkoutResult.InvoiceURL
			actualPaymentID = paymentID
		} else {
			invoiceURL = invoice
			actualPaymentID = id
			log.Printf("[AP2] Found payment data: payment_id=%s, invoice_url=%s", id, invoice)
		}
	} else {
		invoiceURL = checkoutResult.InvoiceURL
		actualPaymentID = paymentID
	}

	log.Printf("[AP2] Execute response: order_id=%s, payment_id=%s, invoice_url=%s", checkoutResult.OrderID, actualPaymentID, invoiceURL)

	// Return 200 with JSON envelope and camelCase keys
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AP2Envelope[AP2ExecuteResult]{
		Result: AP2ExecuteResult{
			ExecutionID: executionID,
			Status:      strings.ToLower("pending"), // ensure lowercase
			InvoiceLink: invoiceURL,                 // actual invoice URL from payment record
			PaymentID:   actualPaymentID,
			OrderID:     checkoutResult.OrderID,
		},
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
	url := fmt.Sprintf("%s/order.sv1.OrderService/%s/Checkout", runtimeURL, orderID)

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

    // Best-effort: clear the user's cart right after successful checkout
    // This is idempotent and UX-friendly; failures are only logged.
    if req.CustomerID != "" {
        runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
        clearURL := fmt.Sprintf("%s/cart.sv1.CartService/%s/ClearCart", runtimeURL, req.CustomerID)
        clearReq := map[string]any{"customer_id": req.CustomerID}
        if b, err := json.Marshal(clearReq); err == nil {
            if resp2, err := http.Post(clearURL, "application/json", bytes.NewReader(b)); err == nil {
                defer resp2.Body.Close()
                if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
                    log.Printf("[checkout] warning: ClearCart returned status %d for %s", resp2.StatusCode, req.CustomerID)
                }
            } else {
                log.Printf("[checkout] warning: failed to ClearCart for %s: %v", req.CustomerID, err)
            }
        }
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"order_id": orderID, "invoice_url": wfResp["invoice_url"]})
}

func handleOrders(db *sql.DB, w http.ResponseWriter, r *http.Request) {
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
		info, err := getOrderFromDB(db, orderID)
		if err != nil {
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}
		orderMap, _ := info["order"].(map[string]any)
		paymentID, _ := orderMap["payment_id"].(string)
		if paymentID == "" {
			// Fallback: find latest payment by order_id
		if db != nil {
			row := db.QueryRow(`SELECT id FROM payments WHERE order_id = $1 ORDER BY updated_at DESC LIMIT 1`, orderID)
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
		if db != nil {
			row := db.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderID)
			if err := row.Scan(&awakeableID); err != nil {
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
			} else {
				defer awakeableResp.Body.Close()
				if awakeableResp.StatusCode >= 200 && awakeableResp.StatusCode < 300 {
				} else {
				}
			}
		} else {
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		return
	}

	// Handle GET request for individual order
	if r.Method == http.MethodGet {
		orderID := path
		resp, err := getOrderFromDB(db, orderID)
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

func getOrderFromDB(db *sql.DB, orderID string) (map[string]any, error) {
	if db == nil {
		return nil, sql.ErrConnDone
	}

	row := db.QueryRow(`
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

// Cart API handlers
func handleCartAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/cart/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		http.Error(w, "Invalid cart API path", http.StatusBadRequest)
		return
	}

	customerID := parts[0]
	action := parts[1]

	switch action {
	case "add":
		handleCartAdd(w, r, customerID)
	case "view":
		handleCartView(w, r, customerID)
	case "update":
		handleCartUpdate(w, r, customerID)
	case "remove":
		handleCartRemove(w, r, customerID)
	default:
		http.Error(w, "Unknown cart action", http.StatusBadRequest)
	}
}

func handleCartAdd(w http.ResponseWriter, r *http.Request, customerID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CustomerID string `json:"customer_id"`
		MerchantID string `json:"merchant_id"`
		Items      []struct {
			ProductID string `json:"product_id"`
			Quantity  int32  `json:"quantity"`
		} `json:"items"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Call Restate cart service
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
	url := fmt.Sprintf("%s/cart.sv1.CartService/%s/AddToCart", runtimeURL, customerID)

	reqBytes, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	json.NewEncoder(w).Encode(result)
}

func handleCartView(w http.ResponseWriter, r *http.Request, customerID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CustomerID string `json:"customer_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Call Restate cart service
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
	url := fmt.Sprintf("%s/cart.sv1.CartService/%s/ViewCart", runtimeURL, customerID)

	reqBytes, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	json.NewEncoder(w).Encode(result)
}

func handleCartUpdate(w http.ResponseWriter, r *http.Request, customerID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CustomerID string `json:"customer_id"`
		ProductID  string `json:"product_id"`
		Quantity   int32  `json:"quantity"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Call Restate cart service
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
	url := fmt.Sprintf("%s/cart.sv1.CartService/%s/UpdateCartItem", runtimeURL, customerID)

	reqBytes, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	json.NewEncoder(w).Encode(result)
}

func handleCartRemove(w http.ResponseWriter, r *http.Request, customerID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CustomerID string   `json:"customer_id"`
		ProductIDs []string `json:"product_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Call Restate cart service
	runtimeURL := getenv("RESTATE_RUNTIME_URL", "http://127.0.0.1:8080")
	url := fmt.Sprintf("%s/cart.sv1.CartService/%s/RemoveFromCart", runtimeURL, customerID)

	reqBytes, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reach Restate runtime"})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	json.NewEncoder(w).Encode(result)
}

func handleMerchantAPI(db *sql.DB, w http.ResponseWriter, r *http.Request) {
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
		handleListMerchantItems(db, w, r, runtimeURL, merchantID)
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

func handleListMerchantItems(db *sql.DB, w http.ResponseWriter, r *http.Request, runtimeURL, merchantID string) {
    // Prefer authoritative stock from the database to avoid stale Restate cache
    if db != nil {
        items, err := postgres.GetMerchantItems(merchantID)
        if err == nil {
            w.Header().Set("Content-Type", "application/json")
            // Normalize payload shape for UI consumption
            out := make([]map[string]any, 0, len(items))
            for _, it := range items {
                out = append(out, map[string]any{
                    "itemId":   it.ItemId,
                    "name":     it.Name,
                    "quantity": it.Quantity,
                    "price":    it.Price,
                })
            }
            _ = json.NewEncoder(w).Encode(map[string]any{"items": out})
            return
        }
        // If DB query fails, fall back to Restate runtime for resiliency
    }

    // Fallback: call Restate runtime (may be stale if object state not in sync)
    url := fmt.Sprintf("%s/merchant.sv1.MerchantService/%s/ListItems", runtimeURL, merchantID)
    reqBody := map[string]any{
        "merchant_id": merchantID,
        "page_size":   100,
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

func handleFixOrders(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if db == nil {
		http.Error(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	// Update order_items that have empty item_id or name
	// This fixes orders that were created before merchant_items data was available
	_, err := db.Exec(`
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
func handleXenditWebhook(prod *events.Producer, db *sql.DB, w http.ResponseWriter, r *http.Request) {
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

	var totalAmount float64
	var invoiceURL string
	var orderID string
	var customerID string

	if info, err := getOrderFromDBByPaymentID(db, externalID); err == nil {
		if ord, ok := info["order"].(map[string]any); ok {
			if id, ok := ord["id"].(string); ok {
				orderID = id
			}
			if v, ok := ord["total_amount"].(float64); ok {
				totalAmount = v
			}
			if cid, ok := ord["customer_id"].(string); ok {
				customerID = cid
			}
		}
		if pay, ok := info["payment"].(map[string]any); ok {
			if v, ok := pay["invoice_url"].(string); ok {
				invoiceURL = v
			}
		}
	}

	log.Printf("[Xendit Webhook] Processing callback: external_id=%s, status=%s, invoice_id=%s", externalID, status, invoiceID)

	// Handle different payment statuses
	switch status {
	case "PAID":
		log.Printf("[Xendit Webhook] Payment completed for external_id: %s", externalID)
		eventType, err := emitPaymentEvent(r.Context(), prod, status, orderID, externalID, customerID, invoiceURL, totalAmount, invoiceID)
		if err != nil {
			log.Printf("[Xendit Webhook] Failed to enqueue payment event: %v", err)
			http.Error(w, "failed to enqueue event", http.StatusInternalServerError)
			return
		}
		if eventType == "" {
			log.Printf("[Xendit Webhook] Ignoring payment status %s for external_id %s", status, externalID)
		} else {
			log.Printf("[Xendit Webhook] Enqueued %s event for order %s", eventType, orderID)
		}

	case "EXPIRED":
		log.Printf("[Xendit Webhook] Payment expired for external_id: %s", externalID)
		eventType, err := emitPaymentEvent(r.Context(), prod, status, orderID, externalID, customerID, invoiceURL, totalAmount, invoiceID)
		if err != nil {
			log.Printf("[Xendit Webhook] Failed to enqueue payment event: %v", err)
			http.Error(w, "failed to enqueue event", http.StatusInternalServerError)
			return
		}
		if eventType == "" {
			log.Printf("[Xendit Webhook] Ignoring payment status %s for external_id %s", status, externalID)
		} else {
			log.Printf("[Xendit Webhook] Enqueued %s event for order %s", eventType, orderID)
		}

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
func getOrderFromDBByPaymentID(db *sql.DB, paymentID string) (map[string]any, error) {
	if db == nil {
		return nil, sql.ErrConnDone
	}

	row := db.QueryRow(`
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
