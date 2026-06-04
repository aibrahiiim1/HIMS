-- Production auth (#P1): password login + server-side sessions.
-- password_hash is bcrypt; empty means "no password set" (cannot log in).
-- Sessions store only the SHA-256 of the cookie token, so a DB leak does not
-- yield usable sessions; the raw token lives only in the operator's cookie.
ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE sessions (
    token_hash   TEXT PRIMARY KEY,             -- sha256(raw cookie token)
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);
