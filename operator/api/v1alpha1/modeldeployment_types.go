package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GPUClass identifies the type of GPU node pool to schedule the model on.
// +kubebuilder:validation:Enum=l4;a100-40g;a100-80g
type GPUClass string

const (
	GPUClassL4      GPUClass = "l4"
	GPUClassA10040G GPUClass = "a100-40g"
	GPUClassA10080G GPUClass = "a100-80g"
)

// DeploymentPhase is the current lifecycle phase of a ModelDeployment.
// +kubebuilder:validation:Enum=Pending;Pulling;Verifying;Running;ScaledToZero;BudgetPaused;Failed
type DeploymentPhase string

const (
	DeploymentPhasePending      DeploymentPhase = "Pending"
	DeploymentPhasePulling      DeploymentPhase = "Pulling"
	DeploymentPhaseVerifying    DeploymentPhase = "Verifying"
	DeploymentPhaseRunning      DeploymentPhase = "Running"
	DeploymentPhaseScaledToZero DeploymentPhase = "ScaledToZero"
	DeploymentPhaseBudgetPaused DeploymentPhase = "BudgetPaused"
	DeploymentPhaseFailed       DeploymentPhase = "Failed"
)

// LoRAAdapterSpec defines a LoRA fine-tuning adapter to hot-load into vLLM.
type LoRAAdapterSpec struct {
	// Name is the identifier used when calling the adapter (passed as model= in API requests).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9\-]*[a-z0-9]$`
	Name string `json:"name"`

	// Path is the GCS path or local path inside the model cache volume to the adapter weights.
	// Example: "gs://my-bucket/lora/my-adapter" or "/adapters/my-adapter"
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// BaseModel is the catalog model ID this adapter was trained on.
	// Must match the deployment's ModelRef.
	// +kubebuilder:validation:MinLength=1
	BaseModel string `json:"baseModel"`
}

// EngineType identifies the inference engine to use for this deployment.
// +kubebuilder:validation:Enum=vllm;sglang
type EngineType string

const (
	EngineTypeVLLM   EngineType = "vllm"
	EngineTypeSGLang EngineType = "sglang"
)

// ModelDeploymentSpec defines the desired state of a deployed model.
type ModelDeploymentSpec struct {
	// ModelRef is the catalog model ID (e.g. "llama-3-8b-instruct").
	// Must match an entry in the openserve catalog registry.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9\-\.]*[a-z0-9]$`
	ModelRef string `json:"modelRef"`

	// GPUClass selects the GPU node pool for this deployment.
	// +kubebuilder:default=l4
	GPUClass GPUClass `json:"gpuClass"`

	// ScaleToZero enables automatic scale-down to 0 replicas after IdleTimeoutMin minutes
	// of inactivity. The first request after idle incurs a cold-start delay.
	// +kubebuilder:default=true
	ScaleToZero bool `json:"scaleToZero"`

	// IdleTimeoutMin is the number of minutes of inactivity before ScaleToZero triggers.
	// Ignored when ScaleToZero is false.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1440
	IdleTimeoutMin int32 `json:"idleTimeoutMin"`

	// MinReplicas is the minimum replica count when the deployment is active.
	// Set to 1 for always-on (disables scale-to-zero regardless of the ScaleToZero field).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas is the maximum replica count for horizontal scaling.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	MaxReplicas int32 `json:"maxReplicas"`

	// Budget defines daily spend guardrails for this deployment.
	// +kubebuilder:default={"dailyUsdCap": "50"}
	Budget BudgetSpec `json:"budget"`

	// Limits defines per-request token caps enforced at the gateway.
	// +kubebuilder:default={"maxInputTokens": 8192, "maxOutputTokens": 4096}
	Limits TokenLimits `json:"limits"`

	// VLLMArgs are additional CLI arguments to pass verbatim to the vLLM server.
	// The catalog manifest sets recommended defaults; use this for overrides.
	// Common overrides: --tensor-parallel-size, --quantization, --max-model-len.
	// +optional
	VLLMArgs []string `json:"vllmArgs,omitempty"`

	// Engine selects the inference engine. Defaults to vllm.
	// sglang is recommended for structured generation and long-context workloads.
	// +kubebuilder:default=vllm
	// +optional
	Engine EngineType `json:"engine,omitempty"`

	// ModelCacheBucket is the GCS bucket where weight files are cached.
	// Defaults to the install-wide bucket configured at Helm install time.
	// +optional
	ModelCacheBucket string `json:"modelCacheBucket,omitempty"`

	// Description is a human-readable note shown in the GUI.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	Description string `json:"description,omitempty"`

	// LoRAAdapters is a list of LoRA fine-tuning adapters to hot-load after the model is Running.
	// Adapters are loaded via vLLM's /v1/load_lora_adapter API after deployment reaches Running phase.
	// To use an adapter, set model=<adapter.name> in API requests.
	// +optional
	LoRAAdapters []LoRAAdapterSpec `json:"loraAdapters,omitempty"`
}

// BudgetSpec defines spend guardrails for a deployment.
type BudgetSpec struct {
	// DailyUsdCap is the maximum USD spend per calendar day (UTC).
	// When exceeded, the operator scales replicas to 0 and sets phase=BudgetPaused.
	// Value is a Kubernetes resource.Quantity string, e.g. "50", "100.50".
	// +kubebuilder:default="50"
	DailyUsdCap resource.Quantity `json:"dailyUsdCap"`

	// AlertThresholdPercent triggers an alert (but not a pause) at this fraction of DailyUsdCap.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=100
	AlertThresholdPercent int32 `json:"alertThresholdPercent,omitempty"`
}

// TokenLimits defines per-request token caps enforced at the gateway layer.
// Requests exceeding either limit receive a 400 error before reaching vLLM.
type TokenLimits struct {
	// MaxInputTokens is the hard cap on prompt length (tokens).
	// +kubebuilder:default=8192
	// +kubebuilder:validation:Minimum=64
	// +kubebuilder:validation:Maximum=131072
	MaxInputTokens int32 `json:"maxInputTokens"`

	// MaxOutputTokens is the hard cap on generated output length (tokens).
	// +kubebuilder:default=4096
	// +kubebuilder:validation:Minimum=64
	// +kubebuilder:validation:Maximum=65536
	MaxOutputTokens int32 `json:"maxOutputTokens"`
}

// ModelDeploymentStatus is the observed state of a ModelDeployment.
type ModelDeploymentStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase DeploymentPhase `json:"phase,omitempty"`

	// ReadyReplicas is the number of vLLM replicas currently ready.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Endpoint is the HTTPS URL of the OpenAI-compatible inference endpoint.
	// Empty until phase=Running.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// ModelVersion is the catalog manifest version that was resolved and deployed.
	// +optional
	ModelVersion string `json:"modelVersion,omitempty"`

	// WeightDigest is the SHA256 digest of the resolved model weights as verified
	// against the catalog manifest. Used to detect cache corruption.
	// +optional
	WeightDigest string `json:"weightDigest,omitempty"`

	// TodayUsdSpend is the current-day spend in USD, updated every minute by the
	// budget controller reading from BigQuery.
	// +optional
	TodayUsdSpend string `json:"todayUsdSpend,omitempty"`

	// BudgetPausedAt is the timestamp when a budget cap triggered a scale-to-zero.
	// Cleared when an admin manually resumes the deployment.
	// +optional
	BudgetPausedAt *metav1.Time `json:"budgetPausedAt,omitempty"`

	// LastReconcile is the timestamp of the most recent successful reconciliation.
	// +optional
	LastReconcile *metav1.Time `json:"lastReconcile,omitempty"`

	// Conditions lists the current status conditions for detailed debugging.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LoadedLoRAAdapters lists the names of LoRA adapters currently loaded in vLLM.
	// Updated by the operator after successful load_lora_adapter calls.
	// +optional
	LoadedLoRAAdapters []string `json:"loadedLoRAAdapters,omitempty"`
}

// Condition types for ModelDeployment.
const (
	// ConditionWeightsVerified is true when the operator has verified model weight
	// integrity against the catalog manifest signature.
	ConditionWeightsVerified = "WeightsVerified"

	// ConditionDeploymentReady is true when at least one vLLM replica is ready.
	ConditionDeploymentReady = "DeploymentReady"

	// ConditionBudgetOK is true when today's spend is below the DailyUsdCap.
	ConditionBudgetOK = "BudgetOK"

	// ConditionEndpointRoutable is true when the gateway has a valid route to this deployment.
	ConditionEndpointRoutable = "EndpointRoutable"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=md,scope=Namespaced,categories=openserve
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelRef`
// +kubebuilder:printcolumn:name="GPU",type=string,JSONPath=`.spec.gpuClass`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Spend",type=string,JSONPath=`.status.todayUsdSpend`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// +kubebuilder:object:root=true

// ModelDeployment represents a deployed open-source LLM model in the customer's cluster.
// The operator reconciles each ModelDeployment into a vLLM Deployment, Service, HPA,
// KEDA ScaledObject, and gateway route.
type ModelDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelDeploymentSpec   `json:"spec,omitempty"`
	Status ModelDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// ModelDeploymentList contains a list of ModelDeployment.
type ModelDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelDeployment{}, &ModelDeploymentList{})
}
