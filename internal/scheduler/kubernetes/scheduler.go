// Package kubernetes implements the only module orchestrator supported by the
// first Ruleshift external-module release.
package kubernetes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const modulePort int32 = 50051

type Scheduler struct {
	client       kubernetes.Interface
	pollInterval time.Duration
	waitTimeout  time.Duration
}

func New(client kubernetes.Interface) (*Scheduler, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	return &Scheduler{client: client, pollInterval: 250 * time.Millisecond, waitTimeout: 2 * time.Minute}, nil
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
	if _, err := s.client.CoreV1().LimitRanges(namespace).Create(ctx, limits, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "default-deny", Namespace: namespace}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}}}
	if _, err := s.client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	ingress := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "allow-ruleshift-core", Namespace: namespace}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/component": "module"}}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/core": "true"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: protocolPtr(corev1.ProtocolTCP), Port: intstrPtr(intstr.FromInt32(modulePort))}}}}}}
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
	waitCtx, cancel := context.WithTimeout(ctx, s.waitTimeout)
	defer cancel()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		deployment, err := s.client.AppsV1().Deployments(namespace).Get(waitCtx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err = deploymentReplicaFailure(deployment); err != nil {
			return err
		}
		desiredReplicas := int32(1)
		if deployment.Spec.Replicas != nil {
			desiredReplicas = *deployment.Spec.Replicas
		}
		if deployment.Status.ReadyReplicas >= desiredReplicas && deployment.Status.UpdatedReplicas >= desiredReplicas {
			return nil
		}
		selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		if err != nil {
			return fmt.Errorf("select deployment replicasets: %w", err)
		}
		replicaSets, err := s.client.AppsV1().ReplicaSets(namespace).List(waitCtx, metav1.ListOptions{LabelSelector: selector.String()})
		if err != nil && !apierrors.IsForbidden(err) {
			return fmt.Errorf("list deployment replicasets: %w", err)
		}
		if err == nil {
			for i := range replicaSets.Items {
				if err = replicaSetFailure(&replicaSets.Items[i]); err != nil {
					return err
				}
			}
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("timed out after %s waiting for deployment %s/%s to become ready", s.waitTimeout, namespace, name)
		case <-ticker.C:
		}
	}
}

func deploymentReplicaFailure(deployment *appsv1.Deployment) error {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return fmt.Errorf("deployment %s/%s replica failure (%s): %s", deployment.Namespace, deployment.Name, condition.Reason, condition.Message)
		}
	}
	return nil
}

func replicaSetFailure(replicaSet *appsv1.ReplicaSet) error {
	for _, condition := range replicaSet.Status.Conditions {
		if condition.Type == appsv1.ReplicaSetReplicaFailure && condition.Status == corev1.ConditionTrue {
			return fmt.Errorf("replicaset %s/%s replica failure (%s): %s", replicaSet.Namespace, replicaSet.Name, condition.Reason, condition.Message)
		}
	}
	return nil
}

func (s *Scheduler) Cleanup(ctx context.Context, version controlplane.Version, pinnedRooms int) error {
	if pinnedRooms > 0 || version.Status == controlplane.StatusActive {
		return nil
	}
	namespace, name := TenantNamespace(version.Ref.DeveloperID), WorkloadName(version)
	policy := metav1.DeletePropagationBackground
	if err := s.client.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil && !apierrors.IsNotFound(err) {
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
	// Temporarily keep one module pod per version. On the current single-node
	// deployment a second replica consumes quota without providing node-level HA.
	replicas := int32(1)
	automount := false
	runAsNonRoot := true
	allowEscalation := false
	readOnly := true
	runAsUser := int64(65532)
	labels := map[string]string{"ruleshift.io/component": "module", "ruleshift.io/workload": name, "ruleshift.io/module": stableLabel(version.Ref.ModuleID), "ruleshift.io/version": stableLabel(version.Ref.Version)}
	pullSecrets := []corev1.LocalObjectReference{}
	if version.CredentialName != "" {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: credentialSecretName(version.CredentialName)})
	}
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"ruleshift.io/workload": name}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{AutomountServiceAccountToken: &automount, ImagePullSecrets: pullSecrets, SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &runAsNonRoot, RunAsUser: &runAsUser, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Containers: []corev1.Container{{Name: "module", Image: version.ImageRef, ImagePullPolicy: corev1.PullIfNotPresent, Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: modulePort}}, Env: []corev1.EnvVar{{Name: "RULESHIFT_MODULE_RPC_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name + "-rpc"}, Key: "token"}}}}, Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("256Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("64Mi")}, Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")}}, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &allowEscalation, ReadOnlyRootFilesystem: &readOnly, RunAsNonRoot: &runAsNonRoot, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: modulePort}}, InitialDelaySeconds: 1, PeriodSeconds: 2, TimeoutSeconds: 1, FailureThreshold: 3}}}}}}}
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
func protocolPtr(value corev1.Protocol) *corev1.Protocol     { return &value }
func intstrPtr(value intstr.IntOrString) *intstr.IntOrString { return &value }
func createNamespace(ctx context.Context, client kubernetes.Interface, value *corev1.Namespace) error {
	_, err := client.CoreV1().Namespaces().Create(ctx, value, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
