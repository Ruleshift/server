ALTER TABLE room_routes
    ADD COLUMN player_count INTEGER NOT NULL DEFAULT 1
    CHECK (player_count BETWEEN 1 AND 64);
