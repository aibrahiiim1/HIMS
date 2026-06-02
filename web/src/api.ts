const BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

async function get<T>(path: string): Promise<T> {
  const r = await fetch(`${BASE}${path}`)
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}: ${path}`)
  return r.json()
}

export const api = { get }

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

export interface DeviceRole {
  device_id: string
  role: string
  source: string
}

export interface Location {
  id: string
  parent_id?: string | null
  kind: string
  name: string
  code?: string | null
}
