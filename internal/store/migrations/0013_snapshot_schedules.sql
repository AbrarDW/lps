-- Snapshots taken by a schedule; only these are deleted automatically.
ALTER TABLE vps_snapshots ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;

-- Automatic snapshots of a VPS, in the PC's local time.
CREATE TABLE snapshot_schedules (
    vps_id     INTEGER PRIMARY KEY REFERENCES vps(id) ON DELETE CASCADE,
    frequency  TEXT NOT NULL,              -- daily | weekly
    hour       INTEGER NOT NULL,           -- 0-23
    weekday    INTEGER NOT NULL DEFAULT 0, -- 0 = Sunday (weekly)
    keep       INTEGER NOT NULL,           -- automatic snapshots kept
    last_run   TIMESTAMP,                  -- the slot last handled
    created_at TIMESTAMP NOT NULL
);
