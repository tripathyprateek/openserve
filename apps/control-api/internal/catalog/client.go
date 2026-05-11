package catalog

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

//go:embed default_models.json
var defaultCatalogFS embed.FS

// Model represents a model in the catalog.
type Model struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Family              string   `json:"family"`
	ParameterCount      string   `json:"parameterCount"`
	License             string   `json:"license"`
	MinGPUClass         string   `json:"minGPUClass"`
	MaxContextLen       int      `json:"contextLength"` // frontend expects "contextLength"
	Description         string   `json:"description"`
	Tags                []string `json:"tags"`
	RecommendedVLLMArgs []string `json:"recommendedVLLMArgs"`
	// Optional metadata sourced from HuggingFace
	HFRepo    string `json:"hfRepo,omitempty"`
	Downloads int    `json:"downloads,omitempty"`
}

// Client manages communication with the catalog service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new catalog client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ListModels retrieves the list of available models.
// Falls back to embedded default catalog if HTTP request fails.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	url := fmt.Sprintf("%s/v1/models", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return c.loadDefaultCatalog()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.loadDefaultCatalog()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.loadDefaultCatalog()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.loadDefaultCatalog()
	}

	var models []Model
	if err := json.Unmarshal(body, &models); err != nil {
		return c.loadDefaultCatalog()
	}

	return models, nil
}

// GetModel retrieves a specific model by ID.
func (c *Client) GetModel(ctx context.Context, id string) (*Model, error) {
	url := fmt.Sprintf("%s/v1/models/%s", c.baseURL, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var model Model
	if err := json.Unmarshal(body, &model); err != nil {
		return nil, fmt.Errorf("failed to unmarshal model: %w", err)
	}

	return &model, nil
}

// loadDefaultCatalog loads the embedded default catalog.
func (c *Client) loadDefaultCatalog() ([]Model, error) {
	data, err := defaultCatalogFS.ReadFile("default_models.json")
	if err != nil {
		return nil, fmt.Errorf("failed to load default catalog: %w", err)
	}

	var models []Model
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, fmt.Errorf("failed to unmarshal default catalog: %w", err)
	}

	return models, nil
}
