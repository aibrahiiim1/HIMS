// Shared topology/path palette — used by both the global Topology graph
// (TopologyPage) and the search-trace PathGraph so node/edge colours stay in sync.
// Concrete hex values (not CSS vars) because cytoscape renders to a canvas and does
// not resolve CSS custom properties.

// Topology layer → node colour (core/distribution/access/edge/gateway/wireless/host).
export const LAYER_COLOR: Record<string, string> = {
  core: '#8b5cf6',
  distribution: '#2563eb',
  access: '#22c55e',
  edge: '#ef4444',
  gateway: '#f59e0b',
  wireless: '#06b6d4',
  host: '#64748b',
}
export const layerColor = (l: string) => LAYER_COLOR[l] ?? '#64748b'

// Link confidence → edge styling.
export const CONF_COLOR: Record<string, string> = { high: '#16a34a', medium: '#f59e0b', low: '#ef4444' }

// Path-step role → node colour (the search-trace chain). Wireless roles lead the
// chain (client → AP → controller) ahead of the wired hops.
export const ROLE_COLOR: Record<string, string> = {
  wireless_client: '#06b6d4',
  ap: '#0ea5e9',
  wireless_controller: '#6366f1',
  endpoint: '#3b82f6',
  access: '#22c55e',
  uplink: '#38bdf8',
  distribution: '#2563eb',
  core: '#8b5cf6',
  gateway: '#f59e0b',
  firewall: '#ef4444',
}
export const roleColor = (r: string) => ROLE_COLOR[r] ?? '#64748b'

// Human label for a path-step role.
export const ROLE_LABEL: Record<string, string> = {
  wireless_client: 'Wi-Fi client',
  ap: 'Access point',
  wireless_controller: 'WLAN controller',
  endpoint: 'Endpoint',
  access: 'Access switch',
  uplink: 'Uplink switch',
  distribution: 'Distribution',
  core: 'Core switch',
  gateway: 'Gateway / Router',
  firewall: 'Firewall',
}
export const roleLabel = (r: string) => ROLE_LABEL[r] ?? r
