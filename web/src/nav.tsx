import type { ComponentType } from 'react'
import {
  LayoutDashboard, MonitorDot, Bell, Search, Activity,
  HeartPulse, Network, Clock, Gauge, ScrollText,
  Radar, ListChecks, History, FileSearch, BookOpen, KeyRound,
  Boxes, Layers, Cpu, Server, HardDrive, Plug, BatteryCharging,
  ShieldAlert, Camera, Video, Phone, CircleHelp,
  Map, Route, Cable, Share2, Network as NetworkIcon,
  Wrench, ClipboardList, Package, DollarSign, CalendarClock,
  Building2,
  FileChartColumn, FileSearch as FileSearchIcon, ChartLine, Tag, Download,
  Users, ShieldCheck, LayoutTemplate, ScanLine, Settings, FileClock,
  Wifi, Flame, MonitorSmartphone,
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

// Placeholder path for not-yet-built pages — routed to <ComingSoon/>.
const soon = (slug: string) => `/soon/${slug}`

export const NAV: NavGroup[] = [
  {
    title: 'Overview',
    items: [
      { label: 'Dashboard', icon: LayoutDashboard, to: '/dashboard' },
      { label: 'NOC View', icon: MonitorDot, to: soon('noc-view') },
      { label: 'Alerts', icon: Bell, to: '/alerts', badge: 'alerts' },
      { label: 'Search', icon: Search, to: '/search' },
      { label: 'Recent Activity', icon: Activity, to: soon('recent-activity') },
    ],
  },
  {
    title: 'Monitoring',
    items: [
      { label: 'Health Overview', icon: HeartPulse, to: '/monitoring' },
      { label: 'Interfaces', icon: Network, to: soon('interfaces') },
      { label: 'Uptime', icon: Clock, to: soon('uptime') },
      { label: 'Performance Metrics', icon: Gauge, to: soon('performance-metrics') },
      { label: 'Events / Logs', icon: ScrollText, to: soon('events-logs') },
    ],
  },
  {
    title: 'Discovery',
    items: [
      { label: 'Discovery Center', icon: Radar, to: '/discovery' },
      { label: 'Scan Jobs', icon: ListChecks, to: soon('scan-jobs'), badge: 'failed_scans' },
      { label: 'Probe History', icon: History, to: soon('probe-history') },
      { label: 'Scan Results', icon: FileSearch, to: soon('scan-results') },
      { label: 'MIB Browser', icon: BookOpen, to: '/mibs' },
      {
        label: 'Credentials', icon: KeyRound,
        children: [
          { label: 'Credential Vault', to: '/credentials' },
          { label: 'Credential Groups', to: soon('credential-groups') },
          { label: 'Site Assignments', to: soon('site-assignments') },
          { label: 'Test Credentials', to: soon('test-credentials') },
          { label: 'Credential Testing', to: soon('credential-testing') },
        ],
      },
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
        children: [
          { label: 'PBX / Voice', to: '/pbx', icon: Phone },
        ],
      },
      { label: 'Unknown Devices', icon: CircleHelp, to: '/unknown', badge: 'unknown' },
    ],
  },
  {
    title: 'Topology',
    items: [
      { label: 'Network Map', icon: Map, to: '/topology' },
      { label: 'Device Path Finder', icon: Route, to: soon('path-finder') },
      { label: 'Switch Port Mapping', icon: Cable, to: soon('port-mapping') },
      { label: 'VLAN Map', icon: Share2, to: soon('vlan-map') },
      { label: 'LLDP / CDP Neighbors', icon: NetworkIcon, to: soon('neighbors') },
    ],
  },
  {
    title: 'Operations',
    items: [
      { label: 'Work Orders', icon: ClipboardList, to: '/work-orders', badge: 'work_orders' },
      { label: 'Systems', icon: Wrench, to: '/systems' },
      { label: 'Spare Parts', icon: Package, to: '/spare-parts' },
      { label: 'Expenses', icon: DollarSign, to: '/expenses' },
      { label: 'External Maintenance', icon: CalendarClock, to: soon('external-maintenance') },
    ],
  },
  {
    title: 'Organization',
    items: [
      { label: 'Locations', icon: Building2, to: '/locations' },
    ],
  },
  {
    title: 'Reports',
    items: [
      { label: 'Inventory Reports', icon: FileChartColumn, to: '/reports/inventory' },
      { label: 'Discovery Reports', icon: FileSearchIcon, to: '/reports/discovery' },
      { label: 'Availability Reports', icon: ChartLine, to: '/reports/availability' },
      { label: 'Vendor Reports', icon: Tag, to: '/reports/vendors' },
      { label: 'Export Center', icon: Download, to: '/reports/export' },
    ],
  },
  {
    title: 'Administration',
    items: [
      { label: 'Users', icon: Users, to: soon('users') },
      { label: 'Roles & Permissions', icon: ShieldCheck, to: '/roles' },
      { label: 'Device Templates', icon: LayoutTemplate, to: soon('device-templates') },
      { label: 'Vendor Fingerprints', icon: ScanLine, to: soon('vendor-fingerprints') },
      { label: 'System Settings', icon: Settings, to: '/settings' },
      { label: 'Audit Log', icon: FileClock, to: soon('audit-log') },
    ],
  },
]
