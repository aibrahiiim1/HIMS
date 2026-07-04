import type { ComponentType } from 'react'
import {
  LayoutDashboard, Bell, Search, HeartPulse, Radar, BookOpen, KeyRound,
  Boxes, Layers, Network, Flame, Wifi, Cpu, Server, HardDrive, MonitorSmartphone,
  Plug, BatteryCharging, ShieldAlert, Camera, Video, Phone, CircleHelp, Brain, Laptop,
  Map, Route as RouteIcon, Waypoints, ClipboardList, ListChecks, Wrench, Package, DollarSign, Building2,
  FileChartColumn, ChartLine, Unplug,
  Users, ShieldCheck, LayoutTemplate, Tags, ScanLine, Settings, FileClock, Lock, Activity, MonitorPlay, ClipboardCheck, Send, FileCode, BadgeCheck, DatabaseBackup,
} from 'lucide-react'

export type BadgeKey = 'alerts' | 'failed_scans' | 'unknown' | 'unmanaged' | 'unmapped' | 'work_orders'

export interface NavLeaf {
  label: string
  to: string
  icon?: ComponentType<{ size?: number | string; className?: string }>
  badge?: BadgeKey
  // Device categories this item lists — drives a red "offline/disconnected" count badge
  // (sum of offline devices across these categories). A group's count = union of its children.
  categories?: string[]
}
export interface NavItem {
  label: string
  icon: ComponentType<{ size?: number | string; className?: string }>
  to?: string
  badge?: BadgeKey
  categories?: string[]
  children?: NavLeaf[]
}
export interface NavGroup {
  title: string
  items: NavItem[]
}

// Every entry below routes to a real, implemented page — there are no
// placeholder/"coming soon" destinations. Items that used to be placeholders
// were either merged into an existing page (and removed here) or implemented.
export const NAV: NavGroup[] = [
  {
    title: 'Overview',
    items: [
      { label: 'Dashboard', icon: LayoutDashboard, to: '/dashboard' },
      { label: 'NOC Wallboard', icon: MonitorPlay, to: '/noc' },
      { label: 'Alerts', icon: Bell, to: '/alerts', badge: 'alerts' },
      { label: 'Action Center', icon: Wrench, to: '/action-center' },
      { label: 'Global Search', icon: Search, to: '/search' },
    ],
  },
  {
    title: 'Monitoring',
    items: [
      { label: 'Health Overview', icon: HeartPulse, to: '/monitoring' },
    ],
  },
  {
    title: 'Discovery',
    items: [
      { label: 'Discovery Center', icon: Radar, to: '/discovery', badge: 'failed_scans' },
      { label: 'Scan Jobs', icon: ListChecks, to: '/discovery/jobs' },
      { label: 'Scan Results', icon: ClipboardList, to: '/discovery/results' },
      { label: 'MIB Browser', icon: BookOpen, to: '/mibs' },
      { label: 'Credentials', icon: KeyRound, to: '/credentials' },
      { label: 'Vendor Profiles', icon: Plug, to: '/vendor-profiles' },
      { label: 'Relay Agents', icon: Radar, to: '/agents' },
      { label: 'Trust Audit', icon: ShieldCheck, to: '/trust-audit' },
    ],
  },
  {
    title: 'Inventory',
    items: [
      { label: 'All Devices', icon: Boxes, to: '/inventory' },
      {
        label: 'Network Devices', icon: Layers,
        children: [
          { label: 'All Network', to: '/inventory/network', icon: Layers },
          { label: 'Switches', to: '/', icon: Network, categories: ['switch'] },
          { label: 'Routers', to: '/inventory/network/routers', icon: RouteIcon, categories: ['router', 'isp_router'] },
          { label: 'Firewalls', to: '/firewalls', icon: Flame, categories: ['firewall'] },
          { label: 'Wireless Controllers', to: '/wlan', icon: Wifi, categories: ['wireless_controller'] },
          { label: 'Access Points', to: '/inventory/network/access-points', icon: Wifi, categories: ['access_point'] },
        ],
      },
      {
        label: 'Compute', icon: Cpu,
        children: [
          { label: 'All Compute', to: '/inventory/compute', icon: Layers },
          { label: 'Servers', to: '/servers', icon: Server, categories: ['server'] },
          { label: 'Virtual Hosts', to: '/virtual-hosts', icon: HardDrive, categories: ['virtual_host'] },
          { label: 'iLO / BMC / iDRAC', to: '/inventory/compute/bmc', icon: Cpu, categories: ['bmc'] },
        ],
      },
      {
        label: 'Endpoints & Peripherals', icon: MonitorSmartphone,
        children: [
          { label: 'All Endpoints', to: '/inventory/endpoints', icon: Layers },
          { label: 'Endpoint Intelligence', to: '/endpoint-intelligence', icon: ChartLine },
          { label: 'Workstations', to: '/workstations', icon: Laptop, categories: ['endpoint'] },
          { label: 'Printers', to: '/printers', icon: Plug, categories: ['printer'] },
          { label: 'Biometric Devices', to: '/inventory/endpoints/biometric', icon: ScanLine, categories: ['biometric', 'biometric_device_unclassified'] },
          { label: 'Point of Sale', to: '/inventory/endpoints/pos', icon: DollarSign, categories: ['pos', 'pos_device_unclassified'] },
          { label: 'NAS Storage', to: '/inventory/endpoints/storage', icon: HardDrive, categories: ['storage'] },
          { label: 'UPS', to: '/ups', icon: BatteryCharging, categories: ['ups'] },
        ],
      },
      {
        label: 'Security & Surveillance', icon: ShieldAlert,
        children: [
          { label: 'All Security & Surveillance', to: '/inventory/security', icon: Layers },
          { label: 'Cameras', to: '/cameras', icon: Camera, categories: ['camera'] },
          { label: 'NVRs', to: '/nvrs', icon: Video, categories: ['nvr', 'dvr'] },
        ],
      },
      {
        label: 'Voice', icon: Phone,
        children: [
          { label: 'All Voice', to: '/inventory/voice', icon: Layers },
          { label: 'PBX / Voice', to: '/pbx', icon: Phone, categories: ['pbx', 'voice_gateway'] },
          { label: 'IP Phones', to: '/inventory/voice/phones', icon: Phone, categories: ['ip_phone'] },
        ],
      },
      // Conceptual split: classification problems vs access/management problems.
      { label: 'Missing Classification', icon: CircleHelp, to: '/inventory/missing-classification', badge: 'unknown' },
      { label: 'Unmanaged Devices', icon: ShieldAlert, to: '/inventory/unmanaged', badge: 'unmanaged' },
      { label: 'Unmapped Devices', icon: Unplug, to: '/inventory/unmapped', badge: 'unmapped' },
      { label: 'Device Intelligence', icon: Brain, to: '/device-intelligence' },
    ],
  },
  {
    title: 'Topology',
    items: [
      { label: 'Network Map', icon: Map, to: '/topology' },
      { label: 'Path Finder', icon: RouteIcon, to: '/path-finder' },
      { label: 'NetFlow', icon: Waypoints, to: '/netflow' },
      { label: 'Unknown MACs', icon: CircleHelp, to: '/unknown-macs' },
    ],
  },
  {
    title: 'Operations',
    items: [
      { label: 'Work Orders', icon: ClipboardList, to: '/work-orders', badge: 'work_orders' },
      { label: 'Config Backup', icon: FileCode, to: '/config-backups' },
      { label: 'Asset Lifecycle', icon: BadgeCheck, to: '/assets' },
      { label: 'Systems', icon: Wrench, to: '/systems' },
      { label: 'Spare Parts', icon: Package, to: '/spare-parts' },
      { label: 'Expenses', icon: DollarSign, to: '/expenses' },
    ],
  },
  {
    title: 'Organization',
    items: [
      { label: 'Multi-Site View', icon: Building2, to: '/sites' },
      { label: 'Locations', icon: Building2, to: '/locations' },
    ],
  },
  {
    title: 'Reports',
    items: [
      // Single enterprise reporting hub — all reports live as tabs inside /coverage
      // (Management, Data Quality, Alerts, Virtualization, Detailed Reports, Export)
      // instead of cluttering the sidebar with one link per report.
      { label: 'Reporting', icon: FileChartColumn, to: '/coverage' },
    ],
  },
  {
    title: 'Administration',
    items: [
      { label: 'Users', icon: Users, to: '/access-control/users' },
      { label: 'Roles & Permissions', icon: ShieldCheck, to: '/access-control/roles' },
      { label: 'Device Templates', icon: LayoutTemplate, to: '/device-templates' },
      { label: 'Device Categories', icon: Tags, to: '/device-categories' },
      { label: 'Vendor Fingerprints', icon: ScanLine, to: '/vendor-fingerprints' },
      { label: 'Encryption', icon: Lock, to: '/security/encryption' },
      { label: 'Notifications', icon: Send, to: '/notifications' },
      { label: 'System Health', icon: Activity, to: '/system-health' },
      { label: 'Data Quality', icon: ClipboardCheck, to: '/data-quality' },
      { label: 'Backup & Restore', icon: DatabaseBackup, to: '/backup-restore' },
      { label: 'API Documentation', icon: BookOpen, to: '/api-docs' },
      { label: 'System Settings', icon: Settings, to: '/settings' },
      { label: 'Audit Log', icon: FileClock, to: '/audit-log' },
    ],
  },
]
