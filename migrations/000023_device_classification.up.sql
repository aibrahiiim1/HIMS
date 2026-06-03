-- HIMS: operator-set classification fields on a device, shown + edited in the
-- Inventory view. Free-text (nullable):
--   vlan         — the VLAN tag/name the operator assigns (e.g. "10" / "Guest")
--   device_class — an operator classification/grouping (e.g. "Core", "Access",
--                  "Production"); distinct from the discovery `category` type
--   location     — a human location label (e.g. "Bldg A / Rack 5"); distinct
--                  from the scoping site FK (location_id)

ALTER TABLE devices ADD COLUMN vlan         TEXT;
ALTER TABLE devices ADD COLUMN device_class TEXT;
ALTER TABLE devices ADD COLUMN location     TEXT;
