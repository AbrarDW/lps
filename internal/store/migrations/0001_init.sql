-- Keep SQL portable (SQLite now, PostgreSQL later): plain types. AUTOINCREMENT
-- (never reuse IDs) maps to SERIAL/IDENTITY in a future PostgreSQL migration set.
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMP NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

-- A "VPS" is a long-lived Ubuntu container you SSH into.
CREATE TABLE vps (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    ssh_user       TEXT NOT NULL,
    ssh_port       INTEGER NOT NULL UNIQUE,
    ssh_public_key TEXT NOT NULL DEFAULT '',
    cpus           REAL NOT NULL,
    memory_mb      INTEGER NOT NULL,
    container_id   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL
);

-- An "App" is a directory with a Dockerfile, built and run as a container.
CREATE TABLE apps (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    source_path    TEXT NOT NULL,
    container_port INTEGER NOT NULL,
    host_port      INTEGER NOT NULL UNIQUE,
    cpus           REAL NOT NULL,
    memory_mb      INTEGER NOT NULL,
    container_id   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL
);

CREATE TABLE deployments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id      INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status      TEXT NOT NULL, -- building | running | failed
    image       TEXT NOT NULL DEFAULT '',
    log         TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMP NOT NULL,
    finished_at TIMESTAMP
);
CREATE INDEX deployments_app_id ON deployments(app_id);
