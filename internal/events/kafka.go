package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Producer struct{ 
	w *kafka.Writer 
	tracer trace.Tracer
}

func NewProducer() *Producer {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = "localhost:9092"
	}
	return &Producer{
		w: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  []string{brokers},
			Balancer: &kafka.Hash{}, // partition by Kafka message key
		}),
		tracer: otel.Tracer("order-processing-pipeline/kafka"),
	}
}

func (p *Producer) Close() error { return p.w.Close() }

// Envelope is the standard event schema your services publish.
// Keep it small and stable.
type Envelope struct {
	EventType    string      `json:"eventType"`
	EventVersion string      `json:"eventVersion"`
	OccurredAt   time.Time   `json:"occurredAt"`
	AggregateID  string      `json:"aggregateId"` // e.g., orderId
	Data         interface{} `json:"data"`
}

// Publish writes a single message to Kafka.
// 'key' is the Kafka partition key (use orderId to keep per-order ordering).
func (p *Producer) Publish(ctx context.Context, topic, key string, evt Envelope) error {
	evt.OccurredAt = time.Now().UTC()
	val, _ := json.Marshal(evt)
	
	// Create span for Kafka publish operation
	spanName := fmt.Sprintf("kafka.publish.%s", topic)
	ctx, span := p.tracer.Start(ctx, spanName)
	defer span.End()
	
	// Add attributes to the span
	span.SetAttributes(
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination", topic),
		attribute.String("messaging.destination_kind", "topic"),
		attribute.String("messaging.message_id", evt.AggregateID),
		attribute.String("messaging.operation", "publish"),
		attribute.String("event.type", evt.EventType),
		attribute.String("event.version", evt.EventVersion),
	)
	
	err := p.w.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: val,
	})
	
	if err != nil {
		span.RecordError(err)
		return err
	}
	
	return nil
}
