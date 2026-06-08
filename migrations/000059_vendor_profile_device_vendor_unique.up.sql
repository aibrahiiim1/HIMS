-- A device may have at most ONE vendor connection profile per vendor_type. This is
-- defense-in-depth behind the application-level idempotent "Add controller" guard
-- (GetVendorProfileForDeviceVendor → update-in-place). Partial on device_id IS NOT
-- NULL so site-level / global profiles (device_id NULL) remain unconstrained — a
-- plain UNIQUE would treat each NULL as distinct anyway, but the partial index makes
-- the intent explicit and keeps the index small.
CREATE UNIQUE INDEX IF NOT EXISTS uq_vendor_profiles_device_vendor
  ON vendor_connection_profiles (device_id, vendor_type)
  WHERE device_id IS NOT NULL;
