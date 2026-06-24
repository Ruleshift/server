CREATE TABLE rooms (
    id TEXT PRIMARY KEY,
    module_version_id TEXT NOT NULL,
    revision NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (revision >= 0),
    lifecycle_status TEXT NOT NULL DEFAULT 'active',
    seed NUMERIC(20, 0) NOT NULL CHECK (seed >= 0),
    state_type_url TEXT NOT NULL,
    state_payload BYTEA NOT NULL,
    state_digest BYTEA NOT NULL CHECK (octet_length(state_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE room_events (
    sequence BIGSERIAL PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    event_kind TEXT NOT NULL,
    player_id TEXT NOT NULL DEFAULT '',
    previous_revision NUMERIC(20, 0) NOT NULL CHECK (previous_revision >= 0),
    new_revision NUMERIC(20, 0) NOT NULL CHECK (new_revision >= 0),
    input_type_url TEXT,
    input_payload BYTEA,
    delta_type_url TEXT NOT NULL,
    delta_payload BYTEA NOT NULL,
    state_digest BYTEA NOT NULL CHECK (octet_length(state_digest) = 32),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX room_events_room_sequence_idx ON room_events(room_id, sequence);
CREATE UNIQUE INDEX room_events_one_created_per_room_idx
    ON room_events(room_id) WHERE event_kind = 'room_created';

CREATE TABLE room_snapshots (
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    revision NUMERIC(20, 0) NOT NULL CHECK (revision >= 0),
    state_type_url TEXT NOT NULL,
    state_payload BYTEA NOT NULL,
    state_digest BYTEA NOT NULL CHECK (octet_length(state_digest) = 32),
    saved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (room_id, revision)
);

CREATE INDEX room_snapshots_latest_idx ON room_snapshots(room_id, revision DESC);
