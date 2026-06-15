-- Credential-test source: why each credential was tried — 'subnet' (subnet-scoped),
-- 'default' (normal resolution), 'manual' (operator-selected on a device collect).
-- Lets scan results + credential history report subnet-scoped vs sprayed attempts.
ALTER TABLE credential_test_results
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'default';
