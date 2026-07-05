package kubernetes

import (
	"context"
	"strings"
	"testing"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 2 {
		t.Fatal("module must have two replicas")
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
}
