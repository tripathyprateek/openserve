-- 010_add_org_unique_domain.sql
-- Make google_domain the canonical multi-tenancy key for orgs.
-- Previously OIDCCallback used ON CONFLICT (name) but no UNIQUE constraint
-- existed on either column, allowing org-name collision attacks
-- (an admin could rename their org to another domain to hijack first-logins
-- from that domain).

-- Backfill: any orgs missing google_domain get a placeholder derived from name.
-- For existing dev installs, this is best-effort; production has had no users yet.
UPDATE orgs
SET google_domain = LOWER(SPLIT_PART(name, '@', 2))
WHERE google_domain IS NULL OR google_domain = '';

UPDATE orgs
SET google_domain = 'unknown-' || id::text
WHERE google_domain IS NULL OR google_domain = '';

-- Enforce: every org has a domain, and each domain maps to exactly one org.
ALTER TABLE orgs
    ALTER COLUMN google_domain SET NOT NULL;

ALTER TABLE orgs
    ADD CONSTRAINT orgs_google_domain_unique UNIQUE (google_domain);
