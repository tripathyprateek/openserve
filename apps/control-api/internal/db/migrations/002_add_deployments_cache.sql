-- 002_add_deployments_cache.sql
-- Lightweight cache of ModelDeployment status for the GUI (source of truth is the K8s CR).
CREATE TABLE IF NOT EXISTS deployment_cache (
    id TEXT NOT NULL,                -- matches ModelDeployment name
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    model_ref TEXT NOT NULL,
    gpu_class TEXT NOT NULL,
    phase TEXT NOT NULL DEFAULT 'Pending',
    endpoint TEXT,
    today_usd_spend NUMERIC(10,4) DEFAULT 0,
    budget_paused_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(id, org_id)
);
