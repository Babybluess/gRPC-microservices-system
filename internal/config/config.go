package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/hashicorp/consul/api"
)

const (
	EnvConsulAddr  = "CONSUL_ADDR"
	EnvOTLPAddr    = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvTLSCert     = "TLS_CERT_FILE"
	EnvTLSKey      = "TLS_KEY_FILE"
	EnvTLSCA       = "TLS_CA_FILE"
	EnvAuthToken   = "AUTH_TOKEN"
	EnvPort        = "PORT"
	EnvHTTPPort    = "HTTP_PORT"
	EnvServiceHost = "SERVICE_HOST"
)

const (
	DefaultConsulAddr  = "localhost:8500"
	DefaultOTLPAddr    = "localhost:4317"
	DefaultCertFile    = "certs/server.pem"
	DefaultKeyFile     = "certs/server-key.pem"
	DefaultCAFile      = "certs/ca.pem"
	DefaultAuthToken   = "Bearer secret-token"
	DefaultHTTPPort    = ":8080"
	DefaultServiceHost = "localhost"
	KVAuthToken        = "grpcshop/auth-token"
)

type Config struct {
	ConsulAddr  string
	OTLPAddr    string
	CertFile    string
	KeyFile     string
	CAFile      string
	AuthToken   string
	Port        int
	HTTPPort    string
	ServiceHost string
}

func Load(callerDefaults Config) (*Config, error) {
	consulAddr := envOr(EnvConsulAddr, firstNonEmpty(callerDefaults.ConsulAddr, DefaultConsulAddr))

	cfg := &Config{
		ConsulAddr:  consulAddr,
		OTLPAddr:    envOr(EnvOTLPAddr, firstNonEmpty(callerDefaults.OTLPAddr, DefaultOTLPAddr)),
		CertFile:    envOr(EnvTLSCert, firstNonEmpty(callerDefaults.CertFile, DefaultCertFile)),
		KeyFile:     envOr(EnvTLSKey, firstNonEmpty(callerDefaults.KeyFile, DefaultKeyFile)),
		CAFile:      envOr(EnvTLSCA, firstNonEmpty(callerDefaults.CAFile, DefaultCAFile)),
		HTTPPort:    envOr(EnvHTTPPort, firstNonEmpty(callerDefaults.HTTPPort, DefaultHTTPPort)),
		ServiceHost: envOr(EnvServiceHost, firstNonEmpty(callerDefaults.ServiceHost, DefaultServiceHost)),
	}

	if s := os.Getenv(EnvPort); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("invalid %s=%q: %w", EnvPort, s, err)
		}
		cfg.Port = p
	} else {
		cfg.Port = callerDefaults.Port
	}

	cfg.AuthToken = os.Getenv(EnvAuthToken)
	if cfg.AuthToken == "" {
		cfg.AuthToken = kvGet(consulAddr, KVAuthToken)
	}
	if cfg.AuthToken == "" {
		cfg.AuthToken = firstNonEmpty(callerDefaults.AuthToken, DefaultAuthToken)
	}

	return cfg, nil
}

func kvGet(consulAddr, key string) string {
	client, err := api.NewClient(&api.Config{Address: consulAddr})
	if err != nil {
		return ""
	}
	pair, _, err := client.KV().Get(key, nil)
	if err != nil || pair == nil {
		return ""
	}
	if len(pair.Value) > 0 {
		log.Printf("config: loaded %q from Consul KV", key)
	}
	return string(pair.Value)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
