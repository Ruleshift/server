CREATE TABLE developer_api_keys (
    id TEXT PRIMARY KEY,
    developer_id TEXT NOT NULL REFERENCES developers(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL,
    key_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX developer_api_keys_developer_id_idx ON developer_api_keys(developer_id);
