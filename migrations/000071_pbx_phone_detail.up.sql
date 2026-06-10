-- Richer PBX phone/subscriber detail: directory number (extension), MAC, and
-- (best-effort, real-time) IP. CUCM AXL yields extension via numplan + MAC from
-- the SEPxxxx device name; OmniPCX yields the extension (= the directory number).
ALTER TABLE pbx_phones
    ADD COLUMN IF NOT EXISTS extension   TEXT,
    ADD COLUMN IF NOT EXISTS mac_address TEXT,
    ADD COLUMN IF NOT EXISTS ip_address  TEXT;
