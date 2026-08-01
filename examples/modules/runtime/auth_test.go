package main

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestAuthorizeAllowsKubernetesHealthCheckWithoutToken(t *testing.T) {
	service := &server{token: "secret"}
	called := false

	_, err := service.authorize(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: healthv1.Health_Check_FullMethodName}, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("health handler was not called")
	}
}

func TestAuthorizeRejectsModuleCallWithoutToken(t *testing.T) {
	service := &server{token: "secret"}

	_, err := service.authorize(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/ruleshift.module.v2.ModuleRuntime/Describe"}, func(context.Context, any) (any, error) {
		t.Fatal("module handler must not be called")
		return nil, nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("authorize() error code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}
