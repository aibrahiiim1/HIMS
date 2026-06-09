-- 000063 added 'manual' to the os_disks/os_nics/os_services/os_software/os_roles
-- collection_source CHECKs but MISSED os_inventory.collection_method, which uses a
-- different vocabulary (winrm/ssh/snmp/winrm-native/wmi/winrm-agent). Without
-- 'manual' here, a virtual workstation's OS-inventory row (hostname/OS) fails the
-- CHECK and is silently dropped, so the Workstation detail page shows "No OS
-- inventory yet" even though the operator entered OS data. Add 'manual' to match
-- the rest of the virtual-device manual-source pattern. Backward-compatible —
-- collectors only ever write their own method.
ALTER TABLE os_inventory DROP CONSTRAINT IF EXISTS os_inventory_collection_method_check;
ALTER TABLE os_inventory ADD  CONSTRAINT os_inventory_collection_method_check
    CHECK (collection_method IN ('winrm','ssh','snmp','winrm-native','wmi','winrm-agent','manual'));
