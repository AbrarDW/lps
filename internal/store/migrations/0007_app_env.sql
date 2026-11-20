-- Environment variables for apps. Secret values are never returned by the API.
CREATE TABLE app_env (
    app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name   TEXT NOT NULL,
    value  TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    pos    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, name)
);
