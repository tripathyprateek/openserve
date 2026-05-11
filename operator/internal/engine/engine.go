// Package engine defines the InferenceEngine interface and built-in implementations.
package engine

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// InferenceEngine abstracts the inference backend (vLLM, SGLang, etc.).
// Each implementation returns the container spec for a given model and GPU class.
type InferenceEngine interface {
	// Name returns the engine identifier (e.g. "vllm", "sglang").
	Name() string
	// ContainerImage returns the pinned container image for this engine.
	ContainerImage() string
	// Args returns the full CLI argument list for the given model and extra user args.
	Args(modelRef string, catalogArgs []string, extraArgs []string) []string
	// Port returns the port the engine listens on.
	Port() int32
	// ReadinessPath returns the HTTP path used for readiness probes.
	ReadinessPath() string
	// GPUResourceRequirements returns the resource.Quantity for the GPU class.
	GPUResources(gpuClass string) corev1.ResourceList
	// MetricName returns the Prometheus metric name used for KEDA scale-to-zero.
	MetricName() string
}
