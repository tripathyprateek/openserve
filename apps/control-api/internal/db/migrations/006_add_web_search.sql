-- 006_add_web_search.sql
-- Adds optional web search grounding capability per deployment.

ALTER TABLE deployment_cache
    ADD COLUMN IF NOT EXISTS web_search_enabled BOOLEAN NOT NULL DEFAULT false;
