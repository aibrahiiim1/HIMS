import { useMemo, type ComponentType } from 'react'
import { useQuery } from '@tanstack/react-query'
import { HardDrive, Radar, ShieldCheck, Activity, MapPin } from 'lucide-react'
import { api, type Device, type Location, type MonitoringCheck, locationPaths } from '../api'
import { HealthRing, StatusPill, colorFor, timeAgo } from './ui'

function deviceHealth(checks: MonitoringCheck[]): { score: number; status: string } {
  if (checks.length === 0) return { score: 0, status: 'unknown' }
  const score = Math.round(checks.reduce((a, c) => {
    const s = (c.last_status || '').toLowerCase()
    return a + (s === 'up' ? 100 : s === 'warning' ? 50 : s === 'down' ? 0 : 60)
  }, 0) / checks.length)
  const status = checks.some((c) => c.last_status === 'down') ? 'down'
    : checks.some((c) => c.last_status === 'warning') ? 'warning'
    : checks.some((c) => c.last_status === 'up') ? 'up' : 'unknown'
  return { score, status }
}

/**
 * Shared device-detail header: identity, badges, health score and a summary
 * stat strip (monitoring, discovery, location, credential/driver). Reused by
 * every per-category detail page for a consistent enterprise device view.
 */
export function DeviceHeader({ deviceId, icon: Icon = HardDrive }: {
  deviceId: string; icon?: ComponentType<{ size?: number | string }>
}) {
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const checksQ = useQuery({ queryKey: ['dev-checks', deviceId], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${deviceId}/monitoring/checks`) })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  const d = (devices.data ?? []).find((x) => x.id === deviceId)
  const checks = checksQ.data ?? []
  const { score, status } = deviceHealth(checks)
  const effStatus = status !== 'unknown' ? status : (d?.status ?? 'unknown')

  if (!d) {
    return (
      <div className="panel"><div className="panel-body">
        <div className="dev-cell"><span className="dev-avatar" style={{ background: 'var(--neutral)' }}><Icon size={18} /></span>
          <div className="dev-meta"><span className="cell-name">{devices.isLoading ? 'Loading device…' : 'Device'}</span><small>{deviceId}</small></div>
        </div>
      </div></div>
    )
  }

  const badges: { label: string; value: string }[] = []
  if (d.vendor) badges.push({ label: 'Vendor', value: d.vendor })
  if (d.model) badges.push({ label: 'Model', value: d.model })
  if (d.os_version) badges.push({ label: 'OS', value: d.os_version })
  if (d.driver) badges.push({ label: 'Driver', value: d.driver })

  return (
    <div className="device-hero">
      <div className="device-hero-id">
        <span className="device-hero-avatar" style={{ background: colorFor(d.category) }}><Icon size={26} /></span>
        <div className="device-hero-text">
          <div className="device-hero-title">
            <h1>{d.name}</h1>
            <StatusPill status={effStatus} />
          </div>
          <div className="device-hero-sub">
            <span className="mono">{d.primary_ip ?? 'no IP'}</span>
            <span>·</span>
            <span style={{ textTransform: 'capitalize' }}>{d.category.replace(/_/g, ' ')}</span>
            {d.hostname && <><span>·</span><span>{d.hostname}</span></>}
          </div>
          <div className="device-hero-badges">
            {badges.map((b) => (
              <span key={b.label} className="hero-badge"><em>{b.label}</em>{b.value}</span>
            ))}
          </div>
        </div>
      </div>

      <div className="device-hero-metrics">
        {checks.length > 0 && <HealthRing score={score} size={84} label="Health" />}
        <div className="device-hero-stats">
          <div className="hero-stat"><span className="hero-stat-ico tone-info"><Activity size={15} /></span>
            <div><b>{checks.length}</b><small>monitor checks</small></div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><Radar size={15} /></span>
            <div><b>{timeAgo(d.last_discovery_at)}</b><small>last discovery</small></div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><MapPin size={15} /></span>
            <div><b>{d.location_id ? (locPath[d.location_id] ?? '—') : '—'}</b><small>location</small></div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><ShieldCheck size={15} /></span>
            <div><b>{d.driver ?? 'unbound'}</b><small>driver / credential</small></div></div>
        </div>
      </div>
    </div>
  )
}
