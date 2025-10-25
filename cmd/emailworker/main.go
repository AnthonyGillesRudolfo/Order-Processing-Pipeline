package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"

	"github.com/segmentio/kafka-go"

	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/email"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/events"
)

func main() {
	log.Println("Email worker starting...")
	startConsumer()
}

func startConsumer() {
	brokers := getenv("KAFKA_BROKERS", "localhost:9092")
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{brokers},
		GroupTopics:    []string{"orders.v1", "payments.v1"},
		GroupID:  "email-workers", // its own consumer group
		MinBytes: 1e3, MaxBytes: 10e6,
	})
	defer reader.Close()

	sender := pickSender()
	log.Println("[email-worker] consuming (group=email-workers)")
	for {
		msg, err := reader.ReadMessage(context.Background() )
		if err != nil {
			log.Printf("[email-worker] read error: %v", err)
			return
		}

		var evt events.Envelope
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Printf("[email-worker] bad json: %v; payload=%s", err, string(msg.Value))
			continue
		}

		switch evt.EventType {
		case "OrderCreated":
			handleOrderCreated(sender, evt)
		case "PaymentCompleted":
			handlePaymentCompleted(sender, evt)
		case "PaymentExpired":
			handlePaymentExpired(sender, evt)
		default:
			// ignore other event types
		}
	}
}

func handleOrderCreated(sender email.Sender, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])
	// You’ll probably store email on the customer record; for demo accept override via env:
	to := getenv("DEMO_TO_EMAIL", "test@example.local")

	body := email.RenderOrderCreatedEmail(orderID, total, invoiceURL)
	if err := sender.Send(to, "Your order confirmation", body); err != nil {
		log.Printf("[email-worker] send failed: %v", err)
		return
	}

	log.Printf("[email-worker] sent OrderCreated email to=%s order=%s total=%.2f", to, orderID, total)
}

func handlePaymentCompleted(sender email.Sender, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])

	to := getenv("DEMO_TO_EMAIL", "test@example.local")

	body := email.RenderPaymentCompletedEmail(orderID, total, invoiceURL)
	if err := sender.Send(to, "Your Payment Confirmation", body); err != nil {
		log.Printf("[email-worker] send failed: %v", err)
		return
	}

	log.Printf("[email-worker] sent PaymentCompleted email to=%s order=%s total=%.2f", to, orderID, total)
}

func handlePaymentExpired(sender email.Sender, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])

	to := getenv("DEMO_TO_EMAIL", "test@example.local")

	body := email.RenderPaymentExpiredEmail(orderID, total, invoiceURL)
	if err := sender.Send(to, "Payment Expired Confirmation", body); err != nil {
		log.Printf("[email-worker] send failed: %v", err)
		return
	}

	log.Printf("[email-worker] sent PaymentExpired email to=%s order=%s total=%.2f", to, orderID, total)
}

func pickSender() email.Sender {
	// Use SMTP if configured; else fallback to log
	if os.Getenv("SMTP_HOST") != "" || os.Getenv("SMTP_PORT") != "" {
		return email.NewSMTPSender()
	}
	return email.LogSender{}
}

// helpers
func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func toMap(v interface{}) map[string]interface{} { if m, ok := v.(map[string]interface{}); ok { return m }; return map[string]interface{}{} }
func toString(v interface{}) string { if s, ok := v.(string); ok { return s }; return "" }
func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64: return t
	case float32: return float64(t)
	case int: return float64(t)
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil { return f }
	}
	return 0
}
