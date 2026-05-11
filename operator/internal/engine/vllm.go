package engine

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const VLLMImageTag = "vllm/vllm-openai:v0.4.2"

// VLLMEngine implements InferenceEngine for vLLM.
type VLLMEngine struct{}

func NewVLLMEngine() InferenceEngine { return &VLLMEngine{} }

func (e *VLLMEngine) Name() string          { return "vllm" }
func (e *VLLMEngine) ContainerImage() string { return VLLMImageTag }
func (e *VLLMEngine) Port() int32           { return 8000 }
func (e *VLLMEngine) ReadinessPath() string  { return "/health" }
func (e *VLLMEngine) MetricName() string     { return "vllm_num_requests_running" }

func (e *VLLMEngine) Args(modelRef string, catalogArgs []string, extraArgs []string) []string {
	base := []string{"--model", modelRef, "--port", "8000", "--served-model-name", modelRef}
	base = append(base, catalogArgs...)
	base = append(base, extraArgs...)
	return base
}

func (e *VLLMEngine) GPUResources(gpuClass string) corev1.ResourceList {
	return corev1.ResourceList{
		"nvidia.com/gpu": resource.MustParse("1"),
	}
}
