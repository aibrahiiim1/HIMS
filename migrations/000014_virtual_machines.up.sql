-- HIMS 3b/3c: virtualization. virtual_machines is the per-VM inventory under
-- a virtual_host device. Populated by the vSphere/Hyper-V API transport
-- (deferred); the table + read path ship now so the virtual_host detail page
-- has its VM section, and so the host→VM mapping has a home.

CREATE TABLE virtual_machines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    -- Optional link to a managed device row if the VM is itself inventoried.
    vm_device_id   UUID REFERENCES devices(id) ON DELETE SET NULL,
    name           TEXT NOT NULL,
    power_state    TEXT NOT NULL DEFAULT 'unknown'
        CHECK (power_state IN ('on','off','suspended','unknown')),
    vcpu           INT,
    mem_mb         INT,
    guest_os       TEXT,
    primary_ip     INET,
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_device_id, name)
);

CREATE INDEX idx_virtual_machines_host ON virtual_machines (host_device_id);
