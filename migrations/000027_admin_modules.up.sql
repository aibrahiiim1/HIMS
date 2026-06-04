-- HIMS admin subsystems: RBAC (users/roles/permissions), device templates,
-- vendor fingerprints, and an audit log. All operator-managed; no seed/demo
-- rows are inserted — these tables start empty and are populated through the UI.

-- ---- RBAC -----------------------------------------------------------------
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username    TEXT NOT NULL UNIQUE,
    full_name   TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT NOT NULL UNIQUE,   -- e.g. devices.read, discovery.run
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE user_roles (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- ---- Device templates -----------------------------------------------------
-- A reusable profile keyed by (vendor, device_type) carrying the discovery,
-- monitoring and classification rules applied to matching devices.
CREATE TABLE device_templates (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    vendor              TEXT NOT NULL DEFAULT '',
    device_type         TEXT NOT NULL DEFAULT '',
    discovery_rules     JSONB NOT NULL DEFAULT '{}'::jsonb,
    monitoring_rules    JSONB NOT NULL DEFAULT '{}'::jsonb,
    classification_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled             BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_device_templates_vendor ON device_templates (vendor, device_type);

-- ---- Vendor fingerprints --------------------------------------------------
-- Operator-managed signatures the classifier can match: OID / service / port /
-- http / ssh patterns mapping to a vendor + device type with a confidence.
CREATE TABLE vendor_fingerprints (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('oid','service','port','http','ssh')),
    pattern     TEXT NOT NULL,
    vendor      TEXT NOT NULL DEFAULT '',
    device_type TEXT NOT NULL DEFAULT '',
    confidence  INTEGER NOT NULL DEFAULT 50 CHECK (confidence BETWEEN 0 AND 100),
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_vendor_fingerprints_kind ON vendor_fingerprints (kind);

-- ---- Audit log ------------------------------------------------------------
CREATE TABLE audit_log (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor       TEXT NOT NULL DEFAULT 'operator',
    action      TEXT NOT NULL,           -- e.g. device.delete, credential.create
    category    TEXT NOT NULL DEFAULT 'general', -- user|discovery|inventory|credential|config|general
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id   TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    details     JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_audit_log_at ON audit_log (at DESC);
CREATE INDEX idx_audit_log_category ON audit_log (category, at DESC);
