CREATE TABLE developers (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE modules (
    id TEXT PRIMARY KEY,
    developer_id TEXT NOT NULL REFERENCES developers(id) ON DELETE RESTRICT,
    module_key TEXT NOT NULL,
    display_name TEXT NOT NULL,
    database_name TEXT NOT NULL UNIQUE,
    active_version TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (developer_id, module_key)
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_authenticated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_identities (
    provider TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL DEFAULT '',
    steam_id TEXT NOT NULL DEFAULT '',
    ownership_verified BOOLEAN NOT NULL DEFAULT FALSE,
    last_authenticated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, provider_user_id)
);

CREATE INDEX user_identities_user_id_idx ON user_identities(user_id);
