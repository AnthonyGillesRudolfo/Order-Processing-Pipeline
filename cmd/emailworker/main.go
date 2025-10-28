package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/segmentio/kafka-go"
	"go.uber.org/fx"

	appconfig "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/config"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/email"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/events"
)

func newWorkerLogger(cfg appconfig.Config) *log.Logger {
	prefix := fmt.Sprintf("[%s-email-worker] ", cfg.ServiceName)
	logger := log.New(os.Stdout, prefix, log.LstdFlags|log.Lmicroseconds)
	log.SetOutput(os.Stdout)
	log.SetFlags(logger.Flags())
	log.SetPrefix(prefix)
	return logger
}

func newEmailSender() email.Sender {
	if os.Getenv("SMTP_HOST") != "" || os.Getenv("SMTP_PORT") != "" {
		return email.NewSMTPSender()
	}
	return email.LogSender{}
}

func registerEmailConsumer(lc fx.Lifecycle, cfg appconfig.Config, logger *log.Logger, sender email.Sender, shutdowner fx.Shutdowner) {
	topics := []string{cfg.Kafka.OrdersTopic, cfg.Kafka.PaymentsTopic}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     cfg.Kafka.Brokers,
		GroupTopics: topics,
		GroupID:     cfg.Kafka.EmailGroup,
		MinBytes:    1e3, MaxBytes: 10e6,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := runEmailConsumer(ctx, reader, logger, sender, cfg.Email.DemoRecipient, topics); err != nil {
					logger.Printf("email worker stopped with error: %v", err)
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

func runEmailConsumer(ctx context.Context, reader *kafka.Reader, logger *log.Logger, sender email.Sender, recipient string, topics []string) error {
	logger.Printf("Email worker consuming topics=%s group=%s", strings.Join(topics, ","), reader.Config().GroupID)
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}

		var evt events.Envelope
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			logger.Printf("[email-worker] bad json: %v; payload=%s", err, string(msg.Value))
			continue
		}

		switch evt.EventType {
		case "OrderCreated":
			handleOrderCreated(logger, sender, recipient, evt)
		case "PaymentCompleted":
			handlePaymentCompleted(logger, sender, recipient, evt)
		case "PaymentExpired":
			handlePaymentExpired(logger, sender, recipient, evt)
		default:
			// ignore other event types
		}
	}
}

func handleOrderCreated(logger *log.Logger, sender email.Sender, recipient string, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])

	body := email.RenderOrderCreatedEmail(orderID, total, invoiceURL)
	if err := sender.Send(recipient, "Your order confirmation", body); err != nil {
		logger.Printf("[email-worker] send failed: %v", err)
		return
	}

	logger.Printf("[email-worker] sent OrderCreated email to=%s order=%s total=%.2f", recipient, orderID, total)
}

func handlePaymentCompleted(logger *log.Logger, sender email.Sender, recipient string, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])

	body := email.RenderPaymentCompletedEmail(orderID, total, invoiceURL)
	if err := sender.Send(recipient, "Your Payment Confirmation", body); err != nil {
		logger.Printf("[email-worker] send failed: %v", err)
		return
	}

	logger.Printf("[email-worker] sent PaymentCompleted email to=%s order=%s total=%.2f", recipient, orderID, total)
}

func handlePaymentExpired(logger *log.Logger, sender email.Sender, recipient string, evt events.Envelope) {
	data := toMap(evt.Data)
	orderID := toString(data["orderId"])
	invoiceURL := toString(data["invoiceUrl"])
	total := toFloat(data["totalAmount"])

	body := email.RenderPaymentExpiredEmail(orderID, total, invoiceURL)
	if err := sender.Send(recipient, "Payment Expired Confirmation", body); err != nil {
		logger.Printf("[email-worker] send failed: %v", err)
		return
	}

	logger.Printf("[email-worker] sent PaymentExpired email to=%s order=%s total=%.2f", recipient, orderID, total)
}

func toMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
	}
	return 0
}

func main() {
	_ = godotenv.Load()

	app := fx.New(
		fx.Provide(
			appconfig.Load,
			newWorkerLogger,
			newEmailSender,
		),
		fx.Invoke(
			func(logger *log.Logger) {
				logger.Println("Email worker starting...")
			},
			registerEmailConsumer,
		),
	)

	app.Run()
}
