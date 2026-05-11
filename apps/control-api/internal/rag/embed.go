package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// EmbedClient calls an OpenAI-compatible /v1/embeddings endpoint
type EmbedClient struct {
	endpoint  string
	apiKey    string
	model     string
	httpClient *http.Client
}

// NewEmbedClient creates a new embedding client
func NewEmbedClient(endpoint, apiKey, model string) *EmbedClient {
	return &EmbedClient{
		endpoint:   endpoint,
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{},
	}
}

// embedRequest matches OpenAI-compatible request format
type embedRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embedResponse matches OpenAI-compatible response format
type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed calls the embeddings endpoint and returns vectors for each text
func (ec *EmbedClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := embedRequest{
		Input: texts,
		Model: ec.model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ec.endpoint+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ec.apiKey)

	resp, err := ec.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var embResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	result := make([][]float32, len(embResp.Data))
	for i, d := range embResp.Data {
		result[i] = d.Embedding
	}

	return result, nil
}
