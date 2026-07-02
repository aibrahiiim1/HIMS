-- Backfill devices.serial from a proven os_inventory.serial where the device row is empty.
-- This closes a historical gap: OS collections BEFORE the osinv.Persist device-enrichment fix
-- (commit 4d6350d) wrote the chassis serial only to os_inventory, never to the device row —
-- which BMC reverse-linking (serial/UUID evidence) uses. Going forward osinv.Persist keeps them
-- in sync; this one-time backfill fixes the pre-fix rows (e.g. servers whose iLO/iDRAC could not
-- be linked because their collected serial never reached devices.serial).
--
-- SAFE: only fills an EMPTY devices.serial (COALESCE/NULLIF) — never overwrites a proven value.
-- Obvious BIOS junk placeholders are excluded so they are never persisted as an identity/serial.
UPDATE devices d
SET serial = TRIM(oi.serial)
FROM os_inventory oi
WHERE oi.device_id = d.id
  AND d.deleted_at IS NULL
  AND COALESCE(NULLIF(TRIM(d.serial), ''), '') = ''
  AND COALESCE(NULLIF(TRIM(oi.serial), ''), '') <> ''
  AND LOWER(TRIM(oi.serial)) NOT IN (
    'system serial number', 'to be filled by o.e.m.', 'to be filled by o.e.m',
    'default string', 'not specified', 'not available', 'none', 'o.e.m.', 'oem',
    '0', 'na', 'n/a', 'unknown', 'invalid'
  );
