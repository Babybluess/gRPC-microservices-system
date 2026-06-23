package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	orderpb "grpcshop/gen/order"
	userpb  "grpcshop/gen/user"
	"grpcshop/internal/discovery"
)

func main() {
	registry, err := discovery.NewRegistry("localhost:8500")
	if err != nil {
		log.Fatal(err)
	}

	userConn  := mustDial(registry, "user-service")
	orderConn := mustDial(registry, "order-service")
	defer userConn.Close()
	defer orderConn.Close()

	userClient  := userpb.NewUserServiceClient(userConn)
	orderClient := orderpb.NewOrderServiceClient(orderConn)

	ctx := metadata.AppendToOutgoingContext(
		context.Background(),
		"authorization", "Bearer secret-token",
	)
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

func mustDial(r *discovery.Registry, name string) *grpc.ClientConn {
	addr, err := r.Discover(name)
	if err != nil {
		log.Fatalf("discover %s: %v", name, err)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial %s: %v", name, err)
	}
	return conn
}
