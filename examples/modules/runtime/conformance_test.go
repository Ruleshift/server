package main

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestPublishedConformanceVectors(t *testing.T) {
	for _, id := range []string{"xiangqi", "hiddennumber", "cardgame"} {
		t.Run(id, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			grpcServer := grpc.NewServer()
			modulev1.RegisterModuleRuntimeServer(grpcServer, &server{id: id, version: "1.0.0", prefix: "type.googleapis.com/ruleshift.examples." + id + ".v1."})
			go grpcServer.Serve(listener)
			defer grpcServer.Stop()
			connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			runtime, err := module.NewGRPCClient(modulev1.NewModuleRuntimeClient(connection), module.GRPCClientConfig{StateTypeURL: "type.googleapis.com/ruleshift.examples." + id + ".v1.State", CommandTypeURLs: map[string]struct{}{"type.googleapis.com/ruleshift.examples." + id + ".v1.Command": {}}})
			if err != nil {
				t.Fatal(err)
			}
			vectors, err := os.ReadFile("../" + id + "/conformance.json")
			if err != nil {
				t.Fatal(err)
			}
			version := controlplane.Version{Manifest: controlplane.Manifest{StateTypeURL: "type.googleapis.com/ruleshift.examples." + id + ".v1.State"}}
			first, err := (controlplane.DefaultConformanceRunner{}).Run(context.Background(), runtime, version, vectors)
			if err != nil {
				t.Fatal(err)
			}
			second, err := (controlplane.DefaultConformanceRunner{}).Run(context.Background(), runtime, version, vectors)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatal("module output is not deterministic")
			}
		})
	}
}
