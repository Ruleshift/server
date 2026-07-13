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
	"k8s.io/apimachinery/pkg/api/resource"
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
	if deployment.Spec.Replicas != nil {
		t.Fatalf("MVP must not enforce a replica count, got %d", *deployment.Spec.Replicas)
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

func TestEnsureTenantReconcilesLegacyLimitRange(t *testing.T) {
	namespace := TenantNamespace("developer-a")
	legacy := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "ruleshift-module-limits", Namespace: namespace},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type: corev1.LimitTypeContainer,
			Default: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse("500m"),
				corev1.ResourceLimitsMemory: resource.MustParse("256Mi"),
			},
		}}},
	}
	client := fake.NewSimpleClientset(legacy)
	scheduler, _ := New(client)

	if err := scheduler.EnsureTenant(context.Background(), "developer-a"); err != nil {
		t.Fatal(err)
	}
	limits, err := client.CoreV1().LimitRanges(namespace).Get(context.Background(), legacy.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defaults := limits.Spec.Limits[0].Default
	if _, ok := defaults[corev1.ResourceCPU]; !ok {
		t.Fatal("reconciled LimitRange is missing cpu")
	}
	if _, ok := defaults[corev1.ResourceMemory]; !ok {
		t.Fatal("reconciled LimitRange is missing memory")
	}
	if _, ok := defaults[corev1.ResourceLimitsCPU]; ok {
		t.Fatal("reconciled LimitRange retained invalid limits.cpu key")
	}
	if _, ok := defaults[corev1.ResourceLimitsMemory]; ok {
		t.Fatal("reconciled LimitRange retained invalid limits.memory key")
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

func TestWaitReadyReturnsDeploymentReplicaFailure(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	deployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentReplicaFailure,
		Status:  corev1.ConditionTrue,
		Reason:  "FailedCreate",
		Message: "exceeded quota: ruleshift-module-quota",
	}}
	client := fake.NewSimpleClientset(deployment)
	scheduler, _ := New(client)

	err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{})
	if err == nil || !strings.Contains(err.Error(), "FailedCreate") || !strings.Contains(err.Error(), "exceeded quota") {
		t.Fatalf("WaitReady error = %v, want Deployment ReplicaFailure details", err)
	}
}

func TestWaitReadyAcceptsReadyWorkloadWithoutReplicaPolicy(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	configuredReplicas := int32(5)
	deployment.Spec.Replicas = &configuredReplicas
	deployment.Status.ReadyReplicas = 1
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 1
	client := fake.NewSimpleClientset(deployment)
	scheduler, _ := New(client)

	if err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{}); err != nil {
		t.Fatalf("WaitReady returned error for ready MVP workload: %v", err)
	}
}

func TestWaitReadyReturnsPodBlockingReason(t *testing.T) {
	for _, reason := range []string{"ErrImagePull", "ImagePullBackOff", "CreateContainerConfigError", "InvalidImageName"} {
		t.Run(reason, func(t *testing.T) {
			version := testVersion()
			namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
			deployment := BuildDeployment(version, namespace, name)
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-abc", Namespace: namespace, Labels: deployment.Spec.Selector.MatchLabels},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "module",
						State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
							Reason:  reason,
							Message: "registry denied pull: " + strings.Repeat("x", maxReadinessDiagnosticBytes*2),
						}},
					}},
				},
			}
			client := fake.NewSimpleClientset(deployment, pod)
			scheduler, _ := New(client)
			scheduler.recoverableFailureGracePeriod = 0

			err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{})
			if err == nil || !strings.Contains(err.Error(), reason) || !strings.Contains(err.Error(), pod.Name) || !strings.Contains(err.Error(), "registry denied pull") {
				t.Fatalf("WaitReady error = %v, want bounded pod blocking details", err)
			}
			if len(err.Error()) > maxReadinessDiagnosticBytes+256 {
				t.Fatalf("WaitReady error length = %d, want bounded diagnostics", len(err.Error()))
			}
		})
	}
}

func TestRecoverablePodFailureRequiresGracePeriod(t *testing.T) {
	now := time.Unix(1_000, 0)
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "module-pod", Namespace: "tenant"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "module",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason:  "ImagePullBackOff",
				Message: "temporary registry failure",
			}},
		}}},
	}
	scheduler := &Scheduler{
		recoverableFailureGracePeriod: time.Minute,
		now:                           func() time.Time { return now },
	}
	firstSeen := map[string]time.Time{}

	if err := scheduler.podBlockingFailure([]corev1.Pod{pod}, firstSeen); err != nil {
		t.Fatalf("first recoverable observation returned error: %v", err)
	}
	now = now.Add(59 * time.Second)
	if err := scheduler.podBlockingFailure([]corev1.Pod{pod}, firstSeen); err != nil {
		t.Fatalf("failure returned before grace elapsed: %v", err)
	}
	now = now.Add(time.Second)
	if err := scheduler.podBlockingFailure([]corev1.Pod{pod}, firstSeen); err == nil || !strings.Contains(err.Error(), "persisted for at least 1m0s") {
		t.Fatalf("failure after grace = %v, want persisted ImagePullBackOff error", err)
	}
}

func TestWaitReadyReturnsRunningButUnreadyAfterGracePeriod(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	startedAt := metav1.NewTime(time.Now().Add(-time.Minute))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-unready", Namespace: namespace, Labels: deployment.Spec.Selector.MatchLabels},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			StartTime: &startedAt,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodReady,
				Status:  corev1.ConditionFalse,
				Reason:  "ContainersNotReady",
				Message: "Readiness probe failed: gRPC health status UNKNOWN",
			}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "module",
				Ready: false,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: startedAt}},
			}},
		},
	}
	client := fake.NewSimpleClientset(deployment, pod)
	scheduler, _ := New(client)
	scheduler.unreadyGracePeriod = time.Second

	err := scheduler.WaitReady(context.Background(), version, controlplane.RuntimeDeployment{})
	for _, want := range []string{"running but unready", "readiness probe", "Ready=False", "ContainersNotReady", "UNKNOWN"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("WaitReady error = %v, want %q", err, want)
		}
	}
}

func TestWaitReadyContextCancellationIncludesLatestStatus(t *testing.T) {
	version := testVersion()
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	deployment := BuildDeployment(version, namespace, name)
	deployment.Status.ReadyReplicas = 0
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 0
	deployment.Status.UnavailableReplicas = 1
	startedAt := metav1.NewTime(time.Now())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-pending", Namespace: namespace, Labels: deployment.Spec.Selector.MatchLabels},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			StartTime: &startedAt,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodReady,
				Status:  corev1.ConditionFalse,
				Reason:  "ContainersNotReady",
				Message: "Readiness probe failed: gRPC health status UNKNOWN",
			}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "module",
				Ready: false,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: startedAt}},
			}},
		},
	}
	client := fake.NewSimpleClientset(deployment, pod)
	scheduler, _ := New(client)
	scheduler.pollInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := scheduler.WaitReady(ctx, version, controlplane.RuntimeDeployment{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitReady error = %v, want context deadline exceeded", err)
	}
	for _, want := range []string{"ready=0", "updated=1", "unavailable=1", pod.Name, "RunningNotReady", "Ready=False", "ContainersNotReady", "UNKNOWN"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("WaitReady error = %v, want %q", err, want)
		}
	}
}
