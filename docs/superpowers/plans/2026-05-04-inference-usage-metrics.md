# Inference Usage Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record real per-request token counts and latency from the gateway, store in Postgres, and surface them in the GUI usage dashboard as actual inference metrics instead of proxied audit log counts.

**Architecture:** The gateway already proxies SSE streams from vLLM. vLLM includes a final SSE chunk with an OpenAI-standard `usage` object (`{"usage":{"prompt_tokens":N,"completion_tokens":N,"total_tokens":N}}`). The gateway intercepts this final chunk, writes an `inference_events` row to Postgres (non-blocking goroutine), and forwards the chunk unchanged. The control-api `GetUsage` handler then queries `inference_events` for token sums and daily breakdowns. The GUI usage page already calls `getUsage()` — only the response shape needs updating.

**Tech Stack:** Go (gateway), pgx (Postgres from gateway), existing chi router pattern, Next.js (existing usage page).

---

## File Map

| File | Action | What changes |
|---|---|---|
| `apps/control-api/internal/db/migrations/005_add_inference_events.sql` | Create | New table for per-request metrics |
| `apps/gateway/internal/proxy/proxy.go` | Modify | Intercept SSE `usage` chunk, write to inference_events |
| `apps/gateway/go.mod` | Verify | pgx already imported (it is — used for auth) |
| `apps/control-api/internal/handler/handler.go` | Modify | `GetUsage` queries inference_events for real metrics |
| `apps/gui/lib/api.ts` | Modify | Update `UsageResponse` interface to include token fields |
| `apps/gui/app/(main)/usage/page.tsx` | Modify | Render token counts alongside request counts |

**Note on migration numbering:** Check what the highest existing migration number is. If `005_add_web_search.sql` was already created by another agent, use `006_add_inference_events.sql`. The plan uses `005` — adjust the filename to the next available number.

---

## Task 1: DB Migration — create inference_events table

**Files:**
- Create: `apps/control-api/internal/db/migrations/005_add_inference_events.sql` (or 006 if 005 is taken)

- [ ] **Step 1: Check existing migration numbers**

```bash
ls /path/to/repo/apps/control-api/internal/db/migrations/
```

Use the next available number (e.g., if 005 exists, use 006).

- [ ] **Step 2: Create the migration file**

```sql
-- 00N_add_inference_events.sql
-- Records per-request inference metrics from the gateway.
-- No prompt content is stored — only metadata.

CREATE TABLE IF NOT EXISTS inference_events (
    id           BIGSERIAL PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    key_id       UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    deployment_id TEXT NOT NULL,   -- deployment name / peer-{id}
    model        TEXT NOT NULL,
    request_id   TEXT,             -- X-Request-ID from the gateway
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,  -- wall time from first byte to last byte
    status        INTEGER NOT NULL DEFAULT 200, -- HTTP status returned to caller
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS inference_events_org_day_idx
    ON inference_events (org_id, created_at DESC);

CREATE INDEX IF NOT EXISTS inference_events_key_idx
    ON inference_events (key_id, created_at DESC);
```

- [ ] **Step 3: Commit**

```bash
git add apps/control-api/internal/db/migrations/
git commit -m "feat(db): add inference_events table for real token usage tracking"
```

---

## Task 2: Gateway — intercept SSE usage chunk and write metrics

**Files:**
- Modify: `apps/gateway/internal/proxy/proxy.go`

First read the file to understand the current proxy structure. The gateway uses `io.Copy` or a manual SSE read loop. We need to add a tee-reader that inspects each SSE line for `"usage"` in the final chunk.

- [ ] **Step 1: Read the current proxy.go**

```bash
cat /path/to/repo/apps/gateway/internal/proxy/proxy.go
```

- [ ] **Step 2: Add the usage-capturing SSE writer**

After the existing imports, add `"bufio"`, `"encoding/json"`, `"strconv"`, `"time"` if not already present.

Add a new function `forwardSSE` that replaces the direct `io.Copy` for streaming responses:

```go
// usageChunk is the shape of the usage field in the final OpenAI SSE chunk.
type usageChunk struct {
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// forwardSSE copies an SSE stream from src to dst, capturing the usage
// data from the final non-[DONE] chunk. Returns the captured token counts.
// This function flushes after each line so the caller sees tokens as they arrive.
func forwardSSE(dst http.ResponseWriter, src io.Reader) (inputTokens, outputTokens int) {
	flusher, canFlush := dst.(http.Flusher)

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 64*1024), 64*1024) // 64KB line buffer

	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintf(dst, "%s\n", line)
		if canFlush {
			flusher.Flush()
		}

		// Look for data lines containing usage information
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				continue
			}
			var chunk usageChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err == nil && chunk.Usage != nil {
				inputTokens = chunk.Usage.PromptTokens
				outputTokens = chunk.Usage.CompletionTokens
			}
		}
	}
	// Write trailing newline to close the SSE stream properly
	_, _ = fmt.Fprint(dst, "\n")
	return
}
```

- [ ] **Step 3: Add writeInferenceEvent helper**

Add this function to proxy.go. It writes asynchronously (goroutine) so it never blocks the response:

```go
// writeInferenceEvent records a completed inference request to Postgres.
// Called in a goroutine — never blocks the HTTP response.
func writeInferenceEvent(
	pool *pgxpool.Pool,
	orgID, keyID, deploymentID, model, requestID string,
	inputTokens, outputTokens, durationMS, status int,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	keyUUID := pgtype.Text{Valid: false}
	if keyID != "" {
		keyUUID = pgtype.Text{String: keyID, Valid: true}
	}
	reqID := pgtype.Text{Valid: false}
	if requestID != "" {
		reqID = pgtype.Text{String: requestID, Valid: true}
	}

	_, _ = pool.Exec(ctx,
		`INSERT INTO inference_events
		 (org_id, key_id, deployment_id, model, request_id, input_tokens, output_tokens, total_tokens, duration_ms, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		orgID, keyUUID, deploymentID, model, reqID,
		inputTokens, outputTokens, inputTokens+outputTokens,
		durationMS, status,
	)
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to imports if needed (pgx/v5 already in go.mod).

- [ ] **Step 4: Wire forwardSSE into the proxy ServeHTTP**

Find where the gateway copies the backend response body to the client. It likely looks like:
```go
io.Copy(w, backendResp.Body)
```

Replace with:
```go
start := time.Now()
inputTokens, outputTokens := forwardSSE(w, backendResp.Body)
durationMS := int(time.Since(start).Milliseconds())

// Write metrics asynchronously — never block the response
go writeInferenceEvent(
    p.DB,           // *pgxpool.Pool — add to Config struct below
    orgID,          // from the validated API key lookup
    keyID,          // from the validated API key lookup
    deploymentID,   // the model/deployment name from the route
    model,          // from the request body's "model" field
    r.Header.Get("X-Request-ID"),
    inputTokens, outputTokens,
    durationMS, backendResp.StatusCode,
)
```

You will need to extract `orgID`, `keyID`, and `model` from the existing request context. Check how the validator stores these — they are likely set in context during API key validation. If `model` is not in context, decode the request body before proxying (it's already being decoded for peer routing).

- [ ] **Step 5: Add DB to proxy Config struct**

Find the `Config` struct in proxy.go. Add:
```go
DB *pgxpool.Pool  // used for writing inference_events
```

- [ ] **Step 6: Wire DB in gateway main.go / entry point**

Find where `proxy.Config` is constructed in the gateway's cmd/main.go (or equivalent). Add:
```go
DB: pool,  // the same pgxpool.Pool used by the auth validator
```

- [ ] **Step 7: Build**

```bash
cd /path/to/repo/apps/gateway && go build -o /tmp/gateway-bin ./cmd/... 2>&1
```

Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add apps/gateway/internal/proxy/proxy.go
git commit -m "feat(gateway): intercept SSE usage chunk to record real token counts in inference_events"
```

---

## Task 3: Update GetUsage handler to query inference_events

**Files:**
- Modify: `apps/control-api/internal/handler/handler.go`

- [ ] **Step 1: Replace the GetUsage handler body**

Find `func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request)` and replace its body:

```go
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	if err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"dailyRequests":  []interface{}{},
			"totalRequests":  0,
			"totalThisMonth": 0,
			"totalInputTokens":  0,
			"totalOutputTokens": 0,
		})
		return
	}

	// Parse optional time range: ?range=7d|30d (default 30d)
	rangeStr := r.URL.Query().Get("range")
	interval := "30 days"
	if rangeStr == "7d" {
		interval = "7 days"
	} else if rangeStr == "today" {
		interval = "1 day"
	}

	// Daily breakdown from inference_events
	rows, err := h.DB.Query(r.Context(),
		`SELECT
		    DATE(created_at) AS day,
		    COUNT(*) AS requests,
		    COALESCE(SUM(input_tokens), 0) AS input_tokens,
		    COALESCE(SUM(output_tokens), 0) AS output_tokens
		 FROM inference_events
		 WHERE org_id = $1
		   AND created_at >= NOW() - INTERVAL '`+interval+`'
		 GROUP BY day
		 ORDER BY day ASC`,
		orgID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	type DayMetrics struct {
		Date         string `json:"date"`
		Requests     int64  `json:"requests"`
		InputTokens  int64  `json:"inputTokens"`
		OutputTokens int64  `json:"outputTokens"`
	}
	var daily []DayMetrics
	var totalRequests, totalInput, totalOutput int64
	for rows.Next() {
		var day time.Time
		var dm DayMetrics
		if err := rows.Scan(&day, &dm.Requests, &dm.InputTokens, &dm.OutputTokens); err != nil {
			continue
		}
		dm.Date = day.Format("2006-01-02")
		daily = append(daily, dm)
		totalRequests += dm.Requests
		totalInput += dm.InputTokens
		totalOutput += dm.OutputTokens
	}
	if daily == nil {
		daily = []DayMetrics{}
	}

	// Month totals
	var thisMonthRequests, thisMonthInput, thisMonthOutput int64
	_ = h.DB.QueryRow(r.Context(),
		`SELECT
		    COUNT(*),
		    COALESCE(SUM(input_tokens), 0),
		    COALESCE(SUM(output_tokens), 0)
		 FROM inference_events
		 WHERE org_id = $1
		   AND DATE_TRUNC('month', created_at) = DATE_TRUNC('month', NOW())`,
		orgID,
	).Scan(&thisMonthRequests, &thisMonthInput, &thisMonthOutput)

	// Top models this period
	modelRows, _ := h.DB.Query(r.Context(),
		`SELECT model, COUNT(*) AS requests, COALESCE(SUM(total_tokens), 0) AS tokens
		 FROM inference_events
		 WHERE org_id = $1 AND created_at >= NOW() - INTERVAL '`+interval+`'
		 GROUP BY model ORDER BY requests DESC LIMIT 10`,
		orgID,
	)
	type ModelStat struct {
		Model    string `json:"model"`
		Requests int64  `json:"requests"`
		Tokens   int64  `json:"tokens"`
	}
	var topModels []ModelStat
	if modelRows != nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var ms ModelStat
			_ = modelRows.Scan(&ms.Model, &ms.Requests, &ms.Tokens)
			topModels = append(topModels, ms)
		}
	}
	if topModels == nil {
		topModels = []ModelStat{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"dailyMetrics":       daily,
		"totalRequests":      totalRequests,
		"totalThisMonth":     thisMonthRequests,
		"totalInputTokens":   totalInput,
		"totalOutputTokens":  totalOutput,
		"monthInputTokens":   thisMonthInput,
		"monthOutputTokens":  thisMonthOutput,
		"topModels":          topModels,
	})
}
```

**Note on the INTERVAL injection:** The `interval` string is only ever one of three hardcoded values (`"30 days"`, `"7 days"`, `"1 day"`) — never user input — so this string interpolation is safe. Add a comment to this effect.

- [ ] **Step 2: Build**

```bash
cd /path/to/repo/apps/control-api && go build -o /tmp/control-api-bin ./cmd/... 2>&1
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add apps/control-api/internal/handler/handler.go
git commit -m "feat(usage): query inference_events for real token metrics in GetUsage"
```

---

## Task 4: Update GUI — UsageResponse type and usage page

**Files:**
- Modify: `apps/gui/lib/api.ts`
- Modify: `apps/gui/app/(main)/usage/page.tsx`

- [ ] **Step 1: Update UsageResponse interface in api.ts**

Find and replace the `UsageResponse` interface:

```typescript
export interface DayMetrics {
  date: string;
  requests: number;
  inputTokens: number;
  outputTokens: number;
}

export interface ModelStat {
  model: string;
  requests: number;
  tokens: number;
}

export interface UsageResponse {
  dailyMetrics: DayMetrics[];
  totalRequests: number;
  totalThisMonth: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  monthInputTokens: number;
  monthOutputTokens: number;
  topModels: ModelStat[];
  // Legacy field kept for backward compat
  dailyRequests?: Array<{ date: string; count: number }>;
  note?: string;
}
```

- [ ] **Step 2: Update getUsage to accept range param**

Find `getUsage()` and update:
```typescript
export async function getUsage(range: "today" | "7d" | "30d" = "30d"): Promise<UsageResponse> {
  return fetchAPI<UsageResponse>(`/api/v1/usage?range=${range}`);
}
```

- [ ] **Step 3: Rewrite usage/page.tsx**

Replace the full content of `apps/gui/app/(main)/usage/page.tsx`:

```tsx
"use client";

import { useEffect, useState } from "react";
import { getUsage, type UsageResponse } from "@/lib/api";

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

export default function UsagePage() {
  const [timeRange, setTimeRange] = useState<"today" | "7d" | "30d">("30d");
  const [usageData, setUsageData] = useState<UsageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    getUsage(timeRange)
      .then(setUsageData)
      .catch((err) => setError(err.message || "Failed to fetch usage"))
      .finally(() => setLoading(false));
  }, [timeRange]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-foreground">Usage & Spend</h1>
          <p className="mt-1 text-muted-foreground">
            Real inference metrics across your organization
          </p>
        </div>
        <div className="flex rounded-lg border border-border overflow-hidden">
          {(["today", "7d", "30d"] as const).map((r) => (
            <button
              key={r}
              onClick={() => setTimeRange(r)}
              className={`px-3 py-1.5 text-sm font-medium transition-colors ${
                timeRange === r
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted"
              }`}
            >
              {r === "today" ? "Today" : r === "7d" ? "7 Days" : "30 Days"}
            </button>
          ))}
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-900 dark:bg-red-950">
          <p className="text-sm text-red-800 dark:text-red-200">Error: {error}</p>
        </div>
      )}

      {loading ? (
        <div className="rounded-lg border border-border bg-card p-12 text-center">
          <p className="text-muted-foreground">Loading usage data…</p>
        </div>
      ) : usageData ? (
        <>
          {/* Summary cards */}
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            {[
              { label: "Total Requests", value: formatNumber(usageData.totalRequests) },
              { label: "Requests This Month", value: formatNumber(usageData.totalThisMonth) },
              { label: "Input Tokens", value: formatNumber(usageData.totalInputTokens) },
              { label: "Output Tokens", value: formatNumber(usageData.totalOutputTokens) },
            ].map((card) => (
              <div key={card.label} className="rounded-lg border border-border bg-card p-4">
                <p className="text-xs text-muted-foreground uppercase tracking-wide">{card.label}</p>
                <p className="mt-1 text-2xl font-bold text-foreground">{card.value}</p>
              </div>
            ))}
          </div>

          {/* Daily breakdown */}
          {usageData.dailyMetrics.length > 0 ? (
            <div className="rounded-lg border border-border bg-card p-4">
              <h2 className="text-sm font-semibold text-foreground mb-4">Daily Requests</h2>
              <div className="space-y-1">
                {usageData.dailyMetrics.map((day) => {
                  const max = Math.max(...usageData.dailyMetrics.map((d) => d.requests), 1);
                  const pct = (day.requests / max) * 100;
                  return (
                    <div key={day.date} className="flex items-center gap-3 text-sm">
                      <span className="w-24 shrink-0 text-muted-foreground">{day.date}</span>
                      <div className="flex-1 bg-muted rounded-full h-2">
                        <div
                          className="bg-primary h-2 rounded-full transition-all"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                      <span className="w-16 text-right text-foreground font-medium">
                        {formatNumber(day.requests)}
                      </span>
                      <span className="w-24 text-right text-xs text-muted-foreground">
                        {formatNumber(day.inputTokens + day.outputTokens)} tok
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          ) : (
            <div className="rounded-lg border border-border bg-card p-12 text-center">
              <p className="text-muted-foreground">No inference events yet in this period.</p>
              <p className="mt-1 text-sm text-muted-foreground">
                Data appears here after API keys are used to call deployed models.
              </p>
            </div>
          )}

          {/* Top models */}
          {usageData.topModels.length > 0 && (
            <div className="rounded-lg border border-border bg-card p-4">
              <h2 className="text-sm font-semibold text-foreground mb-3">Top Models</h2>
              <div className="space-y-2">
                {usageData.topModels.map((m) => (
                  <div key={m.model} className="flex items-center justify-between text-sm">
                    <span className="font-medium text-foreground truncate max-w-xs">{m.model}</span>
                    <div className="flex gap-6 text-muted-foreground shrink-0">
                      <span>{formatNumber(m.requests)} req</span>
                      <span>{formatNumber(m.tokens)} tok</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      ) : null}
    </div>
  );
}
```

- [ ] **Step 4: Typecheck**

```bash
cd /path/to/repo/apps/gui && pnpm typecheck 2>&1
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add apps/gui/lib/api.ts apps/gui/app/\(main\)/usage/page.tsx
git commit -m "feat(usage): show real token counts and top models from inference_events"
```

---

## Self-Review Checklist

- [x] DB migration uses `CREATE TABLE IF NOT EXISTS` — safe to replay
- [x] `writeInferenceEvent` runs in a goroutine — never blocks HTTP response
- [x] `context.WithTimeout(5s)` on the DB write — won't hang if DB is slow
- [x] `forwardSSE` uses bufio.Scanner with 64KB buffer — handles large chunks
- [x] INTERVAL string interpolation uses only hardcoded values — not user input
- [x] `formatNumber` helper makes large numbers readable (1.2M, 45.3K)
- [x] Empty state shown when no inference events yet
- [x] `topModels` nil guard in Go handler (`if topModels == nil { topModels = []ModelStat{} }`)
- [x] All Scan() calls include correct column order matching the SELECT
- [x] No TBDs — every code block is complete
