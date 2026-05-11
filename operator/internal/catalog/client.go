package catalog

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"

	"cloud.google.com/go/storage"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
)

// Model is a catalog manifest entry (matches catalog/models/*.yaml schema).
type Model struct {
	ID                  string   `yaml:"id"`
	Name                string   `yaml:"name"`
	HFRepo              string   `yaml:"hfRepo"`
	HFRevision          string   `yaml:"hfRevision"`
	WeightDigestSha256  string   `yaml:"weightDigestSha256"`
	MinGPUClass         string   `yaml:"minGPUClass"`
	MaxContextLen       int      `yaml:"maxContextLen"`
	RecommendedVLLMArgs []string `yaml:"recommendedVLLMArgs"`
	License             string   `yaml:"license"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

// GetModel fetches the manifest for a model ID from {baseURL}/v1/models/{id}.
// Returns error if the model is not in the catalog.
func (c *Client) GetModel(ctx context.Context, id string) (*Model, error) {
	url := fmt.Sprintf("%s/v1/models/%s", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog API returned status %d for model %s", resp.StatusCode, id)
	}

	var model Model
	if err := yaml.NewDecoder(resp.Body).Decode(&model); err != nil {
		return nil, fmt.Errorf("failed to decode catalog manifest for %s: %w", id, err)
	}

	return &model, nil
}

// VerifyWeights checks that the SHA256 digest of the file at gcsPath matches
// the model's WeightDigestSha256. Uses storage.NewClient() with Application Default Credentials.
// gcsPath format: "gs://bucket/path/to/weights.tar"
// Returns nil if verification passes, error otherwise.
// IMPORTANT: This must be called before scheduling a vLLM pod.
func (c *Client) VerifyWeights(ctx context.Context, model *Model, gcsPath string, log logr.Logger) error {
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer storageClient.Close()

	// Parse gs://bucket/path format
	var bucket, object string
	_, err = fmt.Sscanf(gcsPath, "gs://%[1]s/%[2]s", &bucket, &object)
	if err != nil {
		// Try simple split approach
		if len(gcsPath) > 5 && gcsPath[:5] == "gs://" {
			rest := gcsPath[5:]
			parts := make([]byte, 0)
			for i := 0; i < len(rest); i++ {
				if rest[i] == '/' {
					bucket = rest[:i]
					object = rest[i+1:]
					break
				}
			}
			if bucket == "" {
				return fmt.Errorf("invalid gcsPath format: %s", gcsPath)
			}
		} else {
			return fmt.Errorf("invalid gcsPath format: %s", gcsPath)
		}
	}

	reader, err := storageClient.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to open GCS object %s: %w", gcsPath, err)
	}
	defer reader.Close()

	hash := sha256.New()
	const bufSize = 1024 * 1024 // 1MB chunks
	buf := make([]byte, bufSize)
	bytesRead := int64(0)
	lastLogAt := int64(0)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
			bytesRead += int64(n)

			// Log progress every 1GB
			if bytesRead-lastLogAt >= 1024*1024*1024 {
				log.Info("verifying weights progress", "model", model.ID, "bytesRead", bytesRead)
				lastLogAt = bytesRead
			}
		}
		if err != nil {
			if err != io.EOF {
				return fmt.Errorf("failed to read GCS object: %w", err)
			}
			break
		}
	}

	computed := fmt.Sprintf("%x", hash.Sum(nil))
	if computed != model.WeightDigestSha256 {
		return fmt.Errorf("weight digest mismatch for model %s: expected %s, got %s",
			model.ID, model.WeightDigestSha256, computed)
	}

	log.Info("weights verified successfully", "model", model.ID, "digest", computed)
	return nil
}
