// Package kubernetes implements the only module orchestrator supported by the
// first Ruleshift external-module release.
package kubernetes

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const modulePort int32 = 50051

const maxReadinessDiagnosticBytes = 1024

type Scheduler struct {
	client                        kubernetes.Interface
	pollInterval                  time.Duration
	unreadyGracePeriod            time.Duration
	recoverableFailureGracePeriod time.Duration
	now                           func() time.Time
}

func New(client kubernetes.Interface) (*Scheduler, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	return &Scheduler{
		client:                        client,
		pollInterval:                  250 * time.Millisecond,
		unreadyGracePeriod:            30 * time.Second,
		recoverableFailureGracePeriod: 30 * time.Second,
		now:                           time.Now,
	}, nil
}

func TenantNamespace(developerID string) string {
	sum := sha256.Sum256([]byte(developerID))
	return "ruleshift-tenant-" + hex.EncodeToString(sum[:8])
}

func WorkloadName(version controlplane.Version) string {
	raw := version.Ref.ModuleID + "\x00" + version.Ref.Version + "\x00" + version.Ref.ImageDigest
	sum := sha256.Sum256([]byte(raw))
	return "module-" + hex.EncodeToString(sum[:10])
}

func (s *Scheduler) EnsureTenant(ctx context.Context, developerID string) error {
	namespace := TenantNamespace(developerID)
	labels := map[string]string{"ruleshift.io/tenant": namespace}
	if err := createNamespace(ctx, s.client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: labels}}); err != nil {
		return err
	}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "ruleshift-module-quota", Namespace: namespace}, Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("4"), corev1.ResourceLimitsMemory: resource.MustParse("2Gi"), corev1.ResourcePods: resource.MustParse("20")}}}
	if _, err := s.client.CoreV1().ResourceQuotas(namespace).Create(ctx, quota, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	limits := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "ruleshift-module-limits", Namespace: namespace}, Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{Type: corev1.LimitTypeContainer, Default: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("256Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("64Mi")}, DefaultRequest: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")}}}}}
	if err := s.reconcileLimitRange(ctx, limits); err != nil {
		return fmt.Errorf("reconcile tenant limit range: %w", err)
	}
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "default-deny", Namespace: namespace}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}}}
	if _, err := s.client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	ingress := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "allow-ruleshift-core", Namespace: namespace}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/component": "module"}}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/core": "true"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: ptr.To(corev1.ProtocolTCP), Port: ptr.To(intstr.FromInt32(modulePort))}}}}}}
	if _, err := s.client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, ingress, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (s *Scheduler) PutRegistryCredential(ctx context.Context, developerID, name, server, username, password string) error {
	if name == "" || server == "" || username == "" || password == "" {
		return fmt.Errorf("credential name, registry server, username and password/token are required")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid credential name")
	}
	if err := s.EnsureTenant(ctx, developerID); err != nil {
		return err
	}
	namespace := TenantNamespace(developerID)
	secretName := credentialSecretName(name)
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	config := map[string]any{"auths": map[string]any{server: map[string]string{"username": username, "password": password, "auth": auth}}}
	payload, err := json.Marshal(config)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace, Labels: map[string]string{"ruleshift.io/managed": "true"}}, Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: payload}}
	existing, getErr := s.client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		_, err = s.client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		return err
	}
	if getErr != nil {
		return getErr
	}
	secret.ResourceVersion = existing.ResourceVersion
	_, err = s.client.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func (s *Scheduler) Deploy(ctx context.Context, version controlplane.Version) (controlplane.RuntimeDeployment, error) {
	if err := s.EnsureTenant(ctx, version.Ref.DeveloperID); err != nil {
		return controlplane.RuntimeDeployment{}, err
	}
	namespace := TenantNamespace(version.Ref.DeveloperID)
	name := WorkloadName(version)
	token, err := randomToken()
	if err != nil {
		return controlplane.RuntimeDeployment{}, err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-rpc", Namespace: namespace}, Type: corev1.SecretTypeOpaque, StringData: map[string]string{"token": token}}
	if _, err = s.client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); apierrors.IsAlreadyExists(err) {
		existing, getErr := s.client.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if getErr != nil {
			return controlplane.RuntimeDeployment{}, getErr
		}
		token = string(existing.Data["token"])
		if token == "" {
			token = existing.StringData["token"]
		}
	} else if err != nil {
		return controlplane.RuntimeDeployment{}, err
	}
	deployment := BuildDeployment(version, namespace, name)
	if _, err = s.client.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return controlplane.RuntimeDeployment{}, err
	}
	service := BuildService(namespace, name)
	if _, err = s.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return controlplane.RuntimeDeployment{}, err
	}
	return controlplane.RuntimeDeployment{Endpoint: fmt.Sprintf("%s.%s.svc:%d", name, namespace, modulePort), RPCToken: token}, nil
}

func (s *Scheduler) WaitReady(ctx context.Context, version controlplane.Version, _ controlplane.RuntimeDeployment) error {
	name := WorkloadName(version)
	namespace := TenantNamespace(version.Ref.DeveloperID)
	lastStatus := fmt.Sprintf("deployment %s/%s has not reported status", namespace, name)
	recoverableFailures := make(map[string]time.Time)
	err := wait.PollUntilContextCancel(ctx, s.pollInterval, true, func(ctx context.Context) (bool, error) {
		deployment, err := s.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		lastStatus = deploymentReadinessStatus(deployment, nil, nil)
		// MVP validates that the module workload has become callable. It does not
		// enforce a replica-count policy; HA requirements are deferred until later.
		if deployment.Status.ReadyReplicas > 0 && deployment.Status.UpdatedReplicas > 0 {
			return true, nil
		}
		if err = deploymentReplicaFailure(deployment); err != nil {
			return false, err
		}
		selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		if err != nil {
			return false, fmt.Errorf("select deployment pods: %w", err)
		}
		pods, listErr := s.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
		if listErr != nil {
			lastStatus = deploymentReadinessStatus(deployment, nil, listErr)
			if !apierrors.IsForbidden(listErr) {
				return false, fmt.Errorf("list deployment pods: %w", listErr)
			}
		} else {
			lastStatus = deploymentReadinessStatus(deployment, pods.Items, nil)
			if err = s.podBlockingFailure(pods.Items, recoverableFailures); err != nil {
				return false, err
			}
		}
		return false, nil
	})
	if ctx.Err() != nil {
		return readinessContextError(ctx.Err(), lastStatus)
	}
	return err
}

func (s *Scheduler) reconcileLimitRange(ctx context.Context, desired *corev1.LimitRange) error {
	limitRanges := s.client.CoreV1().LimitRanges(desired.Namespace)
	existing, err := limitRanges.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = limitRanges.Create(ctx, desired, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return nil
	}
	updated := existing.DeepCopy()
	updated.Spec = desired.Spec
	_, err = limitRanges.Update(ctx, updated, metav1.UpdateOptions{})
	return err
}

func deploymentReplicaFailure(deployment *appsv1.Deployment) error {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return fmt.Errorf("deployment %s/%s replica failure (%s): %s", deployment.Namespace, deployment.Name, condition.Reason, boundedDiagnostic(condition.Message))
		}
	}
	return nil
}

type recoverablePodFailure struct {
	key string
	err error
}

func (s *Scheduler) podBlockingFailure(pods []corev1.Pod, firstSeen map[string]time.Time) error {
	items := slices.Clone(pods)
	slices.SortFunc(items, func(a, b corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })
	now := s.now()
	activeRecoverableFailures := make(map[string]struct{})
	recoverableFailures := make([]recoverablePodFailure, 0)
	for i := range items {
		pod := &items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == corev1.PodReasonUnschedulable {
				recoverableFailures = append(recoverableFailures, recoverablePodFailure{
					key: "pod/" + pod.Namespace + "/" + pod.Name + "/scheduling",
					err: fmt.Errorf("pod %s/%s is unschedulable: %s", pod.Namespace, pod.Name, boundedDiagnostic(condition.Message)),
				})
			}
		}
		for _, status := range pod.Status.InitContainerStatuses {
			failure, terminal := containerBlockingFailure(pod, status)
			if terminal {
				return failure
			}
			if failure != nil {
				recoverableFailures = append(recoverableFailures, recoverablePodFailure{key: containerFailureKey(pod, status), err: failure})
			}
		}
		for _, status := range pod.Status.ContainerStatuses {
			failure, terminal := containerBlockingFailure(pod, status)
			if terminal {
				return failure
			}
			if failure != nil {
				recoverableFailures = append(recoverableFailures, recoverablePodFailure{key: containerFailureKey(pod, status), err: failure})
			}
			if !status.Ready && status.State.Running != nil && s.unreadyGracePeriod > 0 {
				startedAt := status.State.Running.StartedAt.Time
				if startedAt.IsZero() && pod.Status.StartTime != nil {
					startedAt = pod.Status.StartTime.Time
				}
				if !startedAt.IsZero() && now.Sub(startedAt) >= s.unreadyGracePeriod {
					return fmt.Errorf("pod %s/%s container %s has been running but unready for at least %s; readiness probe has not succeeded%s", pod.Namespace, pod.Name, status.Name, s.unreadyGracePeriod, podReadyConditionDetail(pod))
				}
			}
		}
	}
	for _, failure := range recoverableFailures {
		activeRecoverableFailures[failure.key] = struct{}{}
		startedAt, ok := firstSeen[failure.key]
		if !ok {
			if s.recoverableFailureGracePeriod <= 0 {
				return failure.err
			}
			firstSeen[failure.key] = now
			continue
		}
		if now.Sub(startedAt) >= s.recoverableFailureGracePeriod {
			return fmt.Errorf("%w (persisted for at least %s)", failure.err, s.recoverableFailureGracePeriod)
		}
	}
	for key := range firstSeen {
		if _, ok := activeRecoverableFailures[key]; !ok {
			delete(firstSeen, key)
		}
	}
	return nil
}

func containerFailureKey(pod *corev1.Pod, status corev1.ContainerStatus) string {
	return "pod/" + pod.Namespace + "/" + pod.Name + "/container/" + status.Name
}

func containerBlockingFailure(pod *corev1.Pod, status corev1.ContainerStatus) (error, bool) {
	waiting := status.State.Waiting
	if waiting == nil {
		return nil, false
	}
	err := fmt.Errorf("pod %s/%s container %s is blocked (%s): %s", pod.Namespace, pod.Name, status.Name, waiting.Reason, boundedDiagnostic(waiting.Message))
	switch waiting.Reason {
	case "CreateContainerConfigError", "InvalidImageName", "ErrImageNeverPull":
		return err, true
	case "ErrImagePull", "ImagePullBackOff", "CreateContainerError", "RunContainerError", "CrashLoopBackOff":
		return err, false
	default:
		return nil, false
	}
}

func deploymentReadinessStatus(deployment *appsv1.Deployment, pods []corev1.Pod, podListErr error) string {
	parts := []string{fmt.Sprintf("deployment %s/%s ready=%d updated=%d available=%d unavailable=%d", deployment.Namespace, deployment.Name, deployment.Status.ReadyReplicas, deployment.Status.UpdatedReplicas, deployment.Status.AvailableReplicas, deployment.Status.UnavailableReplicas)}
	if podListErr != nil {
		parts = append(parts, "pod diagnostics unavailable: "+boundedDiagnostic(podListErr.Error()))
	} else {
		items := slices.Clone(pods)
		slices.SortFunc(items, func(a, b corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })
		const maxPods = 4
		for i := 0; i < len(items) && i < maxPods; i++ {
			parts = append(parts, fmt.Sprintf("pod %s=%s%s%s", items[i].Name, items[i].Status.Phase, podContainerReadiness(&items[i]), podFalseConditionDetail(&items[i])))
		}
		if len(items) > maxPods {
			parts = append(parts, fmt.Sprintf("%d more pods", len(items)-maxPods))
		}
	}
	return boundedDiagnostic(strings.Join(parts, "; "))
}

func podContainerReadiness(pod *corev1.Pod) string {
	for _, status := range pod.Status.InitContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			return "/" + status.State.Waiting.Reason
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			return "/" + status.State.Waiting.Reason
		}
		if status.State.Running != nil && !status.Ready {
			return "/RunningNotReady(" + status.Name + ")"
		}
	}
	return ""
}

func podReadyConditionDetail(pod *corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionFalse {
			return "; pod Ready=False (" + condition.Reason + "): " + boundedDiagnostic(condition.Message)
		}
	}
	return ""
}

func podFalseConditionDetail(pod *corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Status == corev1.ConditionFalse {
			return fmt.Sprintf("/%s=False(%s: %s)", condition.Type, condition.Reason, boundedDiagnostic(condition.Message))
		}
	}
	return ""
}

func readinessContextError(ctxErr error, status string) error {
	return fmt.Errorf("%w: %s", ctxErr, boundedDiagnostic(status))
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxReadinessDiagnosticBytes {
		return value
	}
	return value[:maxReadinessDiagnosticBytes-3] + "..."
}

func (s *Scheduler) Cleanup(ctx context.Context, version controlplane.Version, pinnedRooms int) error {
	if pinnedRooms > 0 || version.Status == controlplane.StatusActive {
		return nil
	}
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	if err := s.client.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationBackground)}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := s.client.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := s.client.CoreV1().Secrets(namespace).Delete(ctx, name+"-rpc", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// ResolveDeployment lets the core reconnect to an already validated version
// after a gateway restart. The token is read from Kubernetes, never PostgreSQL.
func (s *Scheduler) ResolveDeployment(ctx context.Context, ref module.ModuleRef) (controlplane.RuntimeDeployment, error) {
	version := controlplane.Version{Ref: ref}
	name := WorkloadName(version)
	namespace := TenantNamespace(ref.DeveloperID)
	secret, err := s.client.CoreV1().Secrets(namespace).Get(ctx, name+"-rpc", metav1.GetOptions{})
	if err != nil {
		return controlplane.RuntimeDeployment{}, err
	}
	token := string(secret.Data["token"])
	if token == "" {
		token = secret.StringData["token"]
	}
	if token == "" {
		return controlplane.RuntimeDeployment{}, fmt.Errorf("module RPC token is missing")
	}
	return controlplane.RuntimeDeployment{Endpoint: fmt.Sprintf("%s.%s.svc:%d", name, namespace, modulePort), RPCToken: token}, nil
}

func BuildDeployment(version controlplane.Version, namespace, name string) *appsv1.Deployment {
	labels := map[string]string{"ruleshift.io/component": "module", "ruleshift.io/workload": name, "ruleshift.io/module": stableLabel(version.Ref.ModuleID), "ruleshift.io/version": stableLabel(version.Ref.Version)}
	pullSecrets := []corev1.LocalObjectReference{}
	if version.CredentialName != "" {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: credentialSecretName(version.CredentialName)})
	}
	// Replicas is intentionally unset for the MVP, so Kubernetes applies its
	// default. TODO(post-MVP): define and enforce an explicit HA replica policy.
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/workload": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					ImagePullSecrets:             pullSecrets,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						RunAsUser:      ptr.To[int64](65532),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:            "module",
						Image:           version.ImageRef,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: modulePort}},
						Env: []corev1.EnvVar{{
							Name: "RULESHIFT_MODULE_RPC_TOKEN",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: name + "-rpc"},
								Key:                  "token",
							}},
						}},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("500m"),
								corev1.ResourceMemory:           resource.MustParse("256Mi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("64Mi"),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							RunAsNonRoot:             ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: modulePort}},
							InitialDelaySeconds: 1,
							PeriodSeconds:       2,
							TimeoutSeconds:      1,
							FailureThreshold:    3,
						},
					}},
				},
			},
		},
	}
}

func BuildService(namespace, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: map[string]string{"ruleshift.io/workload": name}, Ports: []corev1.ServicePort{{Name: "grpc", Port: modulePort, TargetPort: intstr.FromInt32(modulePort)}}}}
}
func credentialSecretName(name string) string { return "registry-" + stableLabel(name) }
func stableLabel(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
func createNamespace(ctx context.Context, client kubernetes.Interface, value *corev1.Namespace) error {
	_, err := client.CoreV1().Namespaces().Create(ctx, value, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
