-- 011_add_missing_indexes.sql
-- Adds missing B-tree indexes found in audit, a missing FK constraint,
-- and a missing updated_at column on deployment_cache.

-- 1. knowledge_chunks needs org_id index for multi-tenant WHERE org_id = $1 scans
--    (retrieve.go filters by org_id on every RAG query)
CREATE INDEX IF NOT EXISTS knowledge_chunks_org_id_idx
    ON knowledge_chunks(org_id);

-- 2. deployment_cache needs org_id index for the org-scoped list queries
CREATE INDEX IF NOT EXISTS deployment_cache_org_id_idx
    ON deployment_cache(org_id);

-- 3. prompt_templates: add composite index for (org_id, id) lookups used in handlers
CREATE INDEX IF NOT EXISTS prompt_templates_org_id_id_idx
    ON prompt_templates(org_id, id);

-- 4. conversations: add org-level index via member join shortcut
--    conversation queries often filter by member_id (already indexed via PK?)
--    Add explicit index on conversations.member_id for org-level scans
CREATE INDEX IF NOT EXISTS conversations_member_id_idx
    ON conversations(member_id);

-- 5. Add updated_at to deployment_cache (was missing, needed for cache invalidation)
ALTER TABLE deployment_cache ADD COLUMN IF NOT EXISTS
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- 6. Add FK constraint on knowledge_chunks.org_id (safe — existing data is consistent)
--    PostgreSQL does not support ADD CONSTRAINT IF NOT EXISTS; use DO block instead.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'knowledge_chunks_org_id_fkey'
          AND table_name = 'knowledge_chunks'
    ) THEN
        ALTER TABLE knowledge_chunks
            ADD CONSTRAINT knowledge_chunks_org_id_fkey
            FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE;
    END IF;
END $$;
