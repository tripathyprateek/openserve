package k8s

import (
	"context"
	"fmt"
	"os"

	openservev1alpha1 "github.com/openserve/openserve/operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client wraps controller-runtime for openserve CRD operations.
type Client struct {
	inner     client.Client
	namespace string
}

// CreateDeploymentRequest holds parameters for creating a ModelDeployment.
type CreateDeploymentRequest struct {
	Name            string
	ModelRef        string
	GPUClass        string // "l4" or "a100-40g"
	ScaleToZero     bool
	IdleTimeoutMin  int32
	MinReplicas     int32
	MaxReplicas     int32
	DailyBudgetUSD  string // e.g. "50"
	MaxInputTokens  int32
	MaxOutputTokens int32
	VLLMArgs        []string
	Description     string
	OrgID           string // stored as annotation
	CreatedByEmail  string // stored as annotation
}

// New creates a K8s client using in-cluster config (Workload Identity).
// Falls back to kubeconfig if not running in-cluster (for local dev).
func New(namespace string) (*Client, error) {
	scheme := runtime.NewScheme()
	if err := openservev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add openserve types to scheme: %w", err)
	}

	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.ExpandEnv("$HOME/.kube/config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}
	}

	inner, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &Client{
		inner:     inner,
		namespace: namespace,
	}, nil
}

// CreateModelDeployment creates a ModelDeployment CR from the given request.
func (c *Client) CreateModelDeployment(ctx context.Context, req CreateDeploymentRequest) (*openservev1alpha1.ModelDeployment, error) {
	budget, err := resource.ParseQuantity(req.DailyBudgetUSD)
	if err != nil {
		return nil, fmt.Errorf("invalid budget quantity: %w", err)
	}

	md := &openservev1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: c.namespace,
			Annotations: map[string]string{
				"openserve.io/org-id":      req.OrgID,
				"openserve.io/created-by":  req.CreatedByEmail,
			},
		},
		Spec: openservev1alpha1.ModelDeploymentSpec{
			ModelRef:       req.ModelRef,
			GPUClass:       openservev1alpha1.GPUClass(req.GPUClass),
			ScaleToZero:    req.ScaleToZero,
			IdleTimeoutMin: req.IdleTimeoutMin,
			MinReplicas:    req.MinReplicas,
			MaxReplicas:    req.MaxReplicas,
			Budget: openservev1alpha1.BudgetSpec{
				DailyUsdCap:           budget,
				AlertThresholdPercent: 80,
			},
			Limits: openservev1alpha1.TokenLimits{
				MaxInputTokens:  req.MaxInputTokens,
				MaxOutputTokens: req.MaxOutputTokens,
			},
			VLLMArgs:    req.VLLMArgs,
			Description: req.Description,
		},
	}

	if err := c.inner.Create(ctx, md); err != nil {
		return nil, fmt.Errorf("failed to create ModelDeployment: %w", err)
	}

	return md, nil
}

// DeleteModelDeployment deletes the ModelDeployment CR by name.
func (c *Client) DeleteModelDeployment(ctx context.Context, name string) error {
	md := &openservev1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}

	if err := c.inner.Delete(ctx, md); err != nil {
		return fmt.Errorf("failed to delete ModelDeployment: %w", err)
	}

	return nil
}

// GetModelDeployment retrieves a ModelDeployment CR by name.
func (c *Client) GetModelDeployment(ctx context.Context, name string) (*openservev1alpha1.ModelDeployment, error) {
	md := &openservev1alpha1.ModelDeployment{}
	key := client.ObjectKey{
		Name:      name,
		Namespace: c.namespace,
	}

	if err := c.inner.Get(ctx, key, md); err != nil {
		return nil, fmt.Errorf("failed to get ModelDeployment: %w", err)
	}

	return md, nil
}

// ListModelDeployments lists all ModelDeployment CRs in the namespace.
func (c *Client) ListModelDeployments(ctx context.Context) ([]openservev1alpha1.ModelDeployment, error) {
	mdList := &openservev1alpha1.ModelDeploymentList{}

	if err := c.inner.List(ctx, mdList, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("failed to list ModelDeployments: %w", err)
	}

	return mdList.Items, nil
}

// ResumeModelDeployment clears the BudgetPaused phase so the operator re-reconciles.
func (c *Client) ResumeModelDeployment(ctx context.Context, name string) error {
	md := &openservev1alpha1.ModelDeployment{}
	key := client.ObjectKey{
		Name:      name,
		Namespace: c.namespace,
	}

	if err := c.inner.Get(ctx, key, md); err != nil {
		return fmt.Errorf("failed to get ModelDeployment: %w", err)
	}

	// Patch to clear budget paused status
	patch := client.MergeFrom(md.DeepCopy())
	md.Status.Phase = openservev1alpha1.DeploymentPhasePending
	md.Status.BudgetPausedAt = nil

	if err := c.inner.Status().Patch(ctx, md, patch); err != nil {
		return fmt.Errorf("failed to patch ModelDeployment status: %w", err)
	}

	return nil
}
