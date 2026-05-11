package engine

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const SGLangImageTag = "lmsysorg/sglang:v0.3.6-cu121"

// SGLangEngine implements InferenceEngine for SGLang.
type SGLangEngine struct{}

func NewSGLangEngine() InferenceEngine { return &SGLangEngine{} }

func (e *SGLangEngine) Name() string          { return "sglang" }
func (e *SGLangEngine) ContainerImage() string { return SGLangImageTag }
func (e *SGLangEngine) Port() int32           { return 30000 }
func (e *SGLangEngine) ReadinessPath() string  { return "/health" }
func (e *SGLangEngine) MetricName() string     { return "sglang:num_running_reqs" }

func (e *SGLangEngine) Args(modelRef string, catalogArgs []string, extraArgs []string) []string {
	base := []string{
		"python", "-m", "sglang.launch_server",
		"--model-path", modelRef,
		"--port", "30000",
		"--host", "0.0.0.0",
	}
	base = append(base, extraArgs...)
	return base
}

func (e *SGLangEngine) GPUResources(gpuClass string) corev1.ResourceList {
	return corev1.ResourceList{
		"nvidia.com/gpu": resource.MustParse("1"),
	}
}
