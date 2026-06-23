package discovery

import (
	"fmt"
	"log"

	"github.com/hashicorp/consul/api"
)

type Registry struct {
	client *api.Client
}

func NewRegistry(addr string) (*Registry, error) {
	cfg := api.DefaultConfig()
	cfg.Address = addr
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Registry{client: client}, nil
}

func (r *Registry) Register(name, id, host string, port int) error {
	return r.client.Agent().ServiceRegister(&api.AgentServiceRegistration{
		ID:      id,
		Name:    name,
		Address: host,
		Port:    port,
		Check: &api.AgentServiceCheck{
			GRPC:                           fmt.Sprintf("%s:%d", host, port),
			Interval:                       "10s",
			Timeout:                        "2s",
			DeregisterCriticalServiceAfter: "30s",
		},
	})
}

func (r *Registry) Deregister(id string) error {
	return r.client.Agent().ServiceDeregister(id)
}

func (r *Registry) Discover(name string) (string, error) {
	entries, _, err := r.client.Health().Service(name, "", true, nil)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no healthy instances of %q", name)
	}
	e := entries[0]
	addr := fmt.Sprintf("%s:%d", e.Service.Address, e.Service.Port)
	log.Printf("discovered %s at %s", name, addr)
	return addr, nil
}
