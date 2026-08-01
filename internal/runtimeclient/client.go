// Package runtimeclient connects the core to stateless module services.
package runtimeclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/module"
	modulev2 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Endpoint struct {
	Address            string
	Token              string
	StateTypeURL       string
	CommandTypeURLs    []string
	TransitionDeadline time.Duration
}
type EndpointSource interface {
	Endpoint(context.Context, module.ModuleRef) (Endpoint, error)
}

type Resolver struct {
	Source      EndpointSource
	mu          sync.Mutex
	connections map[string]*grpc.ClientConn
}

func NewResolver(source EndpointSource) *Resolver {
	return &Resolver{Source: source, connections: map[string]*grpc.ClientConn{}}
}
func (r *Resolver) Resolve(ctx context.Context, ref module.ModuleRef) (module.Runtime, error) {
	if r.Source == nil {
		return nil, module.ErrUnavailable
	}
	endpoint, err := r.Source.Endpoint(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", module.ErrUnavailable, err)
	}
	connection, err := r.connection(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", module.ErrUnavailable, err)
	}
	return runtimeFor(connection, endpoint)
}

func runtimeFor(connection *grpc.ClientConn, endpoint Endpoint) (module.Runtime, error) {
	commands := map[string]struct{}{}
	for _, value := range endpoint.CommandTypeURLs {
		commands[value] = struct{}{}
	}
	return module.NewGRPCClient(
		modulev2.NewModuleRuntimeClient(connection),
		module.GRPCClientConfig{
			StateTypeURL:       endpoint.StateTypeURL,
			CommandTypeURLs:    commands,
			TransitionDeadline: endpoint.TransitionDeadline,
		},
	)
}

func (r *Resolver) connection(endpoint Endpoint) (*grpc.ClientConn, error) {
	key := endpoint.Address + "\x00" + endpoint.Token
	r.mu.Lock()
	if existing := r.connections[key]; existing != nil {
		r.mu.Unlock()
		return existing, nil
	}
	r.mu.Unlock()
	connection, err := newConnection(endpoint)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.connections[key]; existing != nil {
		_ = connection.Close()
		return existing, nil
	}
	r.connections[key] = connection
	return connection, nil
}

func newConnection(endpoint Endpoint) (*grpc.ClientConn, error) {
	maxMessageBytes := module.MaxStateBytes + module.MaxMessageBytes
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	}
	if endpoint.Token != "" {
		options = append(options, grpc.WithPerRPCCredentials(tokenCredential{token: endpoint.Token}))
	}
	return grpc.NewClient(endpoint.Address, options...)
}

func (r *Resolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var first error
	for _, connection := range r.connections {
		if err := connection.Close(); err != nil && first == nil {
			first = err
		}
	}
	r.connections = map[string]*grpc.ClientConn{}
	return first
}

type tokenCredential struct{ token string }

func (c tokenCredential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}
func (c tokenCredential) RequireTransportSecurity() bool { return false }

var _ credentials.PerRPCCredentials = tokenCredential{}
