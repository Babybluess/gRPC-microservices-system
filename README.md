# grpc-shop

gRPC microservices system in Go with Protobuf, Consul service discovery, mutual TLS, OpenTelemetry tracing, client-side load balancing, an HTTP/JSON reverse proxy, config management via env vars and Consul KV, and typed gRPC error codes.

## Prerequisites

- Go 1.22+
- [mkcert](https://github.com/FiloSottile/mkcert) — local TLS certificates (`brew install mkcert`)
- [consul](https://developer.hashicorp.com/consul/install) — service registry (`brew install consul`)
- Docker — Jaeger tracing backend
- protoc + plugins (only needed to regenerate `.proto` files):
  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
  ```

## Setup

### 1. TLS certificates

```bash
brew install mkcert
sudo mkcert -install          # installs the local CA into the system trust store (one-time, needs sudo)

mkdir certs
mkcert -cert-file certs/server.pem -key-file certs/server-key.pem localhost 127.0.0.1 ::1
cp "$(mkcert -CAROOT)/rootCA.pem" certs/ca.pem
```

`certs/` is gitignored — never commit private keys.

### 2. Go dependencies

```bash
go mod tidy
```

### 3. Regenerate protobuf code (only when `.proto` files change)

```bash
make proto
```

## Configuration

All runtime values are controlled by environment variables. Copy `.env.example` to `.env`, adjust, then `source .env` before running any binary.

| Env var | Default | Used by |
|---|---|---|
| `CONSUL_ADDR` | `localhost:8500` | all |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | all |
| `TLS_CERT_FILE` | `certs/server.pem` | services |
| `TLS_KEY_FILE` | `certs/server-key.pem` | services |
| `TLS_CA_FILE` | `certs/ca.pem` | gateways |
| `AUTH_TOKEN` | `Bearer secret-token` | all |
| `PORT` | `50051` / `50052` | services |
| `HTTP_PORT` | `:8080` | http-gateway |
| `SERVICE_HOST` | `localhost` | services |

`AUTH_TOKEN` is resolved in priority order: `AUTH_TOKEN` env var → Consul KV key `grpcshop/auth-token` → built-in default. To rotate the token without a redeploy:

```bash
consul kv put grpcshop/auth-token "Bearer new-token"
# then restart each process
```

## Running

Start each component in a separate terminal, in order.

```bash
# Terminal 1 — Consul service registry
consul agent -dev

# Terminal 2 — Jaeger tracing UI  (http://localhost:16686)
docker run -d --name jaeger -p 16686:16686 -p 4317:4317 jaegertracing/all-in-one:latest

# Terminal 3 — user-service  (gRPC :50051)
go run services/user/main.go

# Terminal 4 — order-service  (gRPC :50052)
go run services/order/main.go

# Terminal 5 — HTTP/JSON gateway  (:8080)
go run cmd/http-gateway/main.go

# Terminal 6 — gRPC demo client (optional smoke test)
go run gateway/main.go
```

Wait ~10 s after starting a service before running gateways — Consul needs at least one health-check cycle to mark instances as healthy.

## HTTP API (cmd/http-gateway)

The HTTP gateway translates REST calls to gRPC automatically from proto annotations. No extra code — add a route by annotating the `.proto` file and running `make proto`.

```bash
# Register a user
curl -s -X POST http://localhost:8080/v1/users \
  -H "Authorization: Bearer secret-token" \
  -H "Content-Type: application/json" \
  -d '{"email":"bob@test.com","name":"Bob"}'
# → {"userId":"usr_1"}

# Get a user
curl -s http://localhost:8080/v1/users/usr_1 \
  -H "Authorization: Bearer secret-token"

# Create an order
curl -s -X POST http://localhost:8080/v1/orders \
  -H "Authorization: Bearer secret-token" \
  -H "Content-Type: application/json" \
  -d '{"userId":"usr_1","productId":"prod_42","quantity":3}'

# Stream orders (newline-delimited JSON)
curl -s http://localhost:8080/v1/users/usr_1/orders \
  -H "Authorization: Bearer secret-token"
```

## Inspect with grpcurl

Services require TLS. Pass `-cacert` and the auth header:

```bash
grpcurl -cacert certs/ca.pem \
  -H "authorization: Bearer secret-token" \
  -d '{"email":"bob@test.com","name":"Bob"}' \
  localhost:50051 user.UserService/Register
```

## gRPC error codes

Services return typed `google.golang.org/grpc/status` errors. Clients can branch on the code:

| Situation | Code |
|---|---|
| Missing required field | `codes.InvalidArgument` |
| Resource not found | `codes.NotFound` |
| Duplicate registration | `codes.AlreadyExists` |
| Missing / wrong token | `codes.Unauthenticated` |
| Server-side failure | `codes.Internal` |

## Observability

- **Consul UI** — http://localhost:8500 — registered services and health-check status
- **Jaeger UI** — http://localhost:16686 — distributed traces; select service `gateway` or `http-gateway`
  - Client errors (`NotFound`, `InvalidArgument`, etc.) appear on traces but do **not** mark the span as Error — only server-side failures do
- Override the OTLP endpoint (e.g. Grafana Tempo): `OTEL_EXPORTER_OTLP_ENDPOINT=tempo:4317`

## Layout

```
grpc-shop/
├── proto/
│   ├── user/user.proto             HTTP annotations: POST /v1/users, GET /v1/users/{user_id}
│   └── order/order.proto           HTTP annotations: POST /v1/orders, GET /v1/users/{user_id}/orders
├── third_party/googleapis/         vendored google/api/annotations.proto (needed by protoc)
├── gen/                            generated by protoc (run make proto)
├── certs/                          gitignored — generated by mkcert
│   ├── server.pem / server-key.pem
│   └── ca.pem
├── services/
│   ├── user/main.go                UserService  :50051
│   └── order/main.go               OrderService :50052
├── gateway/main.go                 gRPC demo client (register → order → list)
├── cmd/
│   └── http-gateway/main.go        HTTP/JSON → gRPC reverse proxy  :8080
├── internal/
│   ├── config/config.go            env var + Consul KV config loader
│   ├── discovery/consul.go         Consul register / resolver.Builder (round-robin)
│   ├── interceptors/interceptors.go  logging · auth(token) · tracing interceptors
│   ├── tlsconfig/tls.go            server + client TLS credential helpers
│   └── tracing/tracing.go          OTel TracerProvider init + gRPC MetadataCarrier
└── Makefile
```

## Architecture

```
HTTP clients (curl, browser, mobile)
  │  HTTP/JSON  :8080
  ▼
cmd/http-gateway          ← grpc-gateway reverse proxy
  │
  │  gRPC + TLS + round-robin (consul resolver)
  │  W3C traceparent in metadata · Bearer token forwarded
  ├─► user-service  :50051
  │     interceptors: logger → tracing → auth(token)
  └─► order-service :50052
        interceptors: logger → tracing → auth(token)

gateway/main.go  ← gRPC demo client (smoke test, not a server)

Consul :8500   — service registry; gRPC health checks every 10 s
Jaeger :4317   — OTLP collector; UI on :16686
```

## Interceptor chain

| Interceptor | Side | Purpose |
|---|---|---|
| `UnaryLogger` | server | logs every RPC: method, duration, error |
| `UnaryTracing` | server | extracts `traceparent`, starts server span; sets span Error only for server-side codes |
| `UnaryAuth(token)` | server | validates `authorization` header against configured token |
| `UnaryClientTracing` | client | starts client span, injects `traceparent` into outgoing metadata |
| `StreamClientTracing` | client | same as above for server-streaming RPCs |

`/grpc.health.v1.Health/Check` bypasses `UnaryAuth` — Consul probes carry no application token.

## Next steps

- Replace static `Bearer` token with JWT validation (JWKS endpoint, expiry, claims)
- Add OTel metrics (`go.opentelemetry.io/otel/metric`) for request rates and latencies
- Add Consul blocking queries to the resolver for instant scale-up/down detection
- Add `protoc-gen-openapiv2` to `make proto` to auto-generate an OpenAPI spec from the proto annotations
