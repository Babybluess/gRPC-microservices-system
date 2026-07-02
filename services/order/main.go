package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "grpcshop/gen/order"
	"grpcshop/internal/config"
	"grpcshop/internal/discovery"
	"grpcshop/internal/interceptors"
	"grpcshop/internal/tlsconfig"
	"grpcshop/internal/tracing"
)

const (
	serviceName = "order-service"
	serviceID   = "order-service-1"
)

type server struct {
	pb.UnimplementedOrderServiceServer
	orders []*pb.OrderResponse
}

func (s *server) CreateOrder(_ context.Context, req *pb.CreateOrderRequest) (*pb.OrderResponse, error) {
	order := &pb.OrderResponse{
		OrderId:   fmt.Sprintf("ord_%d", len(s.orders)+1),
		UserId:    req.UserId,
		ProductId: req.ProductId,
		Quantity:  req.Quantity,
	}
	s.orders = append(s.orders, order)
	log.Printf("created order %s for user %s", order.OrderId, order.UserId)
	return order, nil
}

func (s *server) ListOrders(req *pb.ListOrdersRequest, stream pb.OrderService_ListOrdersServer) error {
	for _, o := range s.orders {
		if o.UserId != req.UserId {
			continue
		}
		if err := stream.Send(o); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func main() {
	cfg, err := config.Load(config.Config{Port: 50052})
	if err != nil {
		log.Fatal("config:", err)
	}

	ctx := context.Background()

	shutdownTracer, err := tracing.Init(ctx, serviceName, cfg.OTLPAddr)
	if err != nil {
		log.Fatal("tracer:", err)
	}

	tlsCreds, err := tlsconfig.Server(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		log.Fatal("tls:", err)
	}

	registry, err := discovery.NewRegistry(cfg.ConsulAddr)
	if err != nil {
		log.Fatal("consul:", err)
	}
	if err := registry.Register(serviceName, serviceID, cfg.ServiceHost, cfg.Port); err != nil {
		log.Fatal("register:", err)
	}
	defer registry.Deregister(serviceID)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(tlsCreds),
		grpc.ChainUnaryInterceptor(
			interceptors.UnaryLogger,
			interceptors.UnaryTracing,
			interceptors.UnaryAuth(cfg.AuthToken),
		),
	)
	pb.RegisterOrderServiceServer(grpcServer, &server{})
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	reflection.Register(grpcServer)

	log.Printf("order-service listening on :%d", cfg.Port)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		<-quit
		grpcServer.GracefulStop()
		_ = shutdownTracer(ctx)
	}()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
