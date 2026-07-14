CREATE TABLE room_invite_codes (
    room_id TEXT PRIMARY KEY REFERENCES room_routes(room_id) ON DELETE CASCADE,
    code TEXT NOT NULL UNIQUE CHECK (code ~ '^[0-9A-Z]{6}$'),
    deadline TIMESTAMPTZ NOT NULL
);

CREATE INDEX room_invite_codes_deadline_idx ON room_invite_codes(deadline);
