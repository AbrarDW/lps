-- Activity feed: what happened, when, and who did it.
CREATE TABLE events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    at        TIMESTAMP NOT NULL,
    level     TEXT NOT NULL,             -- info | warning | error
    kind      TEXT NOT NULL,             -- e.g. app.deployed, vps.started, auth.login_failed
    subject   TEXT NOT NULL DEFAULT '',  -- vps | app | remote | auth | system
    subject_id INTEGER NOT NULL DEFAULT 0,
    name      TEXT NOT NULL DEFAULT '',  -- the VPS/app name at the time (kept after deletes)
    actor     TEXT NOT NULL DEFAULT '',  -- username, or '' for LPS itself
    ip        TEXT NOT NULL DEFAULT '',
    message   TEXT NOT NULL
);
CREATE INDEX events_subject ON events (subject, subject_id, id);
