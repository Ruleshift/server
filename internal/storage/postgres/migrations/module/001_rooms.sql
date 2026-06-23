CREATE TABLE rooms (
    id TEXT PRIMARY KEY,
    game_type SMALLINT NOT NULL,
    revision NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (revision >= 0),
    lifecycle_status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE room_players (
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    player_id TEXT NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL,
    last_disconnected_at TIMESTAMPTZ,
    last_disconnect_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (room_id, player_id)
);

CREATE INDEX room_players_player_id_idx ON room_players(player_id);

CREATE TABLE room_events (
    sequence BIGSERIAL PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    player_id TEXT NOT NULL DEFAULT '',
    revision NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (revision >= 0),
    previous_revision NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (previous_revision >= 0),
    new_revision NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (new_revision >= 0),
    game_type SMALLINT NOT NULL,
    command_type SMALLINT NOT NULL DEFAULT 0,
    command_payload JSONB,
    state_hash NUMERIC(20, 0) NOT NULL DEFAULT 0 CHECK (state_hash >= 0),
    game_status SMALLINT NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX room_events_room_sequence_idx ON room_events(room_id, sequence);
CREATE UNIQUE INDEX room_events_one_created_per_room_idx
    ON room_events(room_id) WHERE event_type = 'RoomCreated';
