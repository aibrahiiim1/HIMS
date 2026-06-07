-- Capture the SNMP value type alongside each raw walked row so the MIB
-- Explorer can render binary values correctly (MAC / IP / hex / int / string)
-- and infer mappable columns. Default '' keeps pre-existing rows valid.
ALTER TABLE mib_walk_rows ADD COLUMN IF NOT EXISTS val_type TEXT NOT NULL DEFAULT '';
