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

async function del(path: string): Promise<void> {
  const r = await fetch(`${BASE}${path}`, { method: 'DELETE' })
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
}

export const api = { get, post, patch, del }

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
}

export interface SearchResult {
  query: string
  query_type: string
  mac?: string | null
  device_id?: string | null
  device_name?: string | null
  switch_port: SwitchPortEntry[]
  path: PathStep[]
}

export interface PathStep {
  device_id?: string | null
  device_name?: string | null
  ip?: string | null
  if_index?: number | null
  if_name?: string | null
  vlan_id?: number | null
  port_role?: string | null
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
  resolved_at?: string | null
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
