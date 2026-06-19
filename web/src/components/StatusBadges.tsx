import { Link } from 'react-router-dom'
import { reachBadge, mgmtBadge, type Device } from '../api'

// Two SEPARATE badges, used everywhere a device status is shown. Reachability
// (Online/Offline/…) and Management (Managed/Unmanaged/Needs action/…) are never
// merged into a single badge — that conflation is exactly what this replaces.

export function ReachabilityBadge({ value }: { value?: string }) {
  const b = reachBadge(value)
  return <span className={`badge ${b.cls}`} title={`Reachability: ${b.label}`}>{b.label}</span>
}

// Short, human reason hints appended to the management badge tooltip so the precise cause
// (e.g. a broken host WMI repository) is visible on hover wherever the badge appears.
const REASON_HINT: Record<string, string> = {
  wmi_namespace_broken: 'host WMI repository (root\\cimv2) broken — repair WMI on the host',
  transport_unreachable: 'agent could not reach WinRM/RPC — check host firewall/listener',
}

export function ManagementBadge({ value, managedBy, reason }: { value?: string; managedBy?: string[]; reason?: string }) {
  const b = mgmtBadge(value)
  const by = value === 'managed' && managedBy && managedBy.length ? ` via ${managedBy.map((p) => p.toUpperCase()).join(', ')}` : ''
  const hint = reason && REASON_HINT[reason] ? ` — ${REASON_HINT[reason]}` : ''
  return <span className={`badge ${b.cls}`} title={`Management: ${b.label}${by}${hint}`}>{b.label}</span>
}

// StatusBadges renders both axes side by side for a device row/header.
export function StatusBadges({ d, linkUnmanaged }: { d: Pick<Device, 'reachability' | 'management' | 'managed_by' | 'previously_managed' | 'management_reason'>; linkUnmanaged?: boolean }) {
  const mgmt = <ManagementBadge value={d.management} managedBy={d.managed_by} reason={d.management_reason} />
  return (
    <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
      <ReachabilityBadge value={d.reachability} />
      {d.previously_managed
        ? <span className="badge badge-unknown" title="Offline now, but has a working management method on record">Managed (was)</span>
        : linkUnmanaged && d.management !== 'managed'
          ? <Link to={managementLink(d.management)} style={{ textDecoration: 'none' }}>{mgmt}</Link>
          : mgmt}
    </span>
  )
}

// managementLink routes a management state to the bookmarkable Inventory filter.
function managementLink(state?: string): string {
  if (!state || state === 'managed') return '/inventory?management=managed'
  return `/inventory?management=${state}`
}
