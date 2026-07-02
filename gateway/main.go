package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"

	orderpb "grpcshop/gen/order"
	userpb  "grpcshop/gen/user"
	"grpcshop/internal/config"
	"grpcshop/internal/discovery"
	"grpcshop/internal/interceptors"
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

	shutdownTracer, err := tracing.Init(ctx, "gateway", cfg.OTLPAddr)
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
		log.Fatal(err)
	}
	resolver.Register(discovery.NewBuilder(reg))

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithAuthority("localhost"),
		grpc.WithDefaultServiceConfig(lbPolicy),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientTracing),
		grpc.WithChainStreamInterceptor(interceptors.StreamClientTracing),
	}

	userConn  := mustDial("user-service", dialOpts...)
	orderConn := mustDial("order-service", dialOpts...)
	defer userConn.Close()
	defer orderConn.Close()

	userClient  := userpb.NewUserServiceClient(userConn)
	orderClient := orderpb.NewOrderServiceClient(orderConn)

	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", cfg.AuthToken)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	regResp, err := userClient.Register(ctx, &userpb.RegisterRequest{
		Email: "alice@example.com",
		Name:  "Alice",
	})
	if err != nil {
		log.Fatal("register:", err)
	}
	log.Printf("registered: user_id=%s", regResp.UserId)

	orderResp, err := orderClient.CreateOrder(ctx, &orderpb.CreateOrderRequest{
		UserId:    regResp.UserId,
		ProductId: "prod_42",
		Quantity:  3,
	})
	if err != nil {
		log.Fatal("create order:", err)
	}
	log.Printf("order created: %s", orderResp.OrderId)

	stream, err := orderClient.ListOrders(ctx, &orderpb.ListOrdersRequest{UserId: regResp.UserId})
	if err != nil {
		log.Fatal("list orders:", err)
	}
	log.Println("orders:")
	for {
		o, err := stream.Recv()
		if err != nil {
			break
		}
		log.Printf("  order_id=%s product=%s qty=%d", o.OrderId, o.ProductId, o.Quantity)
	}
}

func mustDial(name string, opts ...grpc.DialOption) *grpc.ClientConn {
	conn, err := grpc.NewClient("consul:///"+name, opts...)
	if err != nil {
		log.Fatalf("dial %s: %v", name, err)
	}
	return conn
}
