-- Addresses on the user's own domain, served through their Cloudflare Tunnel.
CREATE TABLE app_domains (
    app_id     INTEGER PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    hostname   TEXT NOT NULL UNIQUE,
    zone_id    TEXT NOT NULL,
    record_id  TEXT NOT NULL DEFAULT '', -- the CNAME record LPS created ('' = adopted)
    created_at TIMESTAMP NOT NULL
);
