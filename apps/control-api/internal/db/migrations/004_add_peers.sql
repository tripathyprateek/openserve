CREATE TABLE IF NOT EXISTS peers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    owner_id    TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    models      TEXT[] NOT NULL DEFAULT '{}',
    online      BOOLEAN NOT NULL DEFAULT false,
    last_seen   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS peer_invites (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    peer_id     UUID NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
    api_key_id  UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(peer_id, api_key_id)
);

CREATE INDEX IF NOT EXISTS peers_org_id_idx ON peers(org_id);
CREATE INDEX IF NOT EXISTS peer_invites_api_key_id_idx ON peer_invites(api_key_id);
