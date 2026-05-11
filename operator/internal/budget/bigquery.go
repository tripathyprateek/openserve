package budget

import (
	"context"
	"fmt"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

type SpendResult struct {
	DeploymentID string
	InputTokens  int64
	OutputTokens int64
	EstUSDSpend  float64
}

type Client struct {
	bqClient   *bigquery.Client
	dataset    string
	gcpProject string
}

func NewClient(ctx context.Context, gcpProject, dataset string) (*Client, error) {
	client, err := bigquery.NewClient(ctx, gcpProject)
	if err != nil {
		return nil, fmt.Errorf("failed to create BigQuery client: %w", err)
	}

	return &Client{
		bqClient:   client,
		dataset:    dataset,
		gcpProject: gcpProject,
	}, nil
}

// QueryTodaySpend returns estimated spend for each deployment for the current UTC calendar day.
// It queries the table `{dataset}.token_usage` with schema:
//   deployment_id STRING, input_tokens INT64, output_tokens INT64, request_ts TIMESTAMP
// Token-based estimate: ($0.0000004 per input token) + ($0.0000012 per output token)
// Only sums rows WHERE DATE(request_ts, "UTC") = CURRENT_DATE("UTC")
func (c *Client) QueryTodaySpend(ctx context.Context) ([]SpendResult, error) {
	query := fmt.Sprintf(`
		SELECT
			deployment_id,
			SUM(input_tokens) as input_tokens,
			SUM(output_tokens) as output_tokens
		FROM %s.token_usage
		WHERE DATE(request_ts, "UTC") = CURRENT_DATE("UTC")
		GROUP BY deployment_id
	`, c.dataset)

	q := c.bqClient.Query(query)
	it, err := q.Read(ctx)
	if err != nil {
		// Table may not exist yet; return empty slice
		if err.Error() == "googleapi: Error 404: Not found: Dataset" ||
			err.Error() == "googleapi: Error 404: Not found: Table" {
			return []SpendResult{}, nil
		}
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	var results []SpendResult
	for {
		var row struct {
			DeploymentID string `bigquery:"deployment_id"`
			InputTokens  int64  `bigquery:"input_tokens"`
			OutputTokens int64  `bigquery:"output_tokens"`
		}

		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}

		// Token-based cost estimate
		// $0.0000004 per input token, $0.0000012 per output token
		estSpend := float64(row.InputTokens)*0.0000004 + float64(row.OutputTokens)*0.0000012

		results = append(results, SpendResult{
			DeploymentID: row.DeploymentID,
			InputTokens:  row.InputTokens,
			OutputTokens: row.OutputTokens,
			EstUSDSpend:  estSpend,
		})
	}

	return results, nil
}
