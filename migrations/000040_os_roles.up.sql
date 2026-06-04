-- OS-detected roles (free-form), separate from the constrained device_roles
-- enum used for topology. These come from authenticated service/package
-- evidence (e.g. "Docker Host", "Kubernetes Node", "Monitoring Server") and
-- intentionally allow any label the collector derives. Prune-on-poll per source.
CREATE TABLE os_roles (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    role              TEXT NOT NULL,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, role)
);
CREATE INDEX idx_os_roles_device ON os_roles (device_id);
