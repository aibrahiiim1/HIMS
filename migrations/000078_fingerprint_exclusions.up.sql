-- Device Intelligence Phase 2: data-driven exclusion rules on vendor fingerprints.
-- A fingerprint rule whose positive pattern matches is SUPPRESSED when the
-- evidence also matches any of its exclusions — so a broad/shared signal (e.g. the
-- HP enterprise OID .1.3.6.1.4.1.11 shared by ProCurve switches AND JetDirect
-- printers) is corrected in DATA, not by a hardcoded driver bail. Each exclusion
-- is {"kind":"oid|service|http|ssh|sysname|port","pattern":"..."} matched with the
-- same channel semantics as a positive match. Empty array = no exclusions (the
-- prior behaviour), so existing rows are unaffected.
ALTER TABLE vendor_fingerprints
    ADD COLUMN IF NOT EXISTS exclusions JSONB NOT NULL DEFAULT '[]'::jsonb;
