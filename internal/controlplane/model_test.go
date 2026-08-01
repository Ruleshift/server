package controlplane

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func validPublish() PublishRequest {
	pkg, name, syntax := "example.sample", "sample.proto", "proto3"
	stateName, commandName := "State", "Command"
	descriptor, _ := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{Name: &name, Package: &pkg, Syntax: &syntax, MessageType: []*descriptorpb.DescriptorProto{{Name: &stateName}, {Name: &commandName}}}}})
	return PublishRequest{DeveloperID: "dev", ModuleID: "sample", ImageRef: "registry.example/sample@sha256:" + strings.Repeat("a", 64), Manifest: Manifest{ModuleID: "sample", Version: "1.2.3", ABIVersion: 2, MinPlayers: 2, MaxPlayers: 4, StateTypeURL: "type.googleapis.com/example.sample.State", CommandTypeURLs: []string{"type.googleapis.com/example.sample.Command"}}, DescriptorSet: descriptor, Vectors: []byte(`{"vectors":[1]}`)}
}
func TestPublishRequestRejectsTagOnlyAndReservedTypes(t *testing.T) {
	request := validPublish()
	request.ImageRef = "registry.example/sample:latest"
	if err := request.Validate(); err == nil {
		t.Fatal("tag-only reference accepted")
	}
	request = validPublish()
	request.Manifest.StateTypeURL = "type.googleapis.com/ruleshift.v2.StateSnapshot"
	if err := request.Validate(); err == nil {
		t.Fatal("reserved Ruleshift type accepted")
	}
	request = validPublish()
	request.Manifest.StateTypeURL = "type.googleapis.com/ruleshift.module.future.State"
	if err := request.Validate(); err == nil {
		t.Fatal("reserved module-runtime namespace accepted")
	}
}
func TestVersionPublishIsIdempotentAndDigestConflictFails(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_, _ = store.CreateModule(ctx, Module{DeveloperID: "dev", Key: "sample", DisplayName: "Sample"})
	request := validPublish()
	version := request.Version()
	if _, created, err := store.PutVersion(ctx, version); err != nil || !created {
		t.Fatalf("first put created=%v err=%v", created, err)
	}
	if _, created, err := store.PutVersion(ctx, version); err != nil || created {
		t.Fatalf("idempotent put created=%v err=%v", created, err)
	}
	version.Ref.ImageDigest = "sha256:" + strings.Repeat("b", 64)
	if _, _, err := store.PutVersion(ctx, version); err != ErrVersionConflict {
		t.Fatalf("digest conflict = %v", err)
	}
}
func TestThreeViolationsDegradeVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_, _ = store.CreateModule(ctx, Module{DeveloperID: "dev", Key: "sample", DisplayName: "Sample"})
	version := validPublish().Version()
	_, _, _ = store.PutVersion(ctx, version)
	tracker := NewProtocolViolationTracker(store)
	for i := 0; i < 2; i++ {
		degraded, err := tracker.Record(ctx, version.Ref)
		if err != nil || degraded {
			t.Fatalf("violation %d degraded=%v err=%v", i, degraded, err)
		}
	}
	degraded, err := tracker.Record(ctx, version.Ref)
	if err != nil || !degraded {
		t.Fatalf("third violation degraded=%v err=%v", degraded, err)
	}
	stored, _ := store.GetVersion(ctx, "dev", "sample", "1.2.3")
	if stored.Status != StatusDegraded {
		t.Fatalf("status=%s", stored.Status)
	}
}
