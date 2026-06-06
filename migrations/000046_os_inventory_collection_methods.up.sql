-- Widen os_inventory.collection_method to cover every transport that actually
-- writes an inventory row. The original CHECK (migration 000039) allowed only
-- winrm/ssh/snmp, predating the legacy-Windows fallbacks and the Relay Agent:
--   winrm-native  — native PowerShell collector (Invoke-Command)
--   wmi           — WMI/DCOM collector (Get-WmiObject), incl. Relay Agent WMI
--   winrm-agent   — Relay Agent modern WinRM collection
-- access.sql already reads these values, so the constraint was the only thing
-- still rejecting them on persist. Drop + re-add with the full provenance set.
ALTER TABLE os_inventory DROP CONSTRAINT IF EXISTS os_inventory_collection_method_check;
ALTER TABLE os_inventory ADD CONSTRAINT os_inventory_collection_method_check
    CHECK (collection_method IN ('winrm','ssh','snmp','winrm-native','wmi','winrm-agent'));
