package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openservev1alpha1 "github.com/openserve/openserve/operator/api/v1alpha1"
	"github.com/openserve/openserve/operator/internal/catalog"
	"github.com/openserve/openserve/operator/internal/engine"
	"github.com/openserve/openserve/operator/internal/gateway"
	"github.com/openserve/openserve/operator/internal/scaling"
)

// Config is shared configuration passed to all controllers at startup.
type Config struct {
	CatalogURL       string
	ModelCacheBucket string
	GatewayDomain    string
	BigQueryDataset  string
}

// gpuNodeSelector maps a GPUClass to the GKE node pool label selector.
var gpuNodeSelector = map[openservev1alpha1.GPUClass]map[string]string{
	openservev1alpha1.GPUClassL4:      {"cloud.google.com/gke-accelerator": "nvidia-l4"},
	openservev1alpha1.GPUClassA10040G: {"cloud.google.com/gke-accelerator": "nvidia-tesla-a100"},
	openservev1alpha1.GPUClassA10080G: {"cloud.google.com/gke-accelerator": "nvidia-a100-80gb"},
}

// gpuResourceName maps a GPUClass to the Kubernetes resource name for GPU limits.
var gpuResourceName = map[openservev1alpha1.GPUClass]corev1.ResourceName{
	openservev1alpha1.GPUClassL4:      "nvidia.com/gpu",
	openservev1alpha1.GPUClassA10040G: "nvidia.com/gpu",
	openservev1alpha1.GPUClassA10080G: "nvidia.com/gpu",
}

// ModelDeploymentReconciler reconciles ModelDeployment objects.
// It creates and owns: Deployment (vLLM), Service, and registers the endpoint
// with the gateway ConfigMap. HPA and KEDA ScaledObject are set up separately
// by the scaling sub-reconciler.
//
// +kubebuilder:rbac:groups=openserve.io,resources=modeldeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openserve.io,resources=modeldeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openserve.io,resources=modeldeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
type ModelDeploymentReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Config            Config
	CatalogClient     *catalog.Client
	ScalingReconciler *scaling.Reconciler
	GatewaySyncer     *gateway.Syncer
}

func (r *ModelDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var md openservev1alpha1.ModelDeployment
	if err := r.Get(ctx, req.NamespacedName, &md); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve the inference engine
	eng, err := engine.ForType(string(md.Spec.Engine))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unknown engine: %w", err)
	}

	// Handle deletion via finalizer.
	const finalizer = "openserve.io/cleanup"
	if md.DeletionTimestamp != nil {
		if containsString(md.Finalizers, finalizer) {
			if err := r.cleanup(ctx, &md); err != nil {
				return ctrl.Result{}, err
			}
			md.Finalizers = removeString(md.Finalizers, finalizer)
			return ctrl.Result{}, r.Update(ctx, &md)
		}
		return ctrl.Result{}, nil
	}

	if !containsString(md.Finalizers, finalizer) {
		md.Finalizers = append(md.Finalizers, finalizer)
		if err := r.Update(ctx, &md); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("reconciling ModelDeployment", "model", md.Spec.ModelRef, "gpu", md.Spec.GPUClass, "engine", eng.Name())

	// Step 1: Resolve model from catalog
	model, err := r.CatalogClient.GetModel(ctx, md.Spec.ModelRef)
	if err != nil {
		r.setCondition(&md, openservev1alpha1.ConditionWeightsVerified, metav1.ConditionFalse, "ModelNotInCatalog", err.Error())
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, r.Status().Update(ctx, &md)
	}

	// Step 2: Verify weights in GCS (only if not already verified for this model version)
	if !r.weightsAlreadyVerified(&md, model) {
		gcsPath := fmt.Sprintf("gs://%s/%s/%s/weights.tar", r.Config.ModelCacheBucket, md.Spec.ModelRef, model.HFRevision)
		if err := r.CatalogClient.VerifyWeights(ctx, model, gcsPath, log); err != nil {
			r.setCondition(&md, openservev1alpha1.ConditionWeightsVerified, metav1.ConditionFalse, "VerificationFailed", err.Error())
			return ctrl.Result{RequeueAfter: 2 * time.Minute}, r.Status().Update(ctx, &md)
		}
		r.setCondition(&md, openservev1alpha1.ConditionWeightsVerified, metav1.ConditionTrue, "Verified", "SHA256 matches catalog manifest")
		md.Status.WeightDigest = model.WeightDigestSha256
		md.Status.ModelVersion = model.HFRevision
	}

	if err := r.reconcileDeployment(ctx, &md, eng); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile deployment: %w", err)
	}

	if err := r.reconcileService(ctx, &md, eng); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile service: %w", err)
	}

	// Step 5: KEDA ScaledObject for scale-to-zero
	if md.Spec.ScaleToZero {
		if err := r.ScalingReconciler.Reconcile(ctx, scaling.ScaledObjectSpec{
			DeploymentName: deploymentName(md.Name),
			Namespace:      md.Namespace,
			MinReplicas:    0,
			MaxReplicas:    md.Spec.MaxReplicas,
			IdleTimeoutMin: md.Spec.IdleTimeoutMin,
			OwnerRef:       *metav1.NewControllerRef(&md, openservev1alpha1.GroupVersion.WithKind("ModelDeployment")),
			MetricName:     eng.MetricName(),
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconcile keda: %w", err)
		}
	}

	// Step 6: Gateway route sync
	if err := r.GatewaySyncer.AddRoute(ctx, md.Name); err != nil {
		log.Error(err, "failed to sync gateway route")
		// non-fatal: gateway will pick it up on next reconcile
	}
	r.setCondition(&md, openservev1alpha1.ConditionEndpointRoutable, metav1.ConditionTrue, "RouteRegistered", "")

	// Step 7: Load LoRA adapters if deployment is Running and adapters are specified
	if len(md.Spec.LoRAAdapters) > 0 {
		if err := r.reconcileLoRAAdapters(ctx, &md, eng); err != nil {
			log.Error(err, "failed to reconcile LoRA adapters")
			// non-fatal: will retry on next reconcile
		}
	}

	// Update status.
	endpoint := fmt.Sprintf("https://%s/inference/%s", r.Config.GatewayDomain, md.Name)
	md.Status.Endpoint = endpoint
	if err := r.Status().Update(ctx, &md); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ModelDeploymentReconciler) reconcileDeployment(ctx context.Context, md *openservev1alpha1.ModelDeployment, eng engine.InferenceEngine) error {
	labels := labelsForDeployment(md.Name)
	nodeSelector := gpuNodeSelector[md.Spec.GPUClass]
	gpuResource := gpuResourceName[md.Spec.GPUClass]
	port := eng.Port()
	readinessPath := eng.ReadinessPath()

	replicas := int32(1)
	if md.Spec.ScaleToZero && md.Spec.MinReplicas == 0 {
		// KEDA will manage replicas; we set the initial value only.
		replicas = 0
	}

	// Build model path argument for the engine
	modelPath := "/model-cache/" + md.Spec.ModelRef

	// Get catalog args and engine args
	// Note: for vLLM, catalog args contain things like --max-num-seqs
	// For SGLang, catalog args may be different; see catalog implementation
	catalogArgs := []string{}
	if md.Spec.ModelRef != "" {
		// Placeholder: in real code, fetch from catalog. For now, use vLLM defaults.
		if eng.Name() == "vllm" {
			catalogArgs = []string{"--max-num-seqs", "256"}
		}
	}

	args := eng.Args(modelPath, catalogArgs, md.Spec.VLLMArgs)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(md.Name),
			Namespace: md.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Tolerations: []corev1.Toleration{{
						Key:      "nvidia.com/gpu",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
						RunAsUser:    int64Ptr(1000),
					},
					Containers: []corev1.Container{{
						Name:  eng.Name(),
						Image: eng.ContainerImage(),
						Args:  args,
						Ports: []corev1.ContainerPort{{ContainerPort: port, Protocol: corev1.ProtocolTCP}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: boolPtr(false),
							ReadOnlyRootFilesystem:   boolPtr(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{gpuResource: resource.MustParse("1")},
							Requests: corev1.ResourceList{gpuResource: resource.MustParse("1")},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "model-cache", MountPath: "/model-cache", ReadOnly: true},
							{Name: "tmp", MountPath: "/tmp"},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: readinessPath, Port: intstr.FromInt32(port)},
							},
							InitialDelaySeconds: 120,
							PeriodSeconds:       30,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: readinessPath, Port: intstr.FromInt32(port)},
							},
							InitialDelaySeconds: 30,
							PeriodSeconds:       10,
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "model-cache",
							VolumeSource: corev1.VolumeSource{
								// GCS FUSE CSI driver mounts the model cache bucket.
								// Requires the GKE GCS CSI driver to be enabled on the cluster.
								CSI: &corev1.CSIVolumeSource{
									Driver: "gcsfuse.csi.storage.gke.io",
									VolumeAttributes: map[string]string{
										"bucketName":   r.Config.ModelCacheBucket,
										"mountOptions": "implicit-dirs,file-cache:enable-o-direct:true",
									},
								},
							},
						},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(md, deploy, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(deploy), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, deploy)
	}
	if err != nil {
		return err
	}

	// Update spec while preserving replica count managed by KEDA.
	existing.Spec.Template = deploy.Spec.Template
	return r.Update(ctx, existing)
}

func (r *ModelDeploymentReconciler) reconcileService(ctx context.Context, md *openservev1alpha1.ModelDeployment, eng engine.InferenceEngine) error {
	labels := labelsForDeployment(md.Name)
	port := eng.Port()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      md.Name,
			Namespace: md.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				Protocol:   corev1.ProtocolTCP,
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if err := ctrl.SetControllerReference(md, svc, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(svc), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, svc)
	}
	return err
}

func (r *ModelDeploymentReconciler) cleanup(ctx context.Context, md *openservev1alpha1.ModelDeployment) error {
	// Owned resources (Deployment, Service) are garbage-collected by controller-runtime
	// via owner references. We only need to clean up non-owned resources here, e.g.
	// removing the gateway route entry from the shared ConfigMap.
	if err := r.GatewaySyncer.RemoveRoute(ctx, md.Name); err != nil {
		return fmt.Errorf("failed to remove gateway route: %w", err)
	}
	return nil
}

func (r *ModelDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openservev1alpha1.ModelDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

// reconcileLoRAAdapters ensures all specified LoRA adapters are loaded in vLLM.
// It calls vLLM's /v1/load_lora_adapter API for any adapter not yet in status.LoadedLoRAAdapters.
// Note: LoRA adapters are currently only supported by vLLM, not SGLang.
func (r *ModelDeploymentReconciler) reconcileLoRAAdapters(ctx context.Context, md *openservev1alpha1.ModelDeployment, eng engine.InferenceEngine) error {
	log := log.FromContext(ctx)

	// Only attempt if deployment is Running
	if md.Status.Phase != openservev1alpha1.DeploymentPhaseRunning {
		return nil
	}

	// Only vLLM supports LoRA adapters for now
	if eng.Name() != "vllm" {
		log.Info("skipping LoRA adapter reconciliation for non-vLLM engine", "engine", eng.Name())
		return nil
	}

	// Build set of already-loaded adapters
	loaded := make(map[string]bool)
	for _, name := range md.Status.LoadedLoRAAdapters {
		loaded[name] = true
	}

	// Determine service address using engine port
	svcAddr := fmt.Sprintf("%s-%s.openserve-inference.svc.cluster.local:%d", eng.Name(), md.Name, eng.Port())

	var newlyLoaded []string
	for _, adapter := range md.Spec.LoRAAdapters {
		if loaded[adapter.Name] {
			continue
		}

		body, _ := json.Marshal(map[string]string{
			"lora_name": adapter.Name,
			"lora_path": adapter.Path,
		})

		url := fmt.Sprintf("http://%s/v1/load_lora_adapter", svcAddr)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create lora load request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Error(err, "failed to call load_lora_adapter", "adapter", adapter.Name)
			continue // try others; will retry on next reconcile
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			newlyLoaded = append(newlyLoaded, adapter.Name)
			log.Info("loaded LoRA adapter", "adapter", adapter.Name, "deployment", md.Name)
		} else {
			log.Error(fmt.Errorf("status %d", resp.StatusCode), "load_lora_adapter failed", "adapter", adapter.Name)
		}
	}

	if len(newlyLoaded) > 0 {
		md.Status.LoadedLoRAAdapters = append(md.Status.LoadedLoRAAdapters, newlyLoaded...)
		return r.Status().Update(ctx, md)
	}
	return nil
}

// helpers

func deploymentName(mdName string) string { return "vllm-" + mdName }
func labelsForDeployment(mdName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "vllm",
		"app.kubernetes.io/instance":   mdName,
		"app.kubernetes.io/managed-by": "openserve-operator",
		"openserve.io/model-deployment": mdName,
	}
}
func boolPtr(b bool) *bool   { return &b }
func int64Ptr(i int64) *int64 { return &i }
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
func removeString(slice []string, s string) []string {
	out := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func (r *ModelDeploymentReconciler) weightsAlreadyVerified(md *openservev1alpha1.ModelDeployment, model *catalog.Model) bool {
	return md.Status.WeightDigest == model.WeightDigestSha256
}

func (r *ModelDeploymentReconciler) setCondition(md *openservev1alpha1.ModelDeployment, condType string, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&md.Status.Conditions, metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
}
