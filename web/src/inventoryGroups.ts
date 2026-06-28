// Single source of truth for inventory grouping. The sidebar, the "All <Group>" pages,
// the per-category child pages, and the sidebar counts are ALL derived from this — so a
// sidebar count always reconciles with the page it links to (a group = the union of its
// categories; a child = its own categories). No device is duplicated: each device has one
// category, and a group is a disjoint union of categories.

export interface InvChild {
  label: string
  path: string
  categories: string[]
  special?: 'bmc' // a child with a bespoke page instead of the generic grid
  manualClassify?: boolean // expose the operator manual-classify action on this page
  onboardType?: string // manual-onboarding registry key for this page's "Add <Type>" button
}
export interface InvGroup {
  key: string
  label: string // e.g. "Compute"
  allLabel: string // e.g. "All Compute"
  allPath: string // e.g. "/inventory/compute"
  categories: string[] // union of all child categories (drives the All page + group count)
  children: InvChild[]
  manualClassify?: boolean // expose the manual-classify action on the All page
}

export const INVENTORY_GROUPS: InvGroup[] = [
  {
    key: 'network',
    label: 'Network',
    allLabel: 'All Network',
    allPath: '/inventory/network',
    categories: ['switch', 'router', 'isp_router', 'firewall', 'wireless_controller', 'access_point'],
    children: [
      { label: 'Switches', path: '/inventory/network/switches', categories: ['switch'] , onboardType: 'switch' },
      { label: 'Routers', path: '/inventory/network/routers', categories: ['router', 'isp_router'] , onboardType: 'router' },
      { label: 'Firewalls', path: '/inventory/network/firewalls', categories: ['firewall'] , onboardType: 'firewall' },
      { label: 'Wireless Controllers', path: '/inventory/network/wireless-controllers', categories: ['wireless_controller'] , onboardType: 'wireless_controller' },
      { label: 'Access Points', path: '/inventory/network/access-points', categories: ['access_point'] , onboardType: 'access_point' },
    ],
  },
  {
    key: 'compute',
    label: 'Compute',
    allLabel: 'All Compute',
    allPath: '/inventory/compute',
    categories: ['server', 'virtual_host', 'virtual_machine', 'bmc'],
    children: [
      { label: 'Servers', path: '/inventory/compute/servers', categories: ['server'] , onboardType: 'server' },
      { label: 'Virtual Hosts', path: '/inventory/compute/virtual-hosts', categories: ['virtual_host', 'virtual_machine'] , onboardType: 'virtual_host_esxi' },
      { label: 'iLO / BMC / iDRAC', path: '/inventory/compute/bmc', categories: ['bmc'], special: 'bmc' },
    ],
  },
  {
    key: 'endpoints',
    label: 'Endpoints',
    allLabel: 'All Endpoints',
    allPath: '/inventory/endpoints',
    categories: ['endpoint', 'printer', 'biometric', 'biometric_device_unclassified', 'pos', 'pos_device_unclassified', 'ups'],
    manualClassify: true,
    children: [
      { label: 'Workstations', path: '/inventory/endpoints/workstations', categories: ['endpoint'], manualClassify: true , onboardType: 'endpoint' },
      { label: 'Printers', path: '/inventory/endpoints/printers', categories: ['printer'] , onboardType: 'printer' },
      { label: 'Biometric Devices', path: '/inventory/endpoints/biometric', categories: ['biometric', 'biometric_device_unclassified'], manualClassify: true , onboardType: 'biometric_zkteco' },
      { label: 'Point of Sale', path: '/inventory/endpoints/pos', categories: ['pos', 'pos_device_unclassified'], manualClassify: true , onboardType: 'pos' },
      { label: 'UPS', path: '/inventory/endpoints/ups', categories: ['ups'] , onboardType: 'ups' },
    ],
  },
  {
    key: 'security',
    label: 'Security & Surveillance',
    allLabel: 'All Security & Surveillance',
    allPath: '/inventory/security',
    categories: ['camera', 'nvr', 'dvr'],
    children: [
      { label: 'Cameras', path: '/inventory/security/cameras', categories: ['camera'] , onboardType: 'camera' },
      { label: 'NVR / DVR', path: '/inventory/security/nvr', categories: ['nvr', 'dvr'] , onboardType: 'nvr' },
    ],
  },
  {
    key: 'voice',
    label: 'Voice',
    allLabel: 'All Voice',
    allPath: '/inventory/voice',
    categories: ['pbx', 'voice_gateway', 'ip_phone'],
    children: [
      { label: 'PBX / Call Managers', path: '/inventory/voice/pbx', categories: ['pbx', 'voice_gateway'] , onboardType: 'pbx' },
      { label: 'IP Phones', path: '/inventory/voice/phones', categories: ['ip_phone'] , onboardType: 'ip_phone' },
    ],
  },
]

// DEVICE_TYPE_LABEL: human label for a device category, used as the "Device Type" column
// and the type filter. Honest about the *_unclassified states (evidence present, exact
// type unknown) — never relabelled as something more specific than the evidence supports.
const DEVICE_TYPE_LABEL: Record<string, string> = {
  bmc: 'iLO / BMC',
  biometric: 'Biometric',
  biometric_device_unclassified: 'Biometric (unclassified)',
  pos: 'POS',
  pos_device_unclassified: 'POS (unclassified)',
  network_device_unclassified: 'Network device (unclassified)',
  virtual_host: 'Virtual Host',
  virtual_machine: 'Virtual Machine',
  isp_router: 'ISP Router',
  wireless_controller: 'Wireless Controller',
  access_point: 'Access Point',
  ip_phone: 'IP Phone',
  nvr: 'NVR',
  dvr: 'DVR',
  ups: 'UPS',
  pbx: 'PBX',
  voice_gateway: 'Voice Gateway',
}

export function deviceTypeLabel(category?: string): string {
  if (!category) return 'Unknown'
  if (DEVICE_TYPE_LABEL[category]) return DEVICE_TYPE_LABEL[category]
  return category.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

// Sum the device counts for a set of categories from a {category: count} map.
export function sumCounts(counts: Record<string, number> | undefined, categories: string[]): number {
  if (!counts) return 0
  return categories.reduce((n, c) => n + (counts[c] || 0), 0)
}

// PATH_CATEGORIES maps a sidebar/route path to the categories its page shows, so the
// sidebar can render a count that EXACTLY equals the page's row count (both derive from
// the same {category: count} source). Covers the data-driven group/child pages here; the
// legacy per-type routes (/servers, /firewalls, …) are intentionally not counted.
export const PATH_CATEGORIES: Record<string, string[]> = (() => {
  const m: Record<string, string[]> = {}
  for (const g of INVENTORY_GROUPS) {
    m[g.allPath] = g.categories
    for (const c of g.children) m[c.path] = c.categories
  }
  return m
})()
