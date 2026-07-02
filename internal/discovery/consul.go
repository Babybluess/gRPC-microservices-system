package discovery

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"google.golang.org/grpc/resolver"
)

// ── Registry ──────────────────────────────────────────────────────────────────

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
			GRPCUseTLS:                     true,
			TLSSkipVerify:                  true,
			Interval:                       "10s",
			Timeout:                        "2s",
			DeregisterCriticalServiceAfter: "30s",
		},
	})
}

func (r *Registry) Deregister(id string) error {
	return r.client.Agent().ServiceDeregister(id)
}

// DiscoverAll returns the addresses of every healthy instance of name.
func (r *Registry) DiscoverAll(name string) ([]string, error) {
	entries, _, err := r.client.Health().Service(name, "", true, nil)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(entries))
	for _, e := range entries {
		addrs = append(addrs, fmt.Sprintf("%s:%d", e.Service.Address, e.Service.Port))
	}
	return addrs, nil
}

// Discover returns the address of the first healthy instance (kept for
// backwards-compatibility; prefer the resolver for production use).
func (r *Registry) Discover(name string) (string, error) {
	addrs, err := r.DiscoverAll(name)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no healthy instances of %q", name)
	}
	log.Printf("discovered %s at %s", name, addrs[0])
	return addrs[0], nil
}

// ── gRPC resolver ─────────────────────────────────────────────────────────────

const Scheme = "consul"

// NewBuilder returns a resolver.Builder backed by the given Registry.
// Register it once at startup with resolver.Register(discovery.NewBuilder(reg)).
func NewBuilder(reg *Registry) resolver.Builder {
	return &consulBuilder{registry: reg}
}

type consulBuilder struct {
	registry *Registry
}

func (b *consulBuilder) Scheme() string { return Scheme }

func (b *consulBuilder) Build(
	target resolver.Target,
	cc resolver.ClientConn,
	_ resolver.BuildOptions,
) (resolver.Resolver, error) {
	r := &consulResolver{
		registry:   b.registry,
		name:       target.Endpoint(),
		cc:         cc,
		resolveNow: make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	go r.watch()
	return r, nil
}

// ── consulResolver ────────────────────────────────────────────────────────────

type consulResolver struct {
	registry   *Registry
	name       string
	cc         resolver.ClientConn
	resolveNow chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
}

// ResolveNow is called by gRPC when it wants a fresh address list (e.g. after
// a subchannel failure). We send on a buffered channel so rapid calls collapse.
func (r *consulResolver) ResolveNow(_ resolver.ResolveNowOptions) {
	select {
	case r.resolveNow <- struct{}{}:
	default:
	}
}

func (r *consulResolver) Close() {
	r.closeOnce.Do(func() { close(r.done) })
}

// watch resolves immediately on startup, then re-resolves every 30 s or
// whenever gRPC asks via ResolveNow.
func (r *consulResolver) watch() {
	r.resolve()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.resolveNow:
			r.resolve()
		case <-ticker.C:
			r.resolve()
		case <-r.done:
			return
		}
	}
}

func (r *consulResolver) resolve() {
	addrs, err := r.registry.DiscoverAll(r.name)
	if err != nil {
		r.cc.ReportError(err)
		return
	}
	if len(addrs) == 0 {
		r.cc.ReportError(fmt.Errorf("no healthy instances of %q", r.name))
		return
	}

	state := resolver.State{Addresses: make([]resolver.Address, len(addrs))}
	for i, a := range addrs {
		state.Addresses[i] = resolver.Address{Addr: a}
	}
	log.Printf("resolver: %s → %v", r.name, addrs)
	if err := r.cc.UpdateState(state); err != nil {
		log.Printf("resolver: UpdateState %s: %v", r.name, err)
	}
}
