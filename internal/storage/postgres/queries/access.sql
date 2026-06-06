-- name: ListDeviceAccessSignals :many
-- One row per (device, protocol, source) describing a REAL way HIMS can manage
-- the device. "Managed access" means an authenticated/working method — a bound
-- credential or proof of a successful authenticated collection — NOT merely an
-- open port. Open-port classification evidence is deliberately excluded here.
--
-- source values: 'bound_credential' (devices.credential_id) outranks 'evidence'
-- (a child table that only exists because an authenticated collection succeeded).
-- Aggregation/normalisation of protocol tokens happens in Go (access_coverage.go).
SELECT device_id, protocol::text AS protocol, source::text AS source FROM (
  -- 1) Device-bound credential — the operator's/HIMS's confirmed working method.
  SELECT d.id AS device_id, c.kind AS protocol, 'bound_credential' AS source
    FROM devices d JOIN credentials c ON c.id = d.credential_id
    WHERE d.deleted_at IS NULL

  -- 2) Deep OS inventory proves authenticated WinRM/SSH/WMI. winrm-native (the
  --    Windows Native Collector helper) is still WinRM-family; wmi is its own.
  UNION ALL
  SELECT device_id,
         CASE WHEN collection_method = 'wmi' THEN 'wmi'
              WHEN collection_method = 'ssh' THEN 'ssh'
              ELSE 'winrm' END AS protocol,
         'evidence' AS source
    FROM os_inventory WHERE collection_method IN ('winrm', 'ssh', 'winrm-native', 'wmi')

  -- 3) ONVIF camera inventory (authenticated device-info / profiles).
  UNION ALL
  SELECT DISTINCT device_id, 'onvif' AS protocol, 'evidence' AS source FROM camera_info

  -- 4) Wireless controller REST (UniFi/Omada/Ruckus/Extreme).
  UNION ALL
  SELECT DISTINCT device_id, 'vendor_api' AS protocol, 'evidence' AS source FROM wlan_controller_info

  -- 4b) VMware vSphere/ESXi inventory (authenticated host/VM collection).
  UNION ALL
  SELECT DISTINCT host_device_id AS device_id, 'vmware' AS protocol, 'evidence' AS source FROM virtual_machines

  -- 5) BMC out-of-band (Redfish HTTPS/JSON API).
  UNION ALL
  SELECT DISTINCT device_id, 'api_token' AS protocol, 'evidence' AS source FROM bmc_info

  -- 6) FortiGate firewall state (validated SNMP OIDs).
  UNION ALL
  SELECT DISTINCT device_id, 'snmp_v2c' AS protocol, 'evidence' AS source FROM firewall_status

  -- 7) UPS status (UPS-MIB over SNMP).
  UNION ALL
  SELECT DISTINCT device_id, 'snmp_v2c' AS protocol, 'evidence' AS source FROM ups_status

  -- 8) Printer marker supplies (Printer-MIB over SNMP).
  UNION ALL
  SELECT DISTINCT device_id, 'snmp_v2c' AS protocol, 'evidence' AS source FROM printer_supplies

  -- 9) PBX phone registry (Cisco CUCM AXL).
  UNION ALL
  SELECT DISTINCT device_id, 'cucm_axl' AS protocol, 'evidence' AS source FROM pbx_phones

  -- 10) Switch interface collection — SNMP or CLI(SSH) per its collection_source.
  UNION ALL
  SELECT DISTINCT device_id,
                  CASE WHEN collection_source = 'cli' THEN 'ssh' ELSE 'snmp_v2c' END AS protocol,
                  'evidence' AS source
    FROM interfaces

  -- 11) A saved credential-test whose LATEST result for that (device, kind)
  --     succeeded — durable proof a credential works, even before/without a bind.
  UNION ALL
  SELECT device_id, kind AS protocol, 'test_result' AS source
    FROM (
      SELECT DISTINCT ON (device_id, kind) device_id, kind, success
        FROM credential_test_results
        WHERE kind <> ''
        ORDER BY device_id, kind, tested_at DESC
    ) latest
    WHERE success
) sig;
