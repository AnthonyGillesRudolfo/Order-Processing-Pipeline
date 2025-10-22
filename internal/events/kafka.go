package events

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
)

type Producer struct{ w *kafka.Writer }

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
	return p.w.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: val,
	})
}
