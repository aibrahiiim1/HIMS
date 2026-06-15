-- Live registration status per phone (CUCM RisPort: Registered / UnRegistered /
-- Rejected / Unknown / PartiallyRegistered). OmniPCX leaves it null (the mgr
-- directory has no live-registration view).
ALTER TABLE pbx_phones ADD COLUMN IF NOT EXISTS registration TEXT;
