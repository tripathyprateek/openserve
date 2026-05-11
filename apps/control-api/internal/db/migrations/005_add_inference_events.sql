-- 005_add_inference_events.sql
-- Stores per-request inference metrics captured by the gateway.
-- The gateway writes one row per completed request (non-blocking goroutine).
-- Prompt content is never stored — only token counts and latency.

CREATE TABLE IF NOT EXISTS inference_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    key_id          UUID NOT NULL,         -- references api_keys(id) but no FK (key may be deleted)
    deployment_id   TEXT NOT NULL,         -- vLLM deployment name or "peer-{id}"
    model           TEXT NOT NULL,
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    request_id      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS inference_events_org_id_created_at_idx
    ON inference_events (org_id, created_at DESC);

CREATE INDEX IF NOT EXISTS inference_events_key_id_idx
    ON inference_events (key_id);
