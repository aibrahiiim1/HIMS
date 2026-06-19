-- Hyper-V/vSphere VM identity + MAC for reverse-linking a guest VM to a discovered
-- device by MAC (not only by guest IP, which needs integration services/tools).
ALTER TABLE virtual_machines
    ADD COLUMN IF NOT EXISTS vm_id TEXT,
    ADD COLUMN IF NOT EXISTS mac   TEXT;
