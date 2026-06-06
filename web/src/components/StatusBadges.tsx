import { Link } from 'react-router-dom'
import { reachBadge, mgmtBadge, type Device } from '../api'

// Two SEPARATE badges, used everywhere a device status is shown. Reachability
// (Online/Offline/…) and Management (Managed/Unmanaged/Needs action/…) are never
// merged into a single badge — that conflation is exactly what this replaces.

export function ReachabilityBadge({ value }: { value?: string }) {
  const b = reachBadge(value)
  return <span className={`badge ${b.cls}`} title={`Reachability: ${b.label}`}>{b.label}</span>
}

export function ManagementBadge({ value, managedBy }: { value?: string; managedBy?: string[] }) {
  const b = mgmtBadge(value)
  const by = value === 'managed' && managedBy && managedBy.length ? ` via ${managedBy.map((p) => p.toUpperCase()).join(', ')}` : ''
  return <span className={`badge ${b.cls}`} title={`Management: ${b.label}${by}`}>{b.label}</span>
}

// StatusBadges renders both axes side by side for a device row/header.
export function StatusBadges({ d, linkUnmanaged }: { d: Pick<Device, 'reachability' | 'management' | 'managed_by' | 'previously_managed'>; linkUnmanaged?: boolean }) {
  const mgmt = <ManagementBadge value={d.management} managedBy={d.managed_by} />
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
