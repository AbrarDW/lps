-- Snapshots of a VPS: its system files as an image plus a copy of /home.
CREATE TABLE vps_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    vps_id       INTEGER NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
    label        TEXT NOT NULL,
    status       TEXT NOT NULL,            -- creating | ready | failed
    error        TEXT NOT NULL DEFAULT '',
    image        TEXT NOT NULL DEFAULT '', -- system files (docker commit)
    volume       TEXT NOT NULL DEFAULT '', -- copy of /home
    system_bytes INTEGER NOT NULL DEFAULT 0,
    home_bytes   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL
);

-- The image a VPS runs from after a restore or when made from a snapshot
-- ('' = the stock VPS image). Each VPS gets its own tag, so deleting the
-- snapshot it came from stays possible.
ALTER TABLE vps ADD COLUMN image TEXT NOT NULL DEFAULT '';
