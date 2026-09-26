-- 0010_t1_prizes.sql: T1 configurable prizes schema
-- Minimal, forward-safe persistence for hackathon prize configuration without altering 0001_t1_core.sql checksum

CREATE TABLE prizes (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    track_id TEXT REFERENCES tracks(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    amount_text TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX idx_prizes_event ON prizes(event_id);
CREATE INDEX idx_prizes_track ON prizes(track_id);
