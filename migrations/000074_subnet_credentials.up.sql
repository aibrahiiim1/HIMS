-- Subnet-scoped credentials: when a site subnet has assigned credentials, scans
-- of IPs inside it use ONLY those (no global spray / lockout risk). Empty = the
-- subnet falls back to normal/default credential resolution.
CREATE TABLE IF NOT EXISTS subnet_credentials (
    subnet_id     uuid NOT NULL REFERENCES subnets(id) ON DELETE CASCADE,
    credential_id uuid NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subnet_id, credential_id)
);
CREATE INDEX IF NOT EXISTS idx_subnet_credentials_subnet ON subnet_credentials(subnet_id);
