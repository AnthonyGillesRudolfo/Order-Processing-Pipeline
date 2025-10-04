package main

import (
    "context"
    "log"
    "os"
    "strconv"
    internalorder "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
    internalpayment "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/payment"
    internalshipping "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/shipping"
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

	// Start the server on port 9081
	log.Println("Restate server listening on :9081")
	log.Println("")
	log.Println("Service Architecture:")
	log.Println("  - OrderService: WORKFLOW (keyed by order ID)")
	log.Println("  - PaymentService: VIRTUAL OBJECT (keyed by payment ID)")
	log.Println("  - ShippingService: VIRTUAL OBJECT (keyed by shipment ID)")
	log.Println("")
	log.Println("Register with Restate:")
	log.Println("  restate deployments register http://localhost:9081")
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
