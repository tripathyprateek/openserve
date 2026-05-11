package engine

import "fmt"

// ForType returns the InferenceEngine for the given engine type string.
// Returns error for unknown engine types.
func ForType(engineType string) (InferenceEngine, error) {
	switch engineType {
	case "vllm", "":
		return NewVLLMEngine(), nil
	case "sglang":
		return NewSGLangEngine(), nil
	default:
		return nil, fmt.Errorf("unknown inference engine %q: supported engines are vllm, sglang", engineType)
	}
}
