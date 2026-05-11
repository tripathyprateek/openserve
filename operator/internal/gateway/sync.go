package gateway

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"gopkg.in/yaml.v3"
)

const routingConfigMapName = "openserve-gateway-routes"
const routingConfigMapKey = "routing.yaml"

type Syncer struct {
	client.Client
	Namespace          string // openserve-system
	InferenceNamespace string // openserve-inference
}

func NewSyncer(c client.Client, namespace, inferenceNamespace string) *Syncer {
	return &Syncer{
		Client:             c,
		Namespace:          namespace,
		InferenceNamespace: inferenceNamespace,
	}
}

// AddRoute upserts the route for deploymentID → upstream in the ConfigMap.
// upstream format: "vllm-<name>.<inferenceNamespace>.svc.cluster.local:8000"
// Creates the ConfigMap if it doesn't exist.
func (s *Syncer) AddRoute(ctx context.Context, deploymentID string) error {
	upstream := fmt.Sprintf("vllm-%s.%s.svc.cluster.local:8000", deploymentID, s.InferenceNamespace)
	return s.syncRoute(ctx, deploymentID, upstream, true)
}

// RemoveRoute removes the route for deploymentID from the ConfigMap.
func (s *Syncer) RemoveRoute(ctx context.Context, deploymentID string) error {
	return s.syncRoute(ctx, deploymentID, "", false)
}

func (s *Syncer) syncRoute(ctx context.Context, deploymentID string, upstream string, add bool) error {
	cm := &corev1.ConfigMap{}
	cmKey := client.ObjectKey{Name: routingConfigMapName, Namespace: s.Namespace}

	err := s.Get(ctx, cmKey, cm)
	if errors.IsNotFound(err) && !add {
		// ConfigMap doesn't exist and we're removing — nothing to do
		return nil
	}
	if errors.IsNotFound(err) && add {
		// Create new ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routingConfigMapName,
				Namespace: s.Namespace,
			},
			Data: make(map[string]string),
		}
	} else if err != nil {
		return err
	}

	// Parse existing routes YAML
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	routes := make(map[string]interface{})
	if yamlData, ok := cm.Data[routingConfigMapKey]; ok {
		if err := yaml.Unmarshal([]byte(yamlData), &routes); err != nil {
			routes = make(map[string]interface{})
		}
	}

	// Ensure routes.routes exists as a map
	var routesMap map[string]interface{}
	if routesVal, ok := routes["routes"]; ok {
		if m, ok := routesVal.(map[string]interface{}); ok {
			routesMap = m
		} else {
			routesMap = make(map[string]interface{})
		}
	} else {
		routesMap = make(map[string]interface{})
	}

	// Add or remove route
	if add {
		routesMap[deploymentID] = upstream
	} else {
		delete(routesMap, deploymentID)
	}

	routes["routes"] = routesMap

	// Re-serialize to YAML
	yamlBytes, err := yaml.Marshal(routes)
	if err != nil {
		return fmt.Errorf("failed to marshal routes YAML: %w", err)
	}

	cm.Data[routingConfigMapKey] = string(yamlBytes)

	// Create or update ConfigMap
	if cm.ResourceVersion == "" {
		return s.Create(ctx, cm)
	}
	return s.Update(ctx, cm)
}
