package scaling

import (
	"context"
	"fmt"

	kedav1alpha1 "sigs.k8s.io/keda/v2/apis/keda/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ScaledObjectSpec describes the KEDA ScaledObject to create for a ModelDeployment.
type ScaledObjectSpec struct {
	DeploymentName  string        // the vllm-<name> Deployment name
	Namespace       string
	MinReplicas     int32         // 0 for scale-to-zero
	MaxReplicas     int32
	IdleTimeoutMin  int32         // minutes before scaling to 0
	OwnerRef        metav1.OwnerReference
	MetricName      string        // Prometheus metric name (e.g. "vllm_num_requests_running", "sglang:num_running_reqs")
}

// Reconciler creates/updates/deletes KEDA ScaledObjects.
type Reconciler struct {
	client.Client
}

func NewReconciler(c client.Client) *Reconciler {
	return &Reconciler{Client: c}
}

// Reconcile creates or updates the ScaledObject for the given deployment.
// Uses a Prometheus scaler targeting the metric specified in spec.MetricName.
// ScaledObject name: "keda-<deploymentName>"
// Trigger: Prometheus metric with threshold=1
// When metric == 0 for IdleTimeoutMin, KEDA scales to 0.
func (r *Reconciler) Reconcile(ctx context.Context, spec ScaledObjectSpec) error {
	// Default to vllm metric if not specified (for backwards compatibility).
	metricName := spec.MetricName
	if metricName == "" {
		metricName = "vllm_num_requests_running"
	}

	scaledObj := &kedav1alpha1.ScaledObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "keda-" + spec.DeploymentName,
			Namespace:       spec.Namespace,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Spec: kedav1alpha1.ScaledObjectSpec{
			ScaleTargetRef: &kedav1alpha1.ScaleTarget{Name: spec.DeploymentName},
			MinReplicaCount: &spec.MinReplicas,
			MaxReplicaCount: &spec.MaxReplicas,
			CooldownPeriod:  int32Ptr(spec.IdleTimeoutMin * 60),
			Triggers: []kedav1alpha1.ScaleTriggers{{
				Type: "prometheus",
				Metadata: map[string]string{
					"serverAddress": "http://prometheus-operated.monitoring.svc.cluster.local:9090",
					"metricName":    metricName,
					"query":         fmt.Sprintf(`%s{deployment="%s"}`, metricName, spec.DeploymentName),
					"threshold":     "1",
				},
			}},
		},
	}

	existing := &kedav1alpha1.ScaledObject{}
	err := r.Get(ctx, client.ObjectKeyFromObject(scaledObj), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, scaledObj)
	}
	if err != nil {
		return err
	}

	// Update existing ScaledObject spec
	existing.Spec = scaledObj.Spec
	return r.Update(ctx, existing)
}

// Delete removes the ScaledObject for the given deployment name.
func (r *Reconciler) Delete(ctx context.Context, name, namespace string) error {
	scaledObj := &kedav1alpha1.ScaledObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "keda-" + name,
			Namespace: namespace,
		},
	}

	err := r.Client.Delete(ctx, scaledObj)
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func int32Ptr(i int32) *int32 {
	return &i
}
