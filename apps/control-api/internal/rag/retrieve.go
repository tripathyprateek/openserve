package rag

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// RetrievedChunk is a retrieved text segment.
type RetrievedChunk struct {
	DocumentID string
	ChunkIndex int
	Content    string
	Score      float64
}

// Retrieve finds the top-k most relevant chunks for a query embedding.
func Retrieve(ctx context.Context, db *pgxpool.Pool, orgID string, queryEmbedding []float32, topK int) ([]RetrievedChunk, error) {
	if topK <= 0 {
		topK = 5
	}

	vec := pgvector.NewVector(queryEmbedding)
	rows, err := db.Query(ctx, `
		SELECT kc.document_id::text, kc.chunk_index, kc.content,
		       1 - (kc.embedding <=> $1) AS score
		FROM knowledge_chunks kc
		WHERE kc.org_id = $2
		  AND kc.embedding IS NOT NULL
		ORDER BY kc.embedding <=> $1
		LIMIT $3
	`, vec, orgID, topK)
	if err != nil {
		return nil, fmt.Errorf("retrieve: query: %w", err)
	}
	defer rows.Close()

	var results []RetrievedChunk
	for rows.Next() {
		var c RetrievedChunk
		if err := rows.Scan(&c.DocumentID, &c.ChunkIndex, &c.Content, &c.Score); err != nil {
			return nil, err
		}
		results = append(results, c)
	}
	return results, rows.Err()
}
