// Package runtimeclient connects the core to stateless module services.
package runtimeclient

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
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
	connection, err := r.connection(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", module.ErrUnavailable, err)
	}
	commands := map[string]struct{}{}
	for _, value := range endpoint.CommandTypeURLs {
		commands[value] = struct{}{}
	}
	return module.NewGRPCClient(modulev1.NewModuleRuntimeClient(connection), module.GRPCClientConfig{StateTypeURL: endpoint.StateTypeURL, CommandTypeURLs: commands, TransitionDeadline: endpoint.TransitionDeadline})
}
func (r *Resolver) connection(ctx context.Context, endpoint Endpoint) (*grpc.ClientConn, error) {
	key := endpoint.Address + "\x00" + endpoint.Token
	r.mu.Lock()
	if existing := r.connections[key]; existing != nil {
		r.mu.Unlock()
		return existing, nil
	}
	r.mu.Unlock()
	options := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(module.MaxStateBytes+module.MaxMessageBytes), grpc.MaxCallSendMsgSize(module.MaxStateBytes+module.MaxMessageBytes))}
	if endpoint.Token != "" {
		options = append(options, grpc.WithPerRPCCredentials(tokenCredential{token: endpoint.Token}))
	}
	connection, err := grpc.NewClient(endpoint.Address, options...)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if existing := r.connections[key]; existing != nil {
		r.mu.Unlock()
		connection.Close()
		return existing, nil
	}
	r.connections[key] = connection
	r.mu.Unlock()
	return connection, nil
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

type Connector struct{}

func (Connector) Connect(ctx context.Context, deployment controlplane.RuntimeDeployment, version controlplane.Version) (module.Runtime, controlplane.Description, error) {
	endpoint := Endpoint{Address: deployment.Endpoint, Token: deployment.RPCToken, StateTypeURL: version.Manifest.StateTypeURL, CommandTypeURLs: version.Manifest.CommandTypeURLs, TransitionDeadline: time.Duration(version.Manifest.TransitionDeadlineMS) * time.Millisecond}
	resolver := NewResolver(endpointSource{endpoint: endpoint})
	runtime, err := resolver.Resolve(ctx, version.Ref)
	if err != nil {
		return nil, controlplane.Description{}, err
	}
	connection, err := resolver.connection(ctx, endpoint)
	if err != nil {
		return nil, controlplane.Description{}, err
	}
	requestCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+deployment.RPCToken)
	response, err := modulev1.NewModuleRuntimeClient(connection).Describe(requestCtx, &modulev1.DescribeRequest{})
	if err != nil {
		return nil, controlplane.Description{}, err
	}
	description := controlplane.Description{ModuleID: response.ModuleId, Version: response.Version, ABIVersion: response.AbiVersion, StateTypeURL: response.StateTypeUrl, CommandTypeURLs: append([]string(nil), response.CommandTypeUrls...), DescriptorDigest: "sha256:" + hex.EncodeToString(response.DescriptorSetSha256), SupportsPlayerLeft: response.SupportsPlayerLeft}
	return runtime, description, nil
}

type endpointSource struct{ endpoint Endpoint }

func (s endpointSource) Endpoint(context.Context, module.ModuleRef) (Endpoint, error) {
	return s.endpoint, nil
}
