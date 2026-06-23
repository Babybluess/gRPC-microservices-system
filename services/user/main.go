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
	"google.golang.org/grpc/reflection"

	pb "grpcshop/gen/user"
	"grpcshop/internal/discovery"
	"grpcshop/internal/interceptors"
)

const (
	port        = 50051
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
	registry, err := discovery.NewRegistry("localhost:8500")
	if err != nil {
		log.Fatal("consul:", err)
	}
	if err := registry.Register(serviceName, serviceID, "localhost", port); err != nil {
		log.Fatal("register:", err)
	}
	defer registry.Deregister(serviceID)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			interceptors.UnaryLogger,
			interceptors.UnaryAuth,
		),
	)
	pb.RegisterUserServiceServer(grpcServer, &server{users: make(map[string]*pb.UserResponse)})
	reflection.Register(grpcServer)

	log.Printf("user-service listening on :%d", port)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		<-quit
		log.Println("shutting down user-service...")
		grpcServer.GracefulStop()
	}()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
