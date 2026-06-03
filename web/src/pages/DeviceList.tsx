import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Boxes, Wifi, WifiOff, Server, Radar } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, Kpi, StatusPill, EmptyState, colorFor } from '../components/ui'

interface Props {
  category: string
  title: string
  detailBase: string
}

const isOffline = (s: string) => ['down', 'offline', 'needs_attention'].includes((s || '').toLowerCase())

export function DeviceList({ category, title, detailBase }: Props) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', category],
    queryFn: () => api.get<Device[]>(`/devices?category=${category}`),
  })

  const all = data ?? []
  const online = all.filter((d) => (d.status || '').toLowerCase() === 'up').length
  const offline = all.filter((d) => isOffline(d.status)).length
  const vendors = useMemo(() => new Set(all.map((d) => d.vendor || 'Unknown')).size, [data])

  return (
    <div>
      <PageHeader title={title} subtitle={`Managed ${title.toLowerCase()} across the fleet`} icon={Boxes} />

      <div className="kpi-grid">
        <Kpi label={title} value={all.length} icon={Boxes} tone="info" />
        <Kpi label="Online" value={online} icon={Wifi} tone="ok" sub={all.length ? `${Math.round((online / Math.max(1, all.length)) * 100)}%` : '—'} />
        <Kpi label="Offline / Attention" value={offline} icon={WifiOff} tone={offline > 0 ? 'crit' : 'default'} />
        <Kpi label="Vendors" value={vendors} icon={Server} tone="default" sub="distinct" />
      </div>

      <Panel title={title} subtitle={`${all.length} device(s)`} pad={false}>
        {isLoading && <div className="loading">Loading {title.toLowerCase()}…</div>}
        {error && <div style={{ padding: 'var(--space-5)' }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && data.length === 0 && (
          <EmptyState
            icon={Radar}
            title={`No ${title.toLowerCase()} yet`}
            message="Run a discovery scan to populate this category, or add a device manually."
            action={<Link className="btn btn-primary btn-sm" to="/discovery">Start Discovery</Link>}
          />
        )}
        {data && data.length > 0 && (
          <table className="data-table">
            <thead>
              <tr><th>Device</th><th>IP</th><th>Vendor</th><th>Model</th><th>OS</th><th>Driver</th><th>Status</th></tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.id}>
                  <td>
                    <div className="dev-cell">
                      <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || d.category).charAt(0).toUpperCase()}</span>
                      <div className="dev-meta">
                        <Link className="cell-name" to={`${detailBase}/${d.id}`}>{d.name}</Link>
                        {d.hostname && <small>{d.hostname}</small>}
                      </div>
                    </div>
                  </td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  <td>{d.vendor ?? '—'}</td>
                  <td>{d.model ?? '—'}</td>
                  <td>{d.os_version ?? '—'}</td>
                  <td>{d.driver ?? '—'}</td>
                  <td><StatusPill status={d.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
