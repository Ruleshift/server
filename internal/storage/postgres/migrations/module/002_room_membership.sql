ALTER TABLE rooms
    ADD COLUMN required_players INTEGER NOT NULL DEFAULT 1
    CHECK (required_players BETWEEN 1 AND 64);

CREATE TABLE room_participants (
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    player_id TEXT NOT NULL,
    seat_index INTEGER NOT NULL CHECK (seat_index BETWEEN 0 AND 63),
    joined_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (room_id, player_id),
    UNIQUE (room_id, seat_index)
);
