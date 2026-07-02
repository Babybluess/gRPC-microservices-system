package interceptors

import (
	"context"
	"io"
	"log"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"grpcshop/internal/tracing"
)

const tracerName = "grpcshop"

// ── server-side interceptors ──────────────────────────────────────────────────

func UnaryLogger(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	log.Printf("rpc=%s dur=%s err=%v", info.FullMethod, time.Since(start), err)
	return resp, err
}

func UnaryAuth(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if info.FullMethod == "/grpc.health.v1.Health/Check" {
		return handler(ctx, req)
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	tokens := md.Get("authorization")
	if len(tokens) == 0 || tokens[0] != "Bearer secret-token" {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return handler(ctx, req)
}

// UnaryTracing extracts the incoming W3C trace context from gRPC metadata,
// starts a server span for the RPC, and records any error on the span.
func UnaryTracing(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	ctx = otel.GetTextMapPropagator().Extract(ctx, tracing.MetadataCarrier{MD: md})

	svc, method := splitMethod(info.FullMethod)
	ctx, span := otel.Tracer(tracerName).Start(ctx, info.FullMethod,
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		oteltrace.WithAttributes(
			semconv.RPCSystemGRPC,
			semconv.RPCServiceKey.String(svc),
			semconv.RPCMethodKey.String(method),
		),
	)
	defer span.End()

	resp, err := handler(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
	}
	return resp, err
}

// ── client-side interceptors ──────────────────────────────────────────────────

// UnaryClientTracing starts a client span and injects the trace context into
// the outgoing gRPC metadata so the server can continue the same trace.
func UnaryClientTracing(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	svc, rpc := splitMethod(method)
	ctx, span := otel.Tracer(tracerName).Start(ctx, method,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			semconv.RPCSystemGRPC,
			semconv.RPCServiceKey.String(svc),
			semconv.RPCMethodKey.String(rpc),
		),
	)
	defer span.End()

	ctx = injectMetadata(ctx)

	err := invoker(ctx, method, req, reply, cc, opts...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
	}
	return err
}

// StreamClientTracing does the same for server-streaming (and bidirectional)
// RPCs. It wraps the returned stream so the span ends when the stream closes.
func StreamClientTracing(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	svc, rpc := splitMethod(method)
	ctx, span := otel.Tracer(tracerName).Start(ctx, method,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			semconv.RPCSystemGRPC,
			semconv.RPCServiceKey.String(svc),
			semconv.RPCMethodKey.String(rpc),
		),
	)

	ctx = injectMetadata(ctx)

	stream, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
		span.End()
		return nil, err
	}
	return &tracedStream{ClientStream: stream, span: span}, nil
}

// tracedStream ends the span when the stream receives EOF or an error.
type tracedStream struct {
	grpc.ClientStream
	span oteltrace.Span
}

func (s *tracedStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err == io.EOF {
		s.span.End()
	} else if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(otelcodes.Error, err.Error())
		s.span.End()
	}
	return err
}

// ── helpers ───────────────────────────────────────────────────────────────────

// injectMetadata copies outgoing metadata from ctx, injects the OTel trace
// context into it, and returns a new ctx with the enriched metadata.
func injectMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}
	otel.GetTextMapPropagator().Inject(ctx, tracing.MetadataCarrier{MD: md})
	return metadata.NewOutgoingContext(ctx, md)
}

// splitMethod turns "/package.Service/Method" into ("package.Service", "Method").
func splitMethod(fullMethod string) (svc, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return "", trimmed
	}
	return trimmed[:idx], trimmed[idx+1:]
}

// ChainUnary is kept for reference; prefer grpc.ChainUnaryInterceptor.
func ChainUnary(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		chain := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			next := chain
			ic := interceptors[i]
			chain = func(c context.Context, r any) (any, error) {
				return ic(c, r, info, next)
			}
		}
		return chain(ctx, req)
	}
}
