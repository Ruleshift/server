package runtimeclient

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type delayedModuleServer struct {
	modulev1.UnimplementedModuleRuntimeServer
	token string
}

func (s delayedModuleServer) Describe(ctx context.Context, _ *modulev1.DescribeRequest) (*modulev1.DescribeResponse, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 || values[0] != "Bearer "+s.token {
		return nil, status.Error(codes.Unauthenticated, "invalid module RPC token")
	}
	return &modulev1.DescribeResponse{
		ModuleId:        "sample",
		Version:         "1.0.0",
		AbiVersion:      1,
		StateTypeUrl:    "type.googleapis.com/example.State",
		CommandTypeUrls: []string{"type.googleapis.com/example.Command"},
	}, nil
}

func TestConnectorWaitsForModuleServiceToBecomeReachable(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	if err = probe.Close(); err != nil {
		t.Fatal(err)
	}

	serverReady := make(chan *grpc.Server, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		listener, listenErr := net.Listen("tcp", address)
		if listenErr != nil {
			return
		}
		server := grpc.NewServer()
		modulev1.RegisterModuleRuntimeServer(server, delayedModuleServer{token: "secret"})
		serverReady <- server
		_ = server.Serve(listener)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	version := controlplane.Version{
		Ref: module.ModuleRef{ModuleID: "sample", Version: "1.0.0"},
		Manifest: controlplane.Manifest{
			StateTypeURL:         "type.googleapis.com/example.State",
			CommandTypeURLs:      []string{"type.googleapis.com/example.Command"},
			TransitionDeadlineMS: 50,
		},
	}
	_, description, err := (Connector{}).Connect(ctx, controlplane.RuntimeDeployment{Endpoint: address, RPCToken: "secret"}, version)
	if err != nil {
		t.Fatal(err)
	}
	if description.ModuleID != "sample" {
		t.Fatalf("module id = %q", description.ModuleID)
	}

	select {
	case server := <-serverReady:
		server.Stop()
	case <-time.After(time.Second):
		t.Fatal("module server did not start")
	}
}
