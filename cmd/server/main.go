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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
	internalcart "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/cart"
	appconfig "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/config"
	internalmerchant "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/merchant"
	internalorder "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
	internalpayment "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
	internalshipping "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/shipping"
	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	internalapi "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/api"

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

// AP2 response types centralized under internal/ap2/types.go

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

func registerWebServer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner, prod *events.Producer, db *sql.DB, repo *postgres.Repository) {
    httpServer := newWebServer(cfg.HTTP.Addr, prod, db, repo)
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

func registerPaymentsConsumer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, shutdowner fx.Shutdowner, repo *postgres.Repository) {
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
                if err := runPaymentsConsumer(ctx, reader, cfg.Kafka.PaymentsTopic, cfg.Kafka.PaymentsGroup, cfg.Restate.RuntimeURL, logger, repo); err != nil {
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

func runPaymentsConsumer(ctx context.Context, reader *kafka.Reader, topic, group, runtimeURL string, logger *log.Logger, repo *postgres.Repository) error {
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
            if err := handlePaymentCompletedEventWithRuntime(evt, runtimeURL, repo); err != nil {
                logger.Printf("[%s] PaymentCompleted error: %v", topic, err)
                success = false
            }
        case "PaymentExpired":
            if err := handlePaymentExpiredEventWithRuntime(evt, runtimeURL, repo); err != nil {
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

func handlePaymentCompletedEventWithRuntime(evt events.Envelope, runtimeURL string, repo *postgres.Repository) error {
    data := toMap(evt.Data)
    paymentID := toString(data["paymentId"])
	if paymentID == "" {
		log.Printf("[payments.v1] PaymentCompleted event missing paymentId: %+v", data)
		return nil
	}

	orderID := toString(data["orderId"])
    if orderID == "" {
        info, err := repo.GetOrderWithPaymentByPaymentID(paymentID)
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

    if err := resolvePaymentAwakeable(repo, orderID, runtimeURL); err != nil {
        return fmt.Errorf("resolve awakeable: %w", err)
    }

    // Best-effort: clear the customer's cart after payment completion as a fallback
    // Try to get customer_id from DB and call ClearCart; ignore failures.
    if repo != nil {
        if info, err := repo.GetOrderWithPayment(orderID); err == nil {
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

func handlePaymentExpiredEventWithRuntime(evt events.Envelope, runtimeURL string, repo *postgres.Repository) error {
	data := toMap(evt.Data)
	paymentID := toString(data["paymentId"])
	if paymentID == "" {
		log.Printf("[payments.v1] PaymentExpired event missing paymentId: %+v", data)
		return nil
	}

	orderID := toString(data["orderId"])
    if orderID == "" {
    info, err := repo.GetOrderWithPaymentByPaymentID(paymentID)
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

func resolvePaymentAwakeable(repo *postgres.Repository, orderID, runtimeURL string) error {
    if repo == nil || repo.DB == nil {
        log.Printf("[payments.v1] database unavailable, skipping awakeable resolve for order %s", orderID)
        return nil
    }

    row := repo.DB.QueryRow(`SELECT awakeable_id FROM orders WHERE id = $1`, orderID)
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

// emitPaymentEvent moved to internal/api/webhooks

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

func newWebServer(addr string, prod *events.Producer, db *sql.DB, repo *postgres.Repository) *http.Server {
    mux := http.NewServeMux()

    // Static web files under / (index.html) and /static/*)
    // Prefer WEB_DIR env (docker sets WEB_DIR=/app/web). Fallbacks for local dev.
    webDir := os.Getenv("WEB_DIR")
    if webDir == "" {
        // Common runtime image path where Dockerfile copies assets
        webDir = "/app/web"
    }
    if st, err := os.Stat(webDir); err != nil || !st.IsDir() {
        // Fallback to source-relative path for local `go run`.
        if _, src, _, ok := runtime.Caller(0); ok {
            base := filepath.Dir(src) // cmd/server
            guess := filepath.Join(base, "..", "..", "web")
            if abs, err := filepath.Abs(guess); err == nil {
                webDir = abs
            } else {
                webDir = guess
            }
        }
    }

    fileServer := http.FileServer(http.Dir(webDir))
    mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
    mux.Handle("/", fileServer)

    // API
    mux.Handle("/api/checkout", otelhttp.NewHandler(http.HandlerFunc(handleCheckout), "checkout"))
    // Orders routes registered via internal/api using repository
    internalapi.RegisterOrdersRoutes(mux, repo)
    // Merchants + Cart routes via internal/api
    internalapi.RegisterMerchantRoutes(mux, repo)
    internalapi.RegisterCartRoutes(mux)
    mux.Handle("/api/debug/fix-orders", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleFixOrders(repo, w, r) }), "fix-orders"))
    // Webhooks via internal/api
    internalapi.RegisterWebhookRoutes(mux, prod, repo)

    // AP2 endpoints via internal/api
    internalapi.RegisterAP2Routes(mux)

	return &http.Server{
		Addr:    addr,
		Handler: withCORS(mux),
	}
}

// GET /api/orders → list recent orders with payment + items
// orders list route moved to internal/api

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
// AP2 routes handled via internal/api

// handlePaymentWebhook handles payment status updates from Xendit webhooks
// processPaymentWebhook handles both the legacy payment webhook and Xendit webhook
// endpoints. When verifyToken is true, the Xendit callback token is validated.
// webhooks moved to internal/api

// legacy webhook moved to internal/api

// processCheckout processes the checkout request and returns the AP2 response
// removed unused processCheckout helper

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

    // Decode response from Restate. Support both invoice_url and invoiceLink
    var wfResp map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&wfResp)
    var invoiceAny any
    if v, ok := wfResp["invoice_url"]; ok { invoiceAny = v } else if v, ok := wfResp["invoiceLink"]; ok { invoiceAny = v }

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
    _ = json.NewEncoder(w).Encode(map[string]any{
        // snake_case for backward compatibility
        "order_id":   orderID,
        "invoice_url": invoiceAny,
        // camelCase for new clients
        "orderId":     orderID,
        "invoiceLink": invoiceAny,
    })
}

// orders endpoints moved to internal/api

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

// moved to postgres.GetOrderWithPayment

// Cart API handlers
// cart endpoints moved to internal/api

// removed unused cart helpers (moved to internal/api)

// merchant endpoints moved to internal/api

func handleFixOrders(repo *postgres.Repository, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
    if repo == nil || repo.DB == nil {
        http.Error(w, "db unavailable", http.StatusInternalServerError)
        return
    }

	// Update order_items that have empty item_id or name
	// This fixes orders that were created before merchant_items data was available
    _, err := repo.DB.Exec(`
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
// xendit webhook moved to internal/api

// getOrderFromDBByPaymentID retrieves order information by payment ID
// moved to postgres.GetOrderWithPaymentByPaymentID
