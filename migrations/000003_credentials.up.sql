-- HIMS Phase 0: credentials + groups + scope bindings (the resolver model).
--
-- Design goal (a hard lesson from NIMS): the operator NEVER picks a
-- credential per scan / per device. Instead they bind Credential Groups to
-- scopes (a location/site and/or a subnet) once; the resolver derives the
-- ordered candidate list for any device from where it lives + what it is.
--
--   Site (location)  ─┐
--   Subnet           ─┼─► credential_bindings ─► credential_group ─► creds
--
-- Bind-on-success: the device row records the credential that last worked,
-- so subsequent polls try it first.

CREATE TABLE credentials (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    -- Protocol family this credential authenticates. The resolver filters
    -- candidates by the protocol the device fingerprint calls for.
    kind           TEXT NOT NULL
        CHECK (kind IN ('snmp_v2c','snmp_v3','ssh','winrm','http_basic',
                        'onvif','vendor_api','ldap')),
    -- Encrypted at rest (envelope encryption); NEVER logged or returned.
    -- key_id names the wrapping key so rotation is possible.
    encrypted_blob BYTEA NOT NULL,
    key_id         TEXT NOT NULL,
    -- weak = a default/guessable secret (e.g. SNMP "public"); surfaced as a
    -- warning, never auto-bound silently.
    weak           BOOLEAN NOT NULL DEFAULT false,
    metadata       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name)
);

CREATE INDEX idx_credentials_kind ON credentials (kind);

CREATE TABLE credential_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name)
);

-- Members of a group, with an explicit try-order (lower priority first).
CREATE TABLE credential_group_members (
    group_id      UUID NOT NULL REFERENCES credential_groups(id) ON DELETE CASCADE,
    credential_id UUID NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    priority      INTEGER NOT NULL DEFAULT 100,
    PRIMARY KEY (group_id, credential_id)
);

CREATE INDEX idx_credential_group_members_group
    ON credential_group_members (group_id, priority);

-- Binds a credential group to a scope. Exactly one of location_id / subnet_id
-- is the anchor (a subnet binding is more specific than a location binding).
-- A more specific (subnet) binding outranks a broader (location) one in the
-- resolver's ordering.
CREATE TABLE credential_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES credential_groups(id) ON DELETE CASCADE,
    location_id UUID REFERENCES locations(id) ON DELETE CASCADE,
    subnet_id   UUID REFERENCES subnets(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Anchor exactly one scope.
    CHECK ((location_id IS NOT NULL) <> (subnet_id IS NOT NULL))
);

CREATE INDEX idx_credential_bindings_location ON credential_bindings (location_id);
CREATE INDEX idx_credential_bindings_subnet ON credential_bindings (subnet_id);
