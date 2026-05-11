-- 001_initial_schema.sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orgs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    google_domain TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    name TEXT,
    role TEXT NOT NULL DEFAULT 'developer' CHECK (role IN ('admin','developer','partner','viewer')),
    invited_by UUID REFERENCES members(id),
    joined_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, email)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES members(id),
    display_name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,        -- first 15 chars of raw key for fast lookup
    key_hash TEXT NOT NULL,          -- argon2id hash of full raw key
    role TEXT NOT NULL DEFAULT 'developer',
    allowed_deployments TEXT[] NOT NULL DEFAULT '{}',
    rpm INTEGER NOT NULL DEFAULT 60,
    tpm INTEGER NOT NULL DEFAULT 100000,
    ip_allowlist TEXT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS api_keys_prefix_idx ON api_keys(key_prefix) WHERE active = true;
CREATE INDEX IF NOT EXISTS api_keys_org_idx ON api_keys(org_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id),
    actor_member_id UUID REFERENCES members(id),
    actor_key_id UUID REFERENCES api_keys(id),
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    details JSONB,
    ip_address INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_org_idx ON audit_log(org_id, created_at DESC);
