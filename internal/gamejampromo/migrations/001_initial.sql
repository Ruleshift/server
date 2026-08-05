CREATE TABLE gamejam_candidates (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 64),
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 512),
    source_url TEXT NOT NULL CHECK (length(source_url) BETWEEN 1 AND 2048),
    official_url TEXT NOT NULL DEFAULT '' CHECK (length(official_url) <= 2048),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    organizer TEXT NOT NULL DEFAULT '' CHECK (length(organizer) <= 300),
    format TEXT NOT NULL CHECK (format IN ('online', 'offline', 'hybrid', 'unknown')),
    city TEXT NOT NULL DEFAULT '' CHECK (length(city) <= 200),
    country_code TEXT NOT NULL DEFAULT '' CHECK (length(country_code) <= 2),
    languages JSONB NOT NULL DEFAULT '[]'::jsonb,
    starts_on DATE NOT NULL,
    ends_on DATE NOT NULL,
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4096),
    relevance TEXT NOT NULL CHECK (relevance IN ('likely_ru', 'unknown', 'unlikely_ru')),
    relevance_notes TEXT NOT NULL DEFAULT '' CHECK (length(relevance_notes) <= 512),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'archived')),
    rejection_reason TEXT NOT NULL DEFAULT '' CHECK (length(rejection_reason) <= 512),
    source_digest TEXT NOT NULL,
    reviewed_digest TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    missing_runs INTEGER NOT NULL DEFAULT 0 CHECK (missing_runs >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_on >= starts_on),
    UNIQUE (source, external_id)
);

CREATE INDEX gamejam_candidates_review_idx ON gamejam_candidates(status, relevance, starts_on);
CREATE INDEX gamejam_candidates_last_seen_idx ON gamejam_candidates(source, last_seen_at);

CREATE TABLE game_jams (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    organizer TEXT NOT NULL DEFAULT '' CHECK (length(organizer) <= 300),
    format TEXT NOT NULL CHECK (format IN ('online', 'offline', 'hybrid', 'unknown')),
    city TEXT NOT NULL DEFAULT '' CHECK (length(city) <= 200),
    country_code TEXT NOT NULL DEFAULT '' CHECK (length(country_code) <= 2),
    languages JSONB NOT NULL DEFAULT '[]'::jsonb,
    starts_on DATE NOT NULL,
    ends_on DATE NOT NULL,
    eligibility_reason TEXT NOT NULL CHECK (eligibility_reason IN ('venue_ru', 'language_ru', 'audience_ru', 'organizer_ru')),
    status TEXT NOT NULL DEFAULT 'approved' CHECK (status IN ('approved', 'disabled', 'ended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_on >= starts_on)
);

CREATE INDEX game_jams_active_idx ON game_jams(status, starts_on, ends_on);

CREATE TABLE gamejam_candidate_links (
    candidate_id TEXT PRIMARY KEY REFERENCES gamejam_candidates(id) ON DELETE RESTRICT,
    game_jam_id TEXT NOT NULL REFERENCES game_jams(id) ON DELETE RESTRICT,
    linked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX gamejam_candidate_links_game_jam_idx ON gamejam_candidate_links(game_jam_id);

CREATE TABLE promotion_codes (
    id BIGSERIAL PRIMARY KEY,
    game_jam_id TEXT NOT NULL REFERENCES game_jams(id) ON DELETE RESTRICT,
    lookup_hmac BYTEA NOT NULL UNIQUE CHECK (octet_length(lookup_hmac) = 32),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) = 26),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    last_four TEXT NOT NULL CHECK (last_four ~ '^[0-9]{4}$'),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX promotion_codes_one_active_idx ON promotion_codes(game_jam_id) WHERE revoked_at IS NULL;

CREATE TABLE discovery_runs (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 64),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'error', 'busy')),
    found_count INTEGER NOT NULL DEFAULT 0 CHECK (found_count >= 0),
    message TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1024)
);

CREATE INDEX discovery_runs_source_started_idx ON discovery_runs(source, started_at DESC);
