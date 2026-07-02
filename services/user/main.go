package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "grpcshop/gen/user"
	"grpcshop/internal/config"
	"grpcshop/internal/discovery"
	"grpcshop/internal/interceptors"
	"grpcshop/internal/tlsconfig"
	"grpcshop/internal/tracing"
)

const (
	serviceName = "user-service"
	serviceID   = "user-service-1"
)

type server struct {
	pb.UnimplementedUserServiceServer
	users map[string]*pb.UserResponse
}

func (s *server) Register(_ context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	id := fmt.Sprintf("usr_%d", len(s.users)+1)
	s.users[id] = &pb.UserResponse{UserId: id, Email: req.Email, Name: req.Name}
	log.Printf("registered user %s (%s)", id, req.Email)
	return &pb.RegisterResponse{UserId: id}, nil
}

func (s *server) GetUser(_ context.Context, req *pb.GetUserRequest) (*pb.UserResponse, error) {
	u, ok := s.users[req.UserId]
	if !ok {
		return nil, fmt.Errorf("user %q not found", req.UserId)
	}
	return u, nil
}

func main() {
	cfg, err := config.Load(config.Config{Port: 50051})
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
	pb.RegisterUserServiceServer(grpcServer, &server{users: make(map[string]*pb.UserResponse)})
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	reflection.Register(grpcServer)

	log.Printf("user-service listening on :%d", cfg.Port)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		<-quit
		log.Println("shutting down user-service...")
		grpcServer.GracefulStop()
		_ = shutdownTracer(ctx)
	}()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
