.PHONY: proto
proto:
	protoc --go_out=gen --go_opt=paths=source_relative \
	       --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	       proto/user/user.proto proto/order/order.proto
