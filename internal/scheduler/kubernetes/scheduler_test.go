package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testVersion() controlplane.Version {
	return controlplane.Version{Ref: module.ModuleRef{DeveloperID: "developer-a", ModuleID: "sample", Version: "1.0.0", ImageDigest: "sha256:" + strings.Repeat("a", 64)}, ImageRef: "registry.example/sample@sha256:" + strings.Repeat("a", 64), CredentialName: "private"}
}
func TestDeploymentSecurityDefaults(t *testing.T) {
	client := fake.NewSimpleClientset()
	scheduler, _ := New(client)
	version := testVersion()
	deploymentInfo, err := scheduler.Deploy(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	if deploymentInfo.RPCToken == "" {
		t.Fatal("missing RPC token")
	}
	namespace := TenantNamespace(version.Ref.DeveloperID)
	deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), WorkloadName(version), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 {
		t.Fatal("module must temporarily use one replica")
	}
	pod := deployment.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("service account token is mounted")
	}
	container := pod.Containers[0]
	security := container.SecurityContext
	if security == nil || security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
		t.Fatal("container hardening is incomplete")
	}
	if len(security.Capabilities.Drop) != 1 || security.Capabilities.Drop[0] != "ALL" {
		t.Fatal("Linux capabilities are not dropped")
	}
	policies, err := client.NetworkingV1().NetworkPolicies(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil || len(policies.Items) < 2 {
		t.Fatalf("network policies=%d err=%v", len(policies.Items), err)
	}
	limitRange, err := client.CoreV1().LimitRanges(namespace).Get(context.Background(), "ruleshift-module-limits", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defaults := limitRange.Spec.Limits[0].Default
	if _, ok := defaults[corev1.ResourceCPU]; !ok {
		t.Fatal("container CPU default is missing")
	}
	if _, ok := defaults[corev1.ResourceMemory]; !ok {
		t.Fatal("container memory default is missing")
	}
	if _, ok := defaults[corev1.ResourceLimitsCPU]; ok {
		t.Fatal("LimitRange must not use ResourceQuota key limits.cpu")
	}
	if _, ok := defaults[corev1.ResourceLimitsMemory]; ok {
		t.Fatal("LimitRange must not use ResourceQuota key limits.memory")
	}
}

func TestCleanupPreservesInactiveVersionWhileRoomsArePinned(t *testing.T) {
	client := fake.NewSimpleClientset()
	scheduler, _ := New(client)
	version := testVersion()
	version.Status = controlplane.StatusInactive
	if _, err := scheduler.Deploy(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Cleanup(context.Background(), version, 1); err != nil {
		t.Fatal(err)
	}
	namespace := TenantNamespace(version.Ref.DeveloperID)
	if _, err := client.AppsV1().Deployments(namespace).Get(context.Background(), WorkloadName(version), metav1.GetOptions{}); err != nil {
		t.Fatalf("pinned deployment removed: %v", err)
	}
	if err := scheduler.Cleanup(context.Background(), version, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppsV1().Deployments(namespace).Get(context.Background(), WorkloadName(version), metav1.GetOptions{}); err == nil {
		t.Fatal("unpinned inactive deployment was not removed")
	}
	if _, err := client.CoreV1().Services(namespace).Get(context.Background(), WorkloadName(version), metav1.GetOptions{}); err == nil {
		t.Fatal("unpinned inactive service was not removed")
	}
	if _, err := client.CoreV1().Secrets(namespace).Get(context.Background(), WorkloadName(version)+"-rpc", metav1.GetOptions{}); err == nil {
		t.Fatal("unpinned inactive RPC secret was not removed")
	}
}

func TestWaitReadyReturnsReplicaSetFailedCreateImmediately(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-abc123",
			Namespace: namespace,
			Labels:    deployment.Spec.Selector.MatchLabels,
		},
		Status: appsv1.ReplicaSetStatus{Conditions: []appsv1.ReplicaSetCondition{{
			Type:    appsv1.ReplicaSetReplicaFailure,
			Status:  corev1.ConditionTrue,
			Reason:  "FailedCreate",
			Message: "exceeded quota: ruleshift-module-quota, requested: limits.cpu=500m, used: limits.cpu=4, hard: limits.cpu=4",
		}}},
	}
	client := fake.NewSimpleClientset(deployment, replicaSet)
	scheduler, _ := New(client)
	scheduler.pollInterval = time.Hour

	started := time.Now()
	err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{})
	if err == nil || !strings.Contains(err.Error(), "FailedCreate") || !strings.Contains(err.Error(), "exceeded quota") {
		t.Fatalf("WaitReady error = %v, want original FailedCreate quota message", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("WaitReady took %s, want immediate failure", elapsed)
	}
}

func TestWaitReadyDoesNotRequireReplicaSetListWhenDeploymentIsReady(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	deployment.Status.ReadyReplicas = *deployment.Spec.Replicas
	deployment.Status.UpdatedReplicas = *deployment.Spec.Replicas
	client := fake.NewSimpleClientset(deployment)
	client.Fake.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("forbidden"))
	})
	scheduler, _ := New(client)

	if err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{}); err != nil {
		t.Fatalf("WaitReady returned %v, want ready deployment without listing replicasets", err)
	}
}

func TestWaitReadyIgnoresForbiddenReplicaSetList(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	client := fake.NewSimpleClientset(deployment)
	client.Fake.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("forbidden"))
	})
	scheduler, _ := New(client)
	scheduler.pollInterval = time.Millisecond
	scheduler.waitTimeout = 25 * time.Millisecond

	err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{})
	if err == nil {
		t.Fatal("WaitReady unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "replicasets") {
		t.Fatalf("WaitReady error = %v, want timeout/deployment status instead of RBAC failure", err)
	}
}
