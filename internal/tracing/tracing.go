package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/metadata"
)

// Init creates the global TracerProvider that exports spans via OTLP/gRPC to
// otlpAddr (host:port). Call the returned shutdown to flush spans before exit.
func Init(ctx context.Context, serviceName, otlpAddr string) (func(context.Context) error, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpAddr),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// MetadataCarrier adapts gRPC metadata to OTel's TextMapCarrier so the
// W3C TraceContext propagator can read/write traceparent and tracestate.
type MetadataCarrier struct{ MD metadata.MD }

func (c MetadataCarrier) Get(key string) string {
	vals := c.MD.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (c MetadataCarrier) Set(key, val string) { c.MD.Set(key, val) }

func (c MetadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.MD))
	for k := range c.MD {
		keys = append(keys, k)
	}
	return keys
}
