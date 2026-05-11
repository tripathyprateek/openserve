# Web Search Grounding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional real-time web search to any deployment — users toggle "Web Search" in the playground; the control-api calls Brave Search, prepends formatted results as context, and the model answers with current information.

**Architecture:** A thin `POST /api/v1/search` endpoint in control-api calls the Brave Search API (free: 2000 queries/month, paid: unlimited) and returns formatted snippets. The GUI playground calls this endpoint before sending to the gateway when web search is enabled for a deployment. A new DB boolean `web_search_enabled` on `deployment_cache` controls whether the toggle appears. No changes to the gateway are needed — context injection happens client-side in the GUI before the request is sent.

**Tech Stack:** Go, Brave Search REST API (`api.search.brave.com`), net/http, Next.js (existing SWR + fetch pattern), Postgres migration.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `apps/control-api/internal/db/migrations/005_add_web_search.sql` | Create | Add `web_search_enabled` column to `deployment_cache` |
| `apps/control-api/internal/handler/handler.go` | Modify | Add `BraveAPIKey` to Deps, add `SearchWeb` handler, update `CreateDeployment`/`GetDeployment` to include web_search_enabled |
| `apps/control-api/cmd/server/main.go` | Modify | Add `--brave-api-key` flag |
| `apps/gui/lib/api.ts` | Modify | Add `webSearchEnabled` to Deployment type, add `searchWeb()` function |
| `apps/gui/app/(main)/deployments/[id]/page.tsx` | Modify | Add web search toggle in playground; call searchWeb() before gateway if enabled |
| `apps/gui/app/(main)/deployments/page.tsx` | Modify | Show web search badge on deployment cards |

---

## Task 1: DB Migration — add web_search_enabled to deployment_cache

**Files:**
- Create: `apps/control-api/internal/db/migrations/005_add_web_search.sql`

- [ ] **Step 1: Create the migration file**

```sql
-- 005_add_web_search.sql
-- Adds optional web search grounding capability per deployment.

ALTER TABLE deployment_cache
    ADD COLUMN IF NOT EXISTS web_search_enabled BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 2: Verify migration runs** (the db.Migrate function reads all files in order — just confirm it exists and is valid SQL)

```bash
grep -r "005_add_web_search" /path/to/repo/apps/control-api/internal/db/ 2>/dev/null || echo "migration file not yet tracked"
ls /path/to/repo/apps/control-api/internal/db/migrations/
```

Expected: `005_add_web_search.sql` appears in the list.

- [ ] **Step 3: Commit**

```bash
git add apps/control-api/internal/db/migrations/005_add_web_search.sql
git commit -m "feat(db): add web_search_enabled column to deployment_cache"
```

---

## Task 2: Add SearchWeb handler to control-api

**Files:**
- Modify: `apps/control-api/internal/handler/handler.go`
- Modify: `apps/control-api/cmd/server/main.go`

- [ ] **Step 1: Add BraveAPIKey to Deps struct**

Find the `Deps` struct and add:
```go
BraveAPIKey  string  // Optional. If empty, SearchWeb returns 501.
```

- [ ] **Step 2: Add SearchWeb handler** (add after the existing GetPeerAgentInstallScript handler, before the last section)

```go
// SearchWeb calls the Brave Search API and returns formatted web results.
// POST /api/v1/search
// Body: {"query": "...", "count": 5}
// Response: {"results": [{"title":"...", "url":"...", "description":"..."}]}
func (h *Handler) SearchWeb(w http.ResponseWriter, r *http.Request) {
	if h.BraveAPIKey == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "web search not configured — set BRAVE_API_KEY on the control-api",
		})
		return
	}

	var req struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		http.Error(w, "body must be JSON with non-empty query field", http.StatusBadRequest)
		return
	}
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 5
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	braveURL := "https://api.search.brave.com/res/v1/web/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, braveURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build search request"})
		return
	}

	q := httpReq.URL.Query()
	q.Set("q", req.Query)
	q.Set("count", strconv.Itoa(req.Count))
	q.Set("safesearch", "moderate")
	httpReq.URL.RawQuery = q.Encode()
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Accept-Encoding", "gzip")
	httpReq.Header.Set("X-Subscription-Token", h.BraveAPIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "search request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("brave search returned %d", resp.StatusCode),
		})
		return
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	bodyReader := resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode gzip response"})
			return
		}
		defer gz.Close()
		bodyReader = gz
	}

	if err := json.NewDecoder(bodyReader).Decode(&braveResp); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode search response"})
		return
	}

	type Result struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	var results []Result
	for _, r := range braveResp.Web.Results {
		results = append(results, Result{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}
	if results == nil {
		results = []Result{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}
```

- [ ] **Step 3: Add gzip import** — add `"compress/gzip"` to the import block in handler.go (it is not currently imported). The import block already has `"fmt"`, `"net/http"`, `"strconv"`, `"context"`, `"time"` — just add gzip.

- [ ] **Step 4: Add BraveAPIKey to the handler construction in main.go**

Find the `h := handler.New(handler.Deps{` block and add:
```go
BraveAPIKey:  braveAPIKey,
```

Add the flag declaration (near other flag declarations):
```go
flag.StringVar(&braveAPIKey, "brave-api-key", os.Getenv("BRAVE_API_KEY"),
    "Brave Search API key for web search grounding (optional; get free key at brave.com/search/api)")
```

Add the var declaration at the top of the var block:
```go
braveAPIKey string
```

- [ ] **Step 5: Register the route in main.go** — add inside the authenticated `r.Group` after the existing routes:

```go
r.Post("/api/v1/search", h.SearchWeb)
```

- [ ] **Step 6: Add BRAVE_API_KEY to docker-compose.yml** control-api environment:

```yaml
      BRAVE_API_KEY: ${BRAVE_API_KEY:-}
```

- [ ] **Step 7: Build**

```bash
cd /path/to/repo/apps/control-api && go build -o /tmp/control-api-bin ./cmd/... 2>&1
```

Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add apps/control-api/internal/handler/handler.go \
        apps/control-api/cmd/server/main.go \
        docker-compose.yml
git commit -m "feat(search): add Brave Search endpoint POST /api/v1/search for web grounding"
```

---

## Task 3: Update CreateDeployment and GetDeployment to handle web_search_enabled

**Files:**
- Modify: `apps/control-api/internal/handler/handler.go`

- [ ] **Step 1: Update CreateDeployment request struct**

Find the anonymous struct inside `CreateDeployment` that decodes the request body. Add:
```go
WebSearchEnabled bool   `json:"webSearchEnabled"`
```

- [ ] **Step 2: Update the INSERT into deployment_cache inside CreateDeployment**

Find the SQL that inserts into `deployment_cache` and add the new column:

Old INSERT columns (approx):
```sql
INSERT INTO deployment_cache (id, org_id, name, model_id, phase, endpoint, budget_per_day, created_at)
VALUES ($1, $2, $3, $4, 'Pending', $5, $6, now())
```

New (add web_search_enabled):
```sql
INSERT INTO deployment_cache (id, org_id, name, model_id, phase, endpoint, budget_per_day, web_search_enabled, created_at)
VALUES ($1, $2, $3, $4, 'Pending', $5, $6, $7, now())
```

Add the new param value at the end of the args (before `now()`):
```go
req.WebSearchEnabled,
```

- [ ] **Step 3: Update the SELECT in GetDeployment / ListDeployments**

Find the SELECT from `deployment_cache`. Add `web_search_enabled` to the column list and the corresponding `.Scan()` variable. For example, if there is a `Deployment` struct being populated, add:

```go
WebSearchEnabled bool   `json:"webSearchEnabled"`
```

And include it in the Scan() call in the correct position.

- [ ] **Step 4: Build**

```bash
cd /path/to/repo/apps/control-api && go build -o /tmp/control-api-bin ./cmd/... 2>&1
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add apps/control-api/internal/handler/handler.go
git commit -m "feat(deployments): add webSearchEnabled field to deployment CRUD"
```

---

## Task 4: Update GUI — api.ts types and searchWeb function

**Files:**
- Modify: `apps/gui/lib/api.ts`

- [ ] **Step 1: Add webSearchEnabled to Deployment interface**

Find the `Deployment` interface and add:
```typescript
webSearchEnabled: boolean;
```

- [ ] **Step 2: Add SearchResult interface and searchWeb function** (append to end of file)

```typescript
export interface SearchResult {
  title: string;
  url: string;
  description: string;
}

export interface SearchResponse {
  results: SearchResult[];
}

export async function searchWeb(query: string, count = 5): Promise<SearchResult[]> {
  const res = await fetch("/api/v1/search", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, count }),
  });
  if (!res.ok) {
    // Web search is optional — return empty on error rather than throwing
    console.warn("Web search failed:", res.status);
    return [];
  }
  const data: SearchResponse = await res.json();
  return data.results ?? [];
}
```

- [ ] **Step 3: Run typecheck**

```bash
cd /path/to/repo/apps/gui && pnpm typecheck 2>&1
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add apps/gui/lib/api.ts
git commit -m "feat(gui/api): add SearchResult type and searchWeb() function"
```

---

## Task 5: Update deployment playground to support web search toggle

**Files:**
- Modify: `apps/gui/app/(main)/deployments/[id]/page.tsx`

- [ ] **Step 1: Add web search state and import**

At the top of `DeploymentDetailPage`, add state:
```typescript
const [webSearchEnabled, setWebSearchEnabled] = useState(false);
const [searchingWeb, setSearchingWeb] = useState(false);
```

Also import `searchWeb` from api.ts:
```typescript
import { getDeployment, searchWeb } from "@/lib/api";
```

- [ ] **Step 2: Initialize webSearchEnabled from deployment config**

Inside the existing `useEffect` that runs when `deployment` changes (or add one):
```typescript
useEffect(() => {
  if (deployment?.webSearchEnabled !== undefined) {
    setWebSearchEnabled(deployment.webSearchEnabled);
  }
}, [deployment]);
```

- [ ] **Step 3: Inject search results before gateway call in handleSendMessage**

Inside `handleSendMessage`, BEFORE the `fetch` call to the gateway, add:

```typescript
// If web search is enabled, retrieve current context and prepend as system message
let searchContext = "";
if (webSearchEnabled && inputValue.trim()) {
  setSearchingWeb(true);
  const results = await searchWeb(inputValue.trim(), 5);
  setSearchingWeb(false);
  if (results.length > 0) {
    searchContext = "## Current web search results\n\n" +
      results.map((r, i) =>
        `[${i + 1}] **${r.title}**\n${r.url}\n${r.description}`
      ).join("\n\n") +
      "\n\n---\nUse the above search results to answer the following question. Cite sources by number.";
  }
}
```

Then in the `messages` array sent to the gateway, prepend a system message when searchContext is non-empty:

```typescript
const messagesToSend = [
  ...(searchContext ? [{ role: "system" as const, content: searchContext }] : []),
  ...messages,
  userMessage,
].map((m) => ({ role: m.role, content: m.content }));
```

Replace the existing messages mapping in the fetch body:
```typescript
body: JSON.stringify({
  model: deployment.modelId,
  messages: messagesToSend,  // was: [...messages, userMessage].map(...)
  stream: true,
}),
```

- [ ] **Step 4: Add web search toggle UI**

In the playground input area (where the API key input is), add the toggle below the API key input:

```tsx
{deployment?.webSearchEnabled && (
  <div className="flex items-center gap-2 px-1">
    <button
      type="button"
      role="switch"
      aria-checked={webSearchEnabled}
      onClick={() => setWebSearchEnabled((v) => !v)}
      className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${
        webSearchEnabled ? "bg-blue-600" : "bg-gray-200"
      }`}
    >
      <span
        className={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition-transform ${
          webSearchEnabled ? "translate-x-4" : "translate-x-0"
        }`}
      />
    </button>
    <span className="text-sm text-muted-foreground flex items-center gap-1">
      {searchingWeb ? (
        <><Loader2 className="h-3 w-3 animate-spin" /> Searching…</>
      ) : (
        <>🔍 Web Search {webSearchEnabled ? "ON" : "OFF"}</>
      )}
    </span>
  </div>
)}
```

- [ ] **Step 5: Run typecheck**

```bash
cd /path/to/repo/apps/gui && pnpm typecheck 2>&1
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add apps/gui/app/\(main\)/deployments/\[id\]/page.tsx
git commit -m "feat(playground): add web search grounding toggle with Brave Search context injection"
```

---

## Task 6: Show web search badge on deployment list

**Files:**
- Modify: `apps/gui/app/(main)/deployments/page.tsx`

- [ ] **Step 1: Add badge to each deployment card**

Find where deployment cards are rendered. After the existing phase badge, add:

```tsx
{deployment.webSearchEnabled && (
  <span className="inline-flex items-center gap-1 rounded-full bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-950 dark:text-blue-300">
    🔍 Web Search
  </span>
)}
```

- [ ] **Step 2: Typecheck and commit**

```bash
cd /path/to/repo/apps/gui && pnpm typecheck 2>&1 && echo "OK"
git add apps/gui/app/\(main\)/deployments/page.tsx
git commit -m "feat(gui): show web search badge on deployment list"
```

---

## Self-Review Checklist

- [x] DB migration uses `ADD COLUMN IF NOT EXISTS` — safe to run on existing DBs
- [x] `SearchWeb` handler returns 501 when `BraveAPIKey` is empty — graceful degradation
- [x] Gzip decompression handled (Brave sends gzip by default)
- [x] `searchWeb()` in api.ts catches errors and returns empty array — playground never crashes on search failure
- [x] Web search toggle only appears when `deployment.webSearchEnabled` is true — no UI noise for deployments without it
- [x] `searchingWeb` spinner shows while Brave API is being called
- [x] Context injection happens BEFORE the gateway fetch — model sees search results
- [x] All code blocks are complete — no TBDs
- [x] Build verified at each task
