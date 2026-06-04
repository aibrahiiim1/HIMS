const BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

async function get<T>(path: string): Promise<T> {
  const r = await fetch(`${BASE}${path}`)
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

async function patch<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(`${BASE}${path}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

async function put<T>(path: string, body: unknown): Promise<T | void> {
  const r = await fetch(`${BASE}${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  if (r.status === 204) return
  return r.json()
}

async function del(path: string): Promise<void> {
  const r = await fetch(`${BASE}${path}`, { method: 'DELETE' })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
}

// postText sends a raw text body (e.g. text/csv for bulk import).
async function postText<T>(path: string, body: string, contentType = 'text/csv'): Promise<T> {
  const r = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': contentType },
    body,
  })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

// postForm sends multipart/form-data (file uploads). The browser sets the
// Content-Type boundary; do not set it manually.
async function postForm<T>(path: string, body: FormData): Promise<T> {
  const r = await fetch(`${BASE}${path}`, { method: 'POST', body })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

export const api = { get, post, patch, put, del, postText, postForm }

// ---- Domain types -----------------------------------------------------------

export interface Device {
  id: string
  name: string
  primary_ip?: string | null
  hostname?: string | null
  vendor?: string | null
  model?: string | null
  serial?: string | null
  os_version?: string | null
  category: string
  status: string
  driver?: string | null
  location_id?: string | null
  last_discovery_at?: string | null
  vlan?: string | null
  device_class?: string | null
  location?: string | null
}

export interface Interface {
  id: string
  device_id: string
  if_index: number
  if_name?: string | null
  if_descr?: string | null
  if_alias?: string | null
  if_type?: number | null
  mac?: string | null
  speed_mbps?: number | null
  admin_status?: number | null
  oper_status?: number | null
  port_role: string
  last_seen_at: string
}

export interface VLAN {
  id: string
  device_id: string
  vlan_id: number
  name?: string | null
  last_seen_at: string
}

export interface PortVlan {
  id: string
  device_id: string
  if_index: number
  vlan_id: number
  tagged: boolean
  collection_source: string
  last_seen_at: string
}

export interface MacEntry {
  id: string
  mac: string
  vlan_id: number
  if_index?: number | null
  fdb_status: number
  collection_source: string
  last_seen_at: string
  if_name?: string | null
  owner_name?: string | null
  owner_vendor?: string | null
}

export interface ArpEntry {
  id: string
  ip_address: string
  mac: string
  if_index?: number | null
  collection_source: string
  last_seen_at: string
  if_name?: string | null
  owner_name?: string | null
}

export interface MacCount {
  if_index?: number | null
  mac_count: number
}

export interface Neighbor {
  id: string
  device_id: string
  local_if_index?: number | null
  local_if_name?: string | null
  rem_chassis_id?: string | null
  rem_sys_name?: string | null
  rem_sys_desc?: string | null
  rem_port_id?: string | null
  rem_port_desc?: string | null
  rem_mgmt_ip?: string | null
  protocol: string
  last_seen_at: string
}

export interface TopologyLink {
  local_device_id: string
  local_device_name: string
  local_ip?: string | null
  local_if_index?: number | null
  local_if_name?: string | null
  remote_device_id?: string | null
  remote_device_name?: string | null
  remote_ip?: string | null
  remote_sys_name?: string | null
  link_source: string
}

export interface SwitchPortEntry {
  switch_id: string
  switch_name: string
  switch_ip?: string | null
  if_index?: number | null
  if_name?: string | null
  vlan_id: number
  port_role?: string | null
  source?: string | null
  last_seen_at?: string | null
}

export interface SearchResult {
  query: string
  query_type: string
  mac?: string | null
  device_id?: string | null
  device_name?: string | null
  switch_port: SwitchPortEntry[]
  path: PathStep[]
  arp_device_id?: string | null
  arp_device_name?: string | null
  arp_source?: string | null
  arp_last_seen?: string | null
  confidence: string
  confidence_reasons: string[]
}

export interface PathStep {
  hop: number
  role: string
  device_id?: string | null
  device_name?: string | null
  ip?: string | null
  if_index?: number | null
  if_name?: string | null
  vlan_id?: number | null
  port_role?: string | null
  source?: string | null
}

export interface ServerStorage {
  id: string
  device_id: string
  hr_index: number
  descr?: string | null
  storage_type: string
  total_bytes?: number | null
  used_bytes?: number | null
  last_seen_at: string
}

export interface DeviceFact {
  device_id: string
  key: string
  value?: string | null
  driver: string
  observed_at: string
}

export interface FirewallStatus {
  device_id: string
  ha_mode: string
  ha_group_name?: string | null
  ha_member_count: number
  session_count?: number | null
  last_seen_at: string
}

export interface VpnTunnel {
  id: string
  device_id: string
  tunnel_name: string
  p1_name?: string | null
  remote_gw?: string | null
  status: string
  in_octets?: number | null
  out_octets?: number | null
  last_seen_at: string
}

export interface HAMember {
  id: string
  device_id: string
  serial: string
  hostname?: string | null
  cpu_pct?: number | null
  mem_pct?: number | null
  session_count?: number | null
  sync_status: string
}

export interface License {
  id: string
  device_id: string
  contract: string
  expiry?: string | null
}

export interface DeviceRole {
  device_id: string
  role: string
  source: string
}

export interface WorkOrder {
  id: string
  device_id?: string | null
  location_id?: string | null
  title: string
  problem_type: string
  priority: string
  status: string
  assigned_to?: string | null
  diagnosis?: string | null
  action_taken?: string | null
  spare_parts?: string | null
  external_vendor?: string | null
  cost: number
  created_at: string
  resolved_at?: string | null
}

export interface WorkOrderEvent {
  id: string
  work_order_id: string
  event_type: string
  note?: string | null
  actor?: string | null
  created_at: string
}

export interface SystemLicense {
  id: string
  name: string
  vendor?: string | null
  location_id?: string | null
  license_expiry?: string | null
  support_expiry?: string | null
  cost: number
  notes?: string | null
  license_status: string
  support_status: string
  overall_status: string
}

export interface SparePart {
  id: string
  name: string
  sku?: string | null
  category: string
  location_id?: string | null
  quantity: number
  min_quantity: number
  unit_cost: number
  notes?: string | null
  stock_status: string
}

export interface Purchase {
  id: string
  description: string
  vendor?: string | null
  category: string
  location_id?: string | null
  system_id?: string | null
  device_id?: string | null
  amount: number
  purchased_at: string
  invoice_ref?: string | null
  notes?: string | null
}

export interface ExpenseByCategory {
  category: string
  total: number
  count: number
}

export interface ExpenseByLocation {
  location_id?: string | null
  location_name?: string | null
  total: number
  count: number
}

export interface UPSStatus {
  device_id?: string
  manufacturer?: string | null
  model?: string | null
  battery_status?: string
  charge_pct?: number | null
  runtime_min?: number | null
  load_pct?: number | null
}

export interface PrinterSupply {
  id: string
  device_id: string
  supply_index: number
  description?: string | null
  level?: number | null
  max_capacity?: number | null
  pct?: number | null
}

export interface PhoneExtension {
  id: string
  device_id: string
  name: string
  model?: string | null
  description?: string | null
  device_pool?: string | null
}

export interface BMCInfo {
  device_id?: string
  vendor?: string | null
  controller_kind?: string | null
  model?: string | null
  serial?: string | null
  firmware_version?: string | null
  power_state?: string | null
  health?: string | null
}

export interface BMCSensor {
  id: string
  device_id: string
  kind: string
  name: string
  status?: string | null
  reading?: number | null
  unit?: string | null
  has_reading: boolean
}

export interface DiscoveryJob {
  id: string
  location_id?: string | null
  scope_cidr?: string | null
  status: string
  started_at?: string | null
  finished_at?: string | null
  host_count: number
  found_count: number
  error?: string | null
  created_at: string
}

export interface DiscoveryResult {
  id: string
  job_id: string
  ip: string
  outcome: string
  device_id?: string | null
  driver?: string | null
  category?: string | null
  error?: string | null
  probed_at: string
}

export interface MibFile {
  id: string
  name: string
  object_count: number
  unresolved: number
  uploaded_at: string
}

export interface MibObject {
  id: string
  mib_file_id: string
  name: string
  oid: string
  syntax?: string | null
  kind: string
  unresolved: boolean
}

export interface OIDMapping {
  id: string
  oid: string
  label: string
  metric_key?: string | null
  vendor?: string | null
  template?: string | null
  notes?: string | null
}

export interface RoleSummaryRow {
  role: string
  count: number
}

export interface WLANControllerInfo {
  device_id?: string
  vendor?: string | null
  version?: string | null
  ap_count?: number
  client_count?: number
}

export interface AccessPoint {
  id: string
  controller_device_id: string
  name: string
  mac?: string | null
  model?: string | null
  ip?: string | null
  status: string
  client_count: number
}

export interface CameraInfo {
  device_id?: string
  manufacturer?: string | null
  model?: string | null
  resolution?: string | null
  rtsp_url?: string | null
  onvif_url?: string | null
}

export interface NVRChannel {
  id: string
  nvr_device_id: string
  channel_no: number
  camera_name?: string | null
  camera_ip?: string | null
  status: string
}

export interface VirtualMachine {
  id: string
  host_device_id: string
  vm_device_id?: string | null
  name: string
  power_state: string
  vcpu?: number | null
  mem_mb?: number | null
  guest_os?: string | null
  primary_ip?: string | null
  last_seen_at: string
}

// Credential is metadata-only — the secret and encrypted blob never leave
// the server.
export interface Credential {
  id: string
  name: string
  kind: string
  weak: boolean
  created_at: string
}

export interface AlertRule {
  id: string
  name: string
  trigger_status: string
  min_failures: number
  device_category?: string | null
  severity: string
  auto_work_order: boolean
  work_order_priority: string
  enabled: boolean
  escalate_after_minutes: number
}

export interface Alert {
  id: string
  rule_id: string
  device_id: string
  check_id?: string | null
  severity: string
  status: string
  message: string
  work_order_id?: string | null
  opened_at: string
  acknowledged_at?: string | null
  acknowledged_by?: string | null
  escalated: boolean
  escalated_at?: string | null
  resolved_at?: string | null
}

export interface AlertEvent {
  id: number
  alert_id: string
  at: string
  kind: string
  actor: string
  note: string
}

export interface NotificationChannel {
  id: string
  name: string
  type: string
  min_severity: string
  enabled: boolean
  quiet_start?: string | null
  quiet_end?: string | null
  target_hint: string
  created_at: string
}

export interface NotificationLogEntry {
  id: number
  channel_id: string
  alert_id?: string | null
  at: string
  status: string
  detail: string
}

export interface MaintenanceWindow {
  id: string
  scope: string
  device_id?: string | null
  location_id?: string | null
  reason: string
  starts_at: string
  ends_at: string
  created_by: string
  created_at: string
}

export interface MonitoringCheck {
  id: string
  device_id: string
  kind: string
  target_port?: number | null
  oid?: string | null
  interval_seconds: number
  down_threshold: number
  enabled: boolean
  last_run_at?: string | null
  last_status: string
  last_latency_ms?: number | null
  consecutive_failures: number
}

export interface MonitoringSample {
  time: string
  check_id: string
  device_id: string
  status: string
  latency_ms?: number | null
  value_num?: number | null
  error?: string | null
}

export interface MonitoringOverviewRow {
  status: string
  count: number
}

export interface Location {
  id: string
  parent_id?: string | null
  kind: string
  name: string
  code?: string | null
}

export interface Lookup {
  id: string
  kind: string
  value: string
}

// ---- Admin: RBAC / templates / fingerprints / audit -------------------------
export interface AppUser {
  id: string
  username: string
  full_name: string
  email: string
  is_active: boolean
  created_at: string
  updated_at: string
}
export interface Role { id: string; name: string; description: string; created_at: string }
export interface Permission { id: string; code: string; description: string; created_at: string }
export interface DeviceTemplate {
  id: string
  name: string
  vendor: string
  device_type: string
  discovery_rules: unknown
  monitoring_rules: unknown
  classification_rules: unknown
  enabled: boolean
}
export interface VendorFingerprint {
  id: string
  kind: string
  pattern: string
  vendor: string
  device_type: string
  confidence: number
  enabled: boolean
  created_at: string
}
export type EncryptionState = 'enabled' | 'pending_restart' | 'missing_key' | 'no_metadata' | 'fingerprint_mismatch' | 'invalid_key'
export interface EncryptionStatus {
  status: EncryptionState
  reason: string
  configured: boolean
  enabled: boolean
  algorithm: string
  fingerprint: string
  key_id: string
  version: number
  created_at?: string | null
  last_rotation_at?: string | null
  last_validation_at?: string | null
  encrypted_count: number
  needs_reset_count: number
  undecryptable_count: number
  fingerprint_match: boolean
  runtime_key_present: boolean
  runtime_key_length_valid: boolean
  stored_fingerprint_present: boolean
  warnings: string[]
}
export interface EncryptionDiagnostics {
  runtime_key_present: boolean
  runtime_key_length_valid: boolean
  stored_fingerprint_present: boolean
  runtime_fingerprint: string
  stored_fingerprint: string
  fingerprint_match: boolean
  self_test_passed: boolean
  status: EncryptionState
  reason: string
}
export interface KeyReveal { key?: string; new_key?: string; fingerprint: string; key_id: string; instructions: string; rotated?: number; failed?: { name: string; reason: string }[] }

// EncryptionUnlockResult is the response of POST /security/encryption/unlock.
// We read the body on success AND on 4xx (mismatch/invalid) so the UI can offer
// the "adopt this key" path. The raw key is never echoed back.
export interface EncryptionUnlockResult {
  ok: boolean
  status: EncryptionState
  detail?: string
  fingerprint?: string
  key_id?: string
  adopted?: boolean
  runtime_fingerprint?: string
  stored_fingerprint?: string
  can_adopt?: boolean
}
export async function unlockEncryption(key: string, adopt = false): Promise<EncryptionUnlockResult> {
  const r = await fetch(`${BASE}/security/encryption/unlock`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ key, adopt }),
  })
  const data = (await r.json().catch(() => ({}))) as Record<string, unknown>
  return { ok: r.ok, ...data } as EncryptionUnlockResult
}
export interface ReentryCred { id: string; name: string; kind: string; weak: boolean; needs_secret_reentry: boolean; created_at: string; updated_at: string }
export interface GuideSection { title: string; body: string }

export interface InfrastructureHealth {
  overall: { score: number; status: string; confidence: string; limited_reasons: string[] }
  sections: { name: string; status: string; score: number; included: boolean; reason: string }[]
  alerts: {
    status: string; open_critical: number; open_warning: number; acknowledged: number
    unresolved: number; last_alert_at?: string | null; active_rules: number
  }
}

export interface OperationalHealth {
  discovery: {
    status: string; last_scan_at?: string | null; last_scan_status: string
    successful_scan_percent?: number | null; failed_scan_count: number
    credential_failure_count?: number | null; pending_job_count: number
  }
  monitoring: {
    status: string; monitored_devices: number; online_devices: number; offline_devices: number
    critical_alerts: number; warning_alerts: number; last_collection_at?: string | null; collection_status: string
  }
  topology: {
    status: string; mapped_devices: number; unmapped_devices: number; missing_neighbors: number
    coverage_percent?: number | null; lldp_cdp_data_age?: string | null; last_topology_refresh_at?: string | null
  }
}

// RuntimeInfo is the identity of the API process currently serving requests
// (GET /system/runtime). No secrets: key id only, DB url password redacted.
export interface RuntimeInfo {
  process_id: number
  started_at: string
  uptime: string
  uptime_seconds: number
  api_version: string
  git_commit: string
  database_url_redacted: string
  encryption_state: EncryptionState
  key_id: string
  port: string
  environment: string
  hostname: string
}

export interface DataQualityDevice { id: string; name: string; primary_ip?: string; category: string; vendor?: string; note?: string }
export interface DataQualityIssue { key: string; label: string; description: string; severity: string; count: number; devices: DataQualityDevice[] }
export interface DataQualityReport { generated_at: string; total_devices: number; issue_count: number; clean: boolean; issues: DataQualityIssue[] }

export interface AuditEntry {
  id: number
  at: string
  actor: string
  action: string
  category: string
  entity_type: string
  entity_id: string
  summary: string
  details: unknown
}

// locationPaths builds a map of location id -> full path label
// ("Hotel A / Main Building / IT Office") from a flat locations list.
export function locationPaths(locs: Location[]): Record<string, string> {
  const byId: Record<string, Location> = {}
  for (const l of locs) byId[l.id] = l
  const cache: Record<string, string> = {}
  const path = (id: string): string => {
    if (cache[id]) return cache[id]
    const l = byId[id]
    if (!l) return ''
    const p = l.parent_id ? path(l.parent_id) + ' / ' + l.name : l.name
    cache[id] = p
    return p
  }
  const out: Record<string, string> = {}
  for (const l of locs) out[l.id] = path(l.id)
  return out
}

export interface Subnet {
  id: string
  location_id: string
  cidr: string
  name?: string | null
  vlan_id?: number | null
}

export interface CredentialGroup {
  id: string
  name: string
  description?: string | null
  member_count: number
  binding_count: number
}

