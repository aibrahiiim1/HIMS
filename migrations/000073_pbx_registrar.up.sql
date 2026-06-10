-- The CM node a phone is registered to (CUCM RisPort CmNode name). Lets the Path
-- Finder show "registered to CUCM <node>" for an IP phone.
ALTER TABLE pbx_phones ADD COLUMN IF NOT EXISTS registrar TEXT;
