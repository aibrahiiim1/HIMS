import type { ComponentType } from 'react'
import {
  LayoutDashboard, Bell, Search, HeartPulse, Radar, BookOpen, KeyRound,
  Boxes, Layers, Network, Flame, Wifi, Cpu, Server, HardDrive, MonitorSmartphone,
  Plug, BatteryCharging, ShieldAlert, Camera, Video, Phone, CircleHelp, Brain,
  Map, Route as RouteIcon, Waypoints, ClipboardList, Wrench, Package, DollarSign, Building2,
  FileChartColumn, FileSearch, ChartLine, Tag, Download,
  Users, ShieldCheck, LayoutTemplate, ScanLine, Settings, FileClock, Lock, Activity, MonitorPlay, ClipboardCheck, Send, FileCode, BadgeCheck, DatabaseBackup,
} from 'lucide-react'

export type BadgeKey = 'alerts' | 'failed_scans' | 'unknown' | 'work_orders'

export interface NavLeaf {
  label: string
  to: string
  icon?: ComponentType<{ size?: number | string; className?: string }>
  badge?: BadgeKey
}
export interface NavItem {
  label: string
  icon: ComponentType<{ size?: number | string; className?: string }>
  to?: string
  badge?: BadgeKey
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
      { label: 'MIB Browser', icon: BookOpen, to: '/mibs' },
      { label: 'Credentials', icon: KeyRound, to: '/credentials' },
    ],
  },
  {
    title: 'Inventory',
    items: [
      { label: 'All Devices', icon: Boxes, to: '/inventory' },
      {
        label: 'Network Devices', icon: Layers,
        children: [
          { label: 'Switches', to: '/', icon: Network },
          { label: 'Firewalls', to: '/firewalls', icon: Flame },
          { label: 'Wireless', to: '/wlan', icon: Wifi },
        ],
      },
      {
        label: 'Compute', icon: Cpu,
        children: [
          { label: 'Servers', to: '/servers', icon: Server },
          { label: 'Virtual Hosts', to: '/virtual-hosts', icon: HardDrive },
        ],
      },
      {
        label: 'Endpoints & Peripherals', icon: MonitorSmartphone,
        children: [
          { label: 'Printers', to: '/printers', icon: Plug },
          { label: 'UPS', to: '/ups', icon: BatteryCharging },
        ],
      },
      {
        label: 'Security & Surveillance', icon: ShieldAlert,
        children: [
          { label: 'Cameras', to: '/cameras', icon: Camera },
          { label: 'NVRs', to: '/nvrs', icon: Video },
        ],
      },
      {
        label: 'Voice', icon: Phone,
        children: [{ label: 'PBX / Voice', to: '/pbx', icon: Phone }],
      },
      { label: 'Unknown Devices', icon: CircleHelp, to: '/unknown', badge: 'unknown' },
      { label: 'Device Intelligence', icon: Brain, to: '/device-intelligence' },
    ],
  },
  {
    title: 'Topology',
    items: [
      { label: 'Network Map', icon: Map, to: '/topology' },
      { label: 'Path Finder', icon: RouteIcon, to: '/path-finder' },
      { label: 'NetFlow', icon: Waypoints, to: '/netflow' },
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
      { label: 'Inventory Reports', icon: FileChartColumn, to: '/reports/inventory' },
      { label: 'Discovery Reports', icon: FileSearch, to: '/reports/discovery' },
      { label: 'Availability Reports', icon: ChartLine, to: '/reports/availability' },
      { label: 'Vendor Reports', icon: Tag, to: '/reports/vendors' },
      { label: 'Export Center', icon: Download, to: '/reports/export' },
    ],
  },
  {
    title: 'Administration',
    items: [
      { label: 'Users', icon: Users, to: '/access-control/users' },
      { label: 'Roles & Permissions', icon: ShieldCheck, to: '/access-control/roles' },
      { label: 'Device Templates', icon: LayoutTemplate, to: '/device-templates' },
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
