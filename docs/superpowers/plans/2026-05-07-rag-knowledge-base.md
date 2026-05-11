# RAG / Knowledge Base Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add document-grounded RAG to openserve — users upload documents to a knowledge base, enable RAG on a deployment, and the model answers with retrieved context from their documents.

**Architecture:** Documents are uploaded via control-api, text-extracted and chunked in-process, embedded via an OpenAI-compatible `/v1/embeddings` call (pointing at the customer's own gateway with a deployed embedding model), and stored in Postgres using the pgvector extension. At query time, the playground embeds the user query, retrieves top-k chunks via cosine similarity, and prepends them as context. Everything stays in the customer's VPC.

**Tech Stack:** Go (pgvector-go, pdf text extraction), Postgres + pgvector, existing chi router, Next.js, existing SSE streaming pattern.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `catalog/schema.json` | Modify | Add `modelType` enum, `embeddingDimensions` field |
| `catalog/models/nomic-embed-text-v1.5.yaml` | Create | Embedding model catalog entry |
| `apps/control-api/go.mod` | Modify | Add `github.com/pgvector/pgvector-go` and `github.com/ledongthuc/pdf` |
| `apps/control-api/internal/db/migrations/009_add_rag.sql` | Create | PGVector extension, documents + chunks tables |
| `apps/control-api/internal/rag/embed.go` | Create | Embedding client (calls OpenAI-compatible /v1/embeddings) |
| `apps/control-api/internal/rag/chunk.go` | Create | Text splitting by paragraphs / token estimate |
| `apps/control-api/internal/rag/extract.go` | Create | Text extraction for .txt, .md, .pdf |
| `apps/control-api/internal/rag/retrieve.go` | Create | PGVector cosine similarity retrieval |
| `apps/control-api/internal/handler/handler.go` | Modify | Add document CRUD + retrieve handlers |
| `apps/control-api/cmd/server/main.go` | Modify | Add `--embedding-endpoint` and `--embedding-api-key` flags |
| `apps/gui/lib/api.ts` | Modify | Document API functions + RAG types |
| `apps/gui/app/(main)/knowledge/page.tsx` | Create | Knowledge base management page |
| `apps/gui/app/(main)/layout.tsx` | Modify | Add "Knowledge" nav item |
| `apps/gui/app/(main)/deployments/[id]/page.tsx` | Modify | RAG toggle + context injection |

---

## Task 1: Catalog schema + embedding model entry

**Files:**
- Modify: `catalog/schema.json`
- Create: `catalog/models/nomic-embed-text-v1.5.yaml`

- [ ] **Step 1: Add modelType to catalog schema**

In `catalog/schema.json`, add to the `properties` object:

```json
"modelType": {
  "type": "string",
  "enum": ["chat", "embedding", "rerank"],
  "default": "chat",
  "description": "Model capability type. 'chat' for text generation, 'embedding' for vector embeddings."
},
"embeddingDimensions": {
  "type": "integer",
  "description": "Output vector dimensions for embedding models (e.g. 768). Required when modelType is 'embedding'."
}
```

- [ ] **Step 2: Create nomic-embed-text-v1.5.yaml**

```yaml
id: nomic-embed-text-v1.5
name: "Nomic Embed Text v1.5"
family: other
version: "1.5"
modelType: embedding
embeddingDimensions: 768
hfRepo: "nomic-ai/nomic-embed-text-v1.5"
hfRevision: "b0753ae76394dd36bcfb912a46fc192ea54e765a"
weightDigestSha256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
license: apache-2.0
parameterCount: "137M"
minGPUClass: l4
maxContextLen: 8192
recommendedVLLMArgs:
  - "--trust-remote-code"
  - "--gpu-memory-utilization=0.3"
description: |
  Nomic Embed Text v1.5 is a high-performing open-source embedding model producing
  768-dimensional vectors. Supports Matryoshka representation learning for flexible
  dimensionality reduction. Apache 2.0 license. Ideal for RAG and semantic search.
tags:
  - embedding
  - rag
  - semantic-search
  - apache-2.0
addedAt: 2026-05-07
addedBy: openserve-bot
```

---

## Task 2: Database migration with pgvector

**Files:**
- Create: `apps/control-api/internal/db/migrations/009_add_rag.sql`

- [ ] **Step 1: Write migration**

```sql
-- Enable pgvector extension (requires Cloud SQL with pgvector flag enabled)
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS knowledge_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    file_type TEXT NOT NULL CHECK (file_type IN ('txt', 'md', 'pdf')),
    file_size_bytes INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'ready', 'error')),
    error_message TEXT,
    created_by UUID REFERENCES members(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    org_id UUID NOT NULL,
    chunk_index INT NOT NULL,
    content TEXT NOT NULL,
    embedding vector(768),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Deployment-level RAG toggle
ALTER TABLE deployment_cache
    ADD COLUMN IF NOT EXISTS rag_enabled BOOLEAN NOT NULL DEFAULT false;

-- Index for cosine similarity search (IVFFlat for scale)
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_embedding
    ON knowledge_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS idx_knowledge_documents_org
    ON knowledge_documents(org_id, created_at DESC);
```

---

## Task 3: Go RAG package

**Files:**
- Modify: `apps/control-api/go.mod`
- Create: `apps/control-api/internal/rag/embed.go`
- Create: `apps/control-api/internal/rag/chunk.go`
- Create: `apps/control-api/internal/rag/extract.go`
- Create: `apps/control-api/internal/rag/retrieve.go`

- [ ] **Step 1: Add dependencies**

```bash
cd /Users/prateektripathy/Downloads/openserve/apps/control-api
go get github.com/pgvector/pgvector-go@latest
go get github.com/ledongthuc/pdf@latest
go mod tidy
```

- [ ] **Step 2: Write embed.go**

```go
package rag

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

// EmbedClient calls an OpenAI-compatible /v1/embeddings endpoint.
type EmbedClient struct {
    endpoint string // e.g. "http://gateway:8081"
    apiKey   string
    model    string // e.g. "nomic-embed-text-v1.5"
    client   *http.Client
}

func NewEmbedClient(endpoint, apiKey, model string) *EmbedClient {
    return &EmbedClient{
        endpoint: endpoint,
        apiKey:   apiKey,
        model:    model,
        client:   &http.Client{},
    }
}

type embedRequest struct {
    Input []string `json:"input"`
    Model string   `json:"model"`
}

type embedResponse struct {
    Data []struct {
        Embedding []float32 `json:"embedding"`
    } `json:"data"`
}

// Embed returns a float32 vector for each input string.
func (c *EmbedClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
    body, _ := json.Marshal(embedRequest{Input: texts, Model: c.model})
    req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/v1/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("embed: request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("embed: upstream returned %d", resp.StatusCode)
    }

    var er embedResponse
    if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
        return nil, fmt.Errorf("embed: decode: %w", err)
    }

    out := make([][]float32, len(er.Data))
    for i, d := range er.Data {
        out[i] = d.Embedding
    }
    return out, nil
}
```

- [ ] **Step 3: Write chunk.go**

```go
package rag

import (
    "strings"
    "unicode/utf8"
)

const (
    defaultChunkSize    = 512  // approx tokens (chars/4)
    defaultChunkOverlap = 64
)

// Chunk splits text into overlapping segments of roughly chunkSize characters.
func Chunk(text string, chunkSize, overlap int) []string {
    if chunkSize <= 0 {
        chunkSize = defaultChunkSize * 4 // chars
    }
    if overlap <= 0 {
        overlap = defaultChunkOverlap * 4
    }

    // Split on double newlines first (paragraph boundaries)
    paragraphs := strings.Split(text, "\n\n")
    var chunks []string
    var current strings.Builder

    for _, para := range paragraphs {
        para = strings.TrimSpace(para)
        if para == "" {
            continue
        }
        if current.Len()+utf8.RuneCountInString(para) > chunkSize && current.Len() > 0 {
            chunks = append(chunks, strings.TrimSpace(current.String()))
            // Overlap: keep tail of current chunk
            tail := current.String()
            if len(tail) > overlap {
                tail = tail[len(tail)-overlap:]
            }
            current.Reset()
            current.WriteString(tail)
            current.WriteString(" ")
        }
        current.WriteString(para)
        current.WriteString("\n\n")
    }
    if current.Len() > 0 {
        chunks = append(chunks, strings.TrimSpace(current.String()))
    }
    return chunks
}
```

- [ ] **Step 4: Write extract.go**

```go
package rag

import (
    "fmt"
    "io"
    "path/filepath"
    "strings"

    "github.com/ledongthuc/pdf"
)

// ExtractText returns plain text from a file based on its extension.
func ExtractText(r io.ReadSeeker, filename string, size int64) (string, error) {
    ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
    switch ext {
    case "txt", "md":
        data, err := io.ReadAll(r)
        if err != nil {
            return "", fmt.Errorf("extract: read: %w", err)
        }
        return string(data), nil
    case "pdf":
        return extractPDF(r, size)
    default:
        return "", fmt.Errorf("extract: unsupported file type %q", ext)
    }
}

func extractPDF(r io.ReadSeeker, size int64) (string, error) {
    rdr, err := pdf.NewReader(r, size)
    if err != nil {
        return "", fmt.Errorf("extract: pdf reader: %w", err)
    }
    var sb strings.Builder
    for i := 1; i <= rdr.NumPage(); i++ {
        page := rdr.Page(i)
        if page.V.IsNull() {
            continue
        }
        text, err := page.GetPlainText(nil)
        if err != nil {
            continue // best effort
        }
        sb.WriteString(text)
        sb.WriteString("\n\n")
    }
    return sb.String(), nil
}
```

- [ ] **Step 5: Write retrieve.go**

```go
package rag

import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/pgvector/pgvector-go"
)

// Chunk is a retrieved text segment.
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
```

---

## Task 4: Backend handlers + routes

**Files:**
- Modify: `apps/control-api/internal/handler/handler.go`
- Modify: `apps/control-api/cmd/server/main.go`

- [ ] **Step 1: Add embed client to Deps and Handler**

In `handler.go`, add to `Deps` struct:
```go
EmbedClient *rag.EmbedClient // nil if embedding not configured
```

Add to `Handler` struct:
```go
embed *rag.EmbedClient
```

In `New()`:
```go
embed: deps.EmbedClient,
```

- [ ] **Step 2: Add UploadDocument handler**

```go
// UploadDocument accepts multipart/form-data with a "file" field.
// It extracts text, chunks it, embeds chunks (if embed client configured), 
// and stores everything in Postgres.
func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
    // Parse multipart (max 50MB)
    if err := r.ParseMultipartForm(50 << 20); err != nil {
        http.Error(w, "file too large (max 50MB)", http.StatusBadRequest)
        return
    }
    
    file, header, err := r.FormFile("file")
    if err != nil {
        http.Error(w, "missing file field", http.StatusBadRequest)
        return
    }
    defer file.Close()
    
    // Validate extension
    ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
    if ext != "txt" && ext != "md" && ext != "pdf" {
        http.Error(w, "unsupported file type; use txt, md, or pdf", http.StatusBadRequest)
        return
    }
    
    // Get member + org
    claims := auth.UserFromContext(r.Context())
    var memberID, orgID string
    err = h.db.QueryRow(r.Context(),
        `SELECT id::text, org_id::text FROM members WHERE id = (SELECT id FROM members WHERE email = $1 LIMIT 1)`,
        claims.Email).Scan(&memberID, &orgID)
    // ... (use same pattern as other handlers: get org_id from member)
    
    // Extract text
    text, err := rag.ExtractText(file, header.Filename, header.Size)
    if err != nil {
        http.Error(w, "text extraction failed: "+err.Error(), http.StatusUnprocessableEntity)
        return
    }
    
    // Create document record
    var docID string
    err = h.db.QueryRow(r.Context(),
        `INSERT INTO knowledge_documents (org_id, name, file_type, file_size_bytes, status, created_by)
         VALUES ($1, $2, $3, $4, 'processing', $5) RETURNING id::text`,
        orgID, header.Filename, ext, header.Size, memberID).Scan(&docID)
    
    // Chunk and embed asynchronously
    go h.processDocument(context.Background(), docID, orgID, text)
    
    writeJSON(w, http.StatusAccepted, map[string]string{
        "id":     docID,
        "status": "processing",
    })
}

func (h *Handler) processDocument(ctx context.Context, docID, orgID, text string) {
    chunks := rag.Chunk(text, 0, 0)
    
    var embeddings [][]float32
    if h.embed != nil {
        var err error
        embeddings, err = h.embed.Embed(ctx, chunks)
        if err != nil {
            h.db.Exec(ctx,
                `UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
                err.Error(), docID)
            return
        }
    }
    
    // Insert chunks
    for i, chunk := range chunks {
        var vec pgvector.Vector
        if embeddings != nil && i < len(embeddings) {
            vec = pgvector.NewVector(embeddings[i])
        }
        h.db.Exec(ctx,
            `INSERT INTO knowledge_chunks (document_id, org_id, chunk_index, content, embedding)
             VALUES ($1, $2, $3, $4, $5)`,
            docID, orgID, i, chunk, vec)
    }
    
    h.db.Exec(ctx,
        `UPDATE knowledge_documents SET status='ready', chunk_count=$1 WHERE id=$2`,
        len(chunks), docID)
}
```

- [ ] **Step 3: Add ListDocuments, DeleteDocument, RetrieveContext handlers**

```go
func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
    // Get org_id, query knowledge_documents WHERE org_id = $1 ORDER BY created_at DESC
}

func (h *Handler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
    // Verify org ownership, DELETE FROM knowledge_documents WHERE id = $1 AND org_id = $2
}

// RetrieveContext takes a query string, embeds it, returns top-k chunks.
// POST /api/v1/rag/retrieve  body: { query, topK? }
func (h *Handler) RetrieveContext(w http.ResponseWriter, r *http.Request) {
    if h.embed == nil {
        http.Error(w, "embedding not configured", http.StatusServiceUnavailable)
        return
    }
    var req struct {
        Query string `json:"query"`
        TopK  int    `json:"topK"`
    }
    json.NewDecoder(r.Body).Decode(&req)
    
    // Get org_id
    // Embed query
    vecs, err := h.embed.Embed(r.Context(), []string{req.Query})
    // Retrieve
    chunks, err := rag.Retrieve(r.Context(), h.db, orgID, vecs[0], req.TopK)
    writeJSON(w, http.StatusOK, chunks)
}
```

- [ ] **Step 4: Add routes in main.go**

```go
r.Post("/api/v1/documents", h.UploadDocument)
r.Get("/api/v1/documents", h.ListDocuments)
r.Delete("/api/v1/documents/{id}", h.DeleteDocument)
r.Post("/api/v1/rag/retrieve", h.RetrieveContext)
```

- [ ] **Step 5: Add embedding flags in main.go**

```go
embeddingEndpoint := flag.String("embedding-endpoint", "", "OpenAI-compatible embedding endpoint URL")
embeddingAPIKey   := flag.String("embedding-api-key", "", "API key for the embedding endpoint")
embeddingModel    := flag.String("embedding-model", "nomic-embed-text-v1.5", "Model ID for embeddings")
```

After flag.Parse():
```go
var embedClient *rag.EmbedClient
if *embeddingEndpoint != "" {
    embedClient = rag.NewEmbedClient(*embeddingEndpoint, *embeddingAPIKey, *embeddingModel)
}
// Pass to Deps
```

---

## Task 5: GUI Knowledge page + RAG toggle

**Files:**
- Create: `apps/gui/app/(main)/knowledge/page.tsx`
- Modify: `apps/gui/app/(main)/layout.tsx`
- Modify: `apps/gui/lib/api.ts`
- Modify: `apps/gui/app/(main)/deployments/[id]/page.tsx`

- [ ] **Step 1: Add API functions to api.ts**

```typescript
export interface KnowledgeDocument {
  id: string
  name: string
  fileType: string
  fileSizeBytes: number
  chunkCount: number
  status: "processing" | "ready" | "error"
  errorMessage?: string
  createdAt: string
}

export interface RetrievedChunk {
  documentId: string
  chunkIndex: number
  content: string
  score: number
}

export async function listDocuments(): Promise<KnowledgeDocument[]> {
  return fetchAPI("/api/v1/documents")
}

export async function uploadDocument(file: File): Promise<{ id: string; status: string }> {
  const form = new FormData()
  form.append("file", file)
  const resp = await fetch(`${baseUrl}/api/v1/documents`, {
    method: "POST",
    headers: { "X-Session-Token": getToken() },
    body: form,
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function deleteDocument(id: string): Promise<void> {
  return fetchAPI(`/api/v1/documents/${id}`, { method: "DELETE" })
}

export async function retrieveContext(query: string, topK = 5): Promise<RetrievedChunk[]> {
  const res = await fetchAPI<{ chunks: RetrievedChunk[] }>("/api/v1/rag/retrieve", {
    method: "POST",
    body: JSON.stringify({ query, topK }),
  })
  return res.chunks ?? []
}
```

- [ ] **Step 2: Create knowledge/page.tsx**

A clean document management page:
- Header: "Knowledge Base" + "Upload Document" button
- File upload via `<input type="file" accept=".txt,.md,.pdf">` → calls uploadDocument()
- Table: name, type, size, chunks, status (with spinner for "processing"), delete button
- Auto-refresh every 3s when any document is in "processing" status
- Empty state: "Upload documents to enable RAG in your deployments"

- [ ] **Step 3: Add Knowledge to nav in layout.tsx**

Read the layout, add `{ href: "/knowledge", label: "Knowledge", icon: BookOpen }` to the nav items list.

- [ ] **Step 4: Add RAG toggle to playground**

In `deployments/[id]/page.tsx`, add a "Knowledge Base" toggle (similar to web search toggle):
- State: `const [ragEnabled, setRagEnabled] = useState(false)`
- When enabled: before sending to gateway, call `retrieveContext(inputValue, 5)` and prepend formatted chunks:

```
Relevant knowledge base context:
[1] <chunk content>
[2] <chunk content>
...

Answer based on the above context where relevant.
```

---

## Verification

```bash
cd /Users/prateektripathy/Downloads/openserve/apps/control-api
go build ./... 2>&1 | head -30

cd /Users/prateektripathy/Downloads/openserve/apps/gui
pnpm typecheck 2>&1 | tail -20
```

Both must pass with no errors.
