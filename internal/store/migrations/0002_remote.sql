-- Remote access (Tailscale today, other providers later).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Exposures LPS has applied on the provider. LPS only removes what is listed
-- here, so the user's own provider configuration is never touched.
CREATE TABLE exposures (
    proto      TEXT NOT NULL,
    port       INTEGER NOT NULL,
    local_port INTEGER NOT NULL,
    public     INTEGER NOT NULL DEFAULT 0,
    applied_at TIMESTAMP NOT NULL,
    PRIMARY KEY (proto, port)
);

-- Public (Funnel) port for an app; 0 = no public link.
ALTER TABLE apps ADD COLUMN public_port INTEGER NOT NULL DEFAULT 0;
