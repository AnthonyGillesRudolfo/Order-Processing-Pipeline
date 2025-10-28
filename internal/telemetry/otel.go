package telemetry

import (
	"context"
	"log"
	"os"
    "net/url"
    "strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func InitTracer(serviceName string) func() {
	ctx := context.Background()

    // Resolve OTLP endpoint from env; accept full URL or host:port
    raw := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    if raw == "" {
        raw = "http://localhost:4318/v1/traces"
    }
    endpoint := "localhost:4318"
    path := "/v1/traces"
    insecure := true
    if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
        if u, err := url.Parse(raw); err == nil {
            if u.Host != "" { endpoint = u.Host }
            if u.Path != "" { path = u.Path }
            insecure = (u.Scheme == "http")
        }
    } else {
        // host:port form
        endpoint = raw
    }

    // Create OTLP HTTP exporter
    opts := []otlptracehttp.Option{
        otlptracehttp.WithEndpoint(endpoint),
        otlptracehttp.WithURLPath(path),
    }
    if insecure { opts = append(opts, otlptracehttp.WithInsecure()) }
    client := otlptracehttp.NewClient(opts...)
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		log.Fatalf("Failed to create OTLP exporter: %v", err)
	}

	// Create resource with service information
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create resource: %v", err)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(1.0)), // Sample all traces for development
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Printf("OpenTelemetry initialized for service: %s", serviceName)

	// Return cleanup function
	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}
}
