-- Two-factor sign-in (TOTP). totp_secret = '' means 2FA is off; totp_pending
-- holds a secret being set up; totp_last is the last accepted time step, so
-- a code can't be used twice.
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_pending TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_last INTEGER NOT NULL DEFAULT 0;

CREATE TABLE recovery_codes (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at   TIMESTAMP,
    PRIMARY KEY (user_id, code_hash)
);
