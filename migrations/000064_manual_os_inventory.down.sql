-- Revert os_inventory.collection_method to the pre-'manual' vocabulary. Any
-- manually-entered virtual-workstation OS rows must be removed first or the
-- re-added CHECK will fail.
DELETE FROM os_inventory WHERE collection_method = 'manual';
ALTER TABLE os_inventory DROP CONSTRAINT IF EXISTS os_inventory_collection_method_check;
ALTER TABLE os_inventory ADD  CONSTRAINT os_inventory_collection_method_check
    CHECK (collection_method IN ('winrm','ssh','snmp','winrm-native','wmi','winrm-agent'));
