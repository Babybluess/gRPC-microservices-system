package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"

	orderpb "grpcshop/gen/order"
	userpb  "grpcshop/gen/user"
	"grpcshop/internal/config"
	"grpcshop/internal/discovery"
	"grpcshop/internal/tlsconfig"
	"grpcshop/internal/tracing"
)

const lbPolicy = `{"loadBalancingConfig": [{"round_robin": {}}]}`

func main() {
	cfg, err := config.Load(config.Config{})
	if err != nil {
		log.Fatal("config:", err)
	}

	ctx := context.Background()

	shutdownTracer, err := tracing.Init(ctx, "http-gateway", cfg.OTLPAddr)
	if err != nil {
		log.Fatal("tracer:", err)
	}
	defer shutdownTracer(ctx)

	tlsCreds, err := tlsconfig.Client(cfg.CAFile)
	if err != nil {
		log.Fatal("tls:", err)
	}

	reg, err := discovery.NewRegistry(cfg.ConsulAddr)
	if err != nil {
		log.Fatal("consul:", err)
	}
	resolver.Register(discovery.NewBuilder(reg))

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithAuthority("localhost"),
		grpc.WithDefaultServiceConfig(lbPolicy),
	}

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			if strings.EqualFold(key, "authorization") {
				return key, true
			}
			return runtime.DefaultHeaderMatcher(key)
		}),
	)

	if err := userpb.RegisterUserServiceHandlerFromEndpoint(
		ctx, mux, "consul:///user-service", dialOpts,
	); err != nil {
		log.Fatal("register user handler:", err)
	}
	if err := orderpb.RegisterOrderServiceHandlerFromEndpoint(
		ctx, mux, "consul:///order-service", dialOpts,
	); err != nil {
		log.Fatal("register order handler:", err)
	}

	log.Printf("http-gateway listening on %s", cfg.HTTPPort)
	log.Fatal(http.ListenAndServe(cfg.HTTPPort, mux))
}
