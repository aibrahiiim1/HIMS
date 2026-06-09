import { Network, Flame, Server, MonitorSmartphone, Radio, Antenna, BatteryCharging, Printer, Camera, HardDrive, Box, type LucideIcon } from 'lucide-react'

// Category-aware Virtual Device templates. Each category declares the sections
// (and their column specs) that make sense for it — a virtual switch is modeled
// nothing like a virtual UPS. The form renders the common identity fields plus
// these sections; the backend persists each block into the same table its real
// detail page reads.

export type ColType = 'text' | 'int' | 'int64' | 'bool' | 'select' | 'mac' | 'ip' | 'intlist'
export interface Col { key: string; label: string; type?: ColType; opts?: string[]; w?: number }

export type Section =
  | { kind: 'rows'; block: string; title: string; cols: Col[]; bulk?: number[]; hint?: string }
  | { kind: 'singleton'; block: 'firewall' | 'wlan' | 'ups'; title: string; cols: Col[] }
  | { kind: 'roles'; title: string; hint?: string }
  | { kind: 'facts'; title: string; hint?: string }

export interface CategoryTemplate {
  label: string
  icon: LucideIcon
  blurb: string
  sections: Section[]
}

const portCols: Col[] = [
  { key: 'if_index', label: '#', type: 'int', w: 56 },
  { key: 'name', label: 'Name' },
  { key: 'alias', label: 'Alias / description' },
  { key: 'up', label: 'State', type: 'bool' },
  { key: 'admin_down', label: 'Shut', type: 'bool' },
  { key: 'speed_mbps', label: 'Speed', type: 'int', w: 80 },
  { key: 'vlan', label: 'VLAN', type: 'int', w: 70 },
  { key: 'trunk_vlans', label: 'Trunk VLANs', type: 'intlist', w: 110 },
  { key: 'role', label: 'Role', type: 'select', opts: ['access', 'trunk', 'uplink', 'unknown'] },
  { key: 'mac', label: 'MAC', type: 'mac' },
]
const vlanCols: Col[] = [{ key: 'id', label: 'VLAN ID', type: 'int', w: 100 }, { key: 'name', label: 'Name' }]
const neighborCols: Col[] = [
  { key: 'local_port', label: 'Local port' },
  { key: 'remote_name', label: 'Remote device' },
  { key: 'remote_port', label: 'Remote port' },
  { key: 'remote_mgmt_ip', label: 'Remote mgmt IP', type: 'ip' },
  { key: 'protocol', label: 'Protocol', type: 'select', opts: ['manual', 'lldp', 'cdp'] },
]
const macCols: Col[] = [{ key: 'mac', label: 'MAC', type: 'mac' }, { key: 'vlan', label: 'VLAN', type: 'int', w: 80 }, { key: 'if_index', label: 'Port (ifIndex)', type: 'int', w: 110 }]
const nicCols: Col[] = [
  { key: 'name', label: 'Name' }, { key: 'mac', label: 'MAC', type: 'mac' }, { key: 'ip', label: 'IP', type: 'ip' },
  { key: 'gateway', label: 'Gateway', type: 'ip' }, { key: 'dns', label: 'DNS' }, { key: 'speed_mbps', label: 'Speed', type: 'int', w: 80 },
]
const fwIfaceCols: Col[] = [
  { key: 'name', label: 'Interface' }, { key: 'zone', label: 'Zone (WAN/LAN/DMZ)' }, { key: 'ip', label: 'IP', type: 'ip' },
  { key: 'mac', label: 'MAC', type: 'mac' }, { key: 'speed_mbps', label: 'Speed', type: 'int', w: 80 },
]
const diskCols: Col[] = [
  { key: 'name', label: 'Name / mount' }, { key: 'model', label: 'Model' }, { key: 'filesystem', label: 'FS' },
  { key: 'total_bytes', label: 'Total bytes', type: 'int64', w: 130 }, { key: 'used_bytes', label: 'Used bytes', type: 'int64', w: 130 },
]
const softwareCols: Col[] = [{ key: 'name', label: 'Name' }, { key: 'version', label: 'Version' }, { key: 'publisher', label: 'Publisher' }]
const vpnCols: Col[] = [{ key: 'name', label: 'Tunnel' }, { key: 'p1_name', label: 'Phase-1' }, { key: 'remote_gw', label: 'Remote GW', type: 'ip' }, { key: 'status', label: 'Status', type: 'select', opts: ['up', 'down', 'unknown'] }]
const haCols: Col[] = [{ key: 'serial', label: 'Serial' }, { key: 'hostname', label: 'Hostname' }, { key: 'sync_status', label: 'Sync status' }]
const licCols: Col[] = [{ key: 'contract', label: 'Contract / feature' }, { key: 'expiry', label: 'Expiry (YYYY-MM-DD)' }]
const apCols: Col[] = [
  { key: 'name', label: 'AP name' }, { key: 'mac', label: 'MAC', type: 'mac' }, { key: 'model', label: 'Model' }, { key: 'ip', label: 'IP', type: 'ip' },
  { key: 'status', label: 'Status' }, { key: 'serial', label: 'Serial' }, { key: 'band', label: 'Band' }, { key: 'site', label: 'Site' },
]
const ssidCols: Col[] = [{ key: 'name', label: 'SSID' }, { key: 'security', label: 'Security' }, { key: 'band', label: 'Band' }, { key: 'vlan', label: 'VLAN' }, { key: 'status', label: 'Status' }]
const clientCols: Col[] = [{ key: 'mac', label: 'MAC', type: 'mac' }, { key: 'ip', label: 'IP', type: 'ip' }, { key: 'hostname', label: 'Hostname' }, { key: 'ap_name', label: 'AP' }, { key: 'ssid', label: 'SSID' }, { key: 'band', label: 'Band' }]
const fwSingleton: Col[] = [{ key: 'ha_mode', label: 'HA mode', type: 'select', opts: ['standalone', 'active-passive', 'active-active'] }, { key: 'ha_group_name', label: 'HA group' }, { key: 'session_count', label: 'Sessions', type: 'int' }]
const wlanSingleton: Col[] = [{ key: 'vendor', label: 'Vendor' }, { key: 'version', label: 'Version' }, { key: 'controller_name', label: 'Controller name' }, { key: 'model', label: 'Model' }, { key: 'serial', label: 'Serial' }]
const upsSingleton: Col[] = [
  { key: 'manufacturer', label: 'Manufacturer' }, { key: 'model', label: 'Model' },
  { key: 'battery_status', label: 'Battery status', type: 'select', opts: ['normal', 'on_battery', 'low', 'replace', 'unknown'] },
  { key: 'charge_pct', label: 'Charge %', type: 'int' }, { key: 'runtime_min', label: 'Runtime (min)', type: 'int' }, { key: 'load_pct', label: 'Load %', type: 'int' },
]

const connNeighbor: Section = { kind: 'rows', block: 'neighbors', title: 'Connected to (uplink / switch port)', cols: neighborCols, hint: 'Where this device plugs in — feeds topology.' }

export const VIRTUAL_TEMPLATES: Record<string, CategoryTemplate> = {
  switch: {
    label: 'Switch', icon: Network, blurb: 'Ports, VLANs, neighbors and learned MACs — renders in the Switch detail page.',
    sections: [
      { kind: 'rows', block: 'ports', title: 'Ports / Interfaces', cols: portCols, bulk: [24, 48] },
      { kind: 'rows', block: 'vlans', title: 'VLANs', cols: vlanCols },
      { kind: 'rows', block: 'neighbors', title: 'Neighbors (LLDP/CDP)', cols: neighborCols },
      { kind: 'rows', block: 'macs', title: 'Learned MACs (FDB)', cols: macCols },
    ],
  },
  firewall: {
    label: 'Firewall', icon: Flame, blurb: 'Interfaces/zones, HA, VPN tunnels and licenses — renders in the Firewall detail page.',
    sections: [
      { kind: 'rows', block: 'nics', title: 'Interfaces (WAN / LAN / DMZ)', cols: fwIfaceCols },
      { kind: 'singleton', block: 'firewall', title: 'HA / status', cols: fwSingleton },
      { kind: 'rows', block: 'vpn_tunnels', title: 'VPN tunnels', cols: vpnCols },
      { kind: 'rows', block: 'ha_members', title: 'HA members', cols: haCols },
      { kind: 'rows', block: 'licenses', title: 'Licenses / support', cols: licCols },
      connNeighbor,
    ],
  },
  server: {
    label: 'Server', icon: Server, blurb: 'NICs, disks, roles and specs — renders in the Server detail page.',
    sections: [
      { kind: 'rows', block: 'nics', title: 'Network interfaces', cols: nicCols },
      { kind: 'rows', block: 'disks', title: 'Disks / volumes', cols: diskCols },
      { kind: 'roles', title: 'Roles / services', hint: 'e.g. Application Server, SQL, File Server' },
      { kind: 'facts', title: 'Specs & notes', hint: 'cpu, ram, owner, department, backup …' },
      connNeighbor,
    ],
  },
  endpoint: {
    label: 'Workstation', icon: MonitorSmartphone, blurb: 'OS, NICs, disks, software — renders in the Workstation detail page.',
    sections: [
      { kind: 'rows', block: 'nics', title: 'Network interfaces', cols: nicCols },
      { kind: 'rows', block: 'disks', title: 'Disks', cols: diskCols },
      { kind: 'rows', block: 'software', title: 'Software', cols: softwareCols },
      { kind: 'roles', title: 'Roles', hint: 'optional' },
      { kind: 'facts', title: 'Specs & notes', hint: 'cpu, ram, user, department …' },
      connNeighbor,
    ],
  },
  wireless_controller: {
    label: 'Wireless Controller', icon: Radio, blurb: 'APs, SSIDs and clients — renders in the Wireless detail page.',
    sections: [
      { kind: 'singleton', block: 'wlan', title: 'Controller', cols: wlanSingleton },
      { kind: 'rows', block: 'aps', title: 'Access points', cols: apCols },
      { kind: 'rows', block: 'ssids', title: 'SSIDs', cols: ssidCols },
      { kind: 'rows', block: 'clients', title: 'Clients (optional)', cols: clientCols },
    ],
  },
  access_point: {
    label: 'Access Point', icon: Antenna, blurb: 'A standalone AP — where it connects + identity facts.',
    sections: [
      connNeighbor,
      { kind: 'facts', title: 'AP details', hint: 'mac, serial, ssid, vlan, controller …' },
    ],
  },
  ups: {
    label: 'UPS', icon: BatteryCharging, blurb: 'Battery/load/runtime — renders in the UPS detail page.',
    sections: [
      { kind: 'singleton', block: 'ups', title: 'UPS status', cols: upsSingleton },
      { kind: 'facts', title: 'Capacity & notes', hint: 'capacity_va, input_voltage, output_voltage, connected_devices, maintenance …' },
    ],
  },
  printer: {
    label: 'Printer', icon: Printer, blurb: 'Identity + where it connects.',
    sections: [connNeighbor, { kind: 'facts', title: 'Details', hint: 'serial, toner, page_count …' }],
  },
  camera: {
    label: 'Camera', icon: Camera, blurb: 'Identity + where it connects.',
    sections: [connNeighbor, { kind: 'facts', title: 'Details', hint: 'resolution, nvr, channel …' }],
  },
  nvr: {
    label: 'NVR / DVR', icon: HardDrive, blurb: 'Identity + where it connects.',
    sections: [connNeighbor, { kind: 'facts', title: 'Details', hint: 'channels, storage_tb, cameras …' }],
  },
  other: {
    label: 'Other', icon: Box, blurb: 'A generic device — connection + any L2 detail + free facts.',
    sections: [
      connNeighbor,
      { kind: 'rows', block: 'ports', title: 'Ports (optional)', cols: portCols },
      { kind: 'rows', block: 'vlans', title: 'VLANs (optional)', cols: vlanCols },
      { kind: 'rows', block: 'macs', title: 'Learned MACs (optional)', cols: macCols },
      { kind: 'facts', title: 'Details / notes' },
    ],
  },
}

// Type-picker order (rich categories first, then peripherals, then Other).
export const VIRTUAL_TYPE_ORDER = ['switch', 'firewall', 'server', 'endpoint', 'wireless_controller', 'access_point', 'ups', 'printer', 'camera', 'nvr', 'other']

// templateFor resolves a category (incl. aliases) to its template.
export function templateFor(category: string): CategoryTemplate {
  if (category === 'router' || category === 'isp_router') return VIRTUAL_TEMPLATES.switch
  if (category === 'virtual_host') return VIRTUAL_TEMPLATES.server
  return VIRTUAL_TEMPLATES[category] ?? VIRTUAL_TEMPLATES.other
}

export const VIRTUAL_STATUSES = ['up', 'down', 'warning', 'unknown']
