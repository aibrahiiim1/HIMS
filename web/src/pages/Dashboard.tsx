import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  LayoutDashboard, Server, Wifi, WifiOff, Bell, ClipboardList, ShieldAlert,
  Radar, Activity, TriangleAlert, RefreshCw, Clock, Boxes, TrendingUp,
} from 'lucide-react'
import { api, type Device, type Alert, type DiscoveryJob, type MonitoringOverviewRow, type RoleSummaryRow, type ExpenseByCategory } from '../api'
import {
  PageHeader, Panel, Kpi, HealthRing, Donut, Legend, BarList, Sparkline,
  ActivityFeed, EmptyState, StatusPill, colorFor, timeAgo,
} from '../components/ui'

interface CountRow { category?: string; status?: string; count: number }
interface DashboardData {
  by_category?: CountRow[]
  by_status?: CountRow[]
  by_role?: RoleSummaryRow[]
  monitoring?: MonitoringOverviewRow[]
  expenses_by_category?: ExpenseByCategory[]
  headline?: {
    open_work_orders?: number
    open_alerts?: number
    expiring_systems?: number
    devices_needing_attention?: number
    total_expenses?: number
  }
}

const STATUS_DONUT_COLOR: Record<string, string> = {
  up: '#16a34a', warning: '#d97706', down: '#dc2626', unknown: '#94a3b8',
}

export function Dashboard() {
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get<DashboardData>('/dashboard'), refetchInterval: 30_000 })
  const mon = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'), refetchInterval: 30_000 })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'), refetchInterval: 30_000 })
  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 30_000 })

  const h = dash.data?.headline ?? {}
  const devs = devices.data ?? []
  const total = devs.length

  // ---- Monitoring rollup → online/offline + health score ----
  const monMap = new Map((mon.data ?? []).map((r) => [r.status, r.count]))
  const up = monMap.get('up') ?? 0
  const warning = monMap.get('warning') ?? 0
  const down = monMap.get('down') ?? 0
  const unknown = monMap.get('unknown') ?? 0
  const monitored = up + warning + down
  const health = monitored > 0 ? Math.round(((up + warning * 0.5) / monitored) * 100) : (total > 0 ? 100 : 0)
  const statusDonut = [
    { label: 'Online', value: up, color: STATUS_DONUT_COLOR.up },
    { label: 'Warning', value: warning, color: STATUS_DONUT_COLOR.warning },
    { label: 'Offline', value: down, color: STATUS_DONUT_COLOR.down },
    { label: 'Unknown', value: unknown, color: STATUS_DONUT_COLOR.unknown },
  ].filter((d) => d.value > 0)

  // ---- Device-type donut + top vendors ----
  const byType = Object.entries(devs.reduce<Record<string, number>>((m, d) => { m[d.category] = (m[d.category] ?? 0) + 1; return m }, {}))
    .sort((a, b) => b[1] - a[1])
  const typeDonut = byType.slice(0, 7).map(([label, value]) => ({ label: label.replace(/_/g, ' '), value, color: colorFor(label) }))
  const topVendors = Object.entries(devs.reduce<Record<string, number>>((m, d) => { const v = d.vendor || 'Unknown'; m[v] = (m[v] ?? 0) + 1; return m }, {}))
    .sort((a, b) => b[1] - a[1]).slice(0, 6).map(([label, value]) => ({ label, value, color: colorFor(label) }))

  // ---- Critical assets (offline / needs attention) ----
  const critical = devs.filter((d) => ['down', 'needs_attention', 'offline'].includes((d.status || '').toLowerCase())).slice(0, 6)

  // ---- Discovery activity ----
  const recentJobs = [...(jobs.data ?? [])].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  const discoveryTrend = [...recentJobs].reverse().slice(-12).map((j) => j.found_count)
  const lastJob = recentJobs[0]

  // ---- Recent activity feed (alerts + discovery jobs merged) ----
  const feed = [
    ...(alerts.data ?? []).map((a) => ({
      ts: a.opened_at,
      icon: a.severity === 'critical' ? TriangleAlert : Bell,
      tone: (a.severity === 'critical' ? 'crit' : a.severity === 'warning' ? 'warn' : 'info') as 'crit' | 'warn' | 'info',
      title: a.message,
      meta: `Alert · ${a.severity} · ${a.status}`,
    })),
    ...recentJobs.slice(0, 8).map((j) => ({
      ts: j.created_at,
      icon: Radar,
      tone: (j.status === 'failed' ? 'crit' : j.status === 'running' ? 'info' : 'ok') as 'crit' | 'info' | 'ok',
      title: `Discovery scan ${j.status}${j.scope_cidr ? ` · ${j.scope_cidr}` : ''}`,
      meta: `${j.found_count} found of ${j.host_count} host(s)`,
    })),
  ].sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime()).slice(0, 9)
    .map((f) => ({ icon: f.icon, tone: f.tone, title: f.title, meta: f.meta, time: timeAgo(f.ts) }))

  const detailBase: Record<string, string> = { switch: '/devices', server: '/servers', firewall: '/firewalls', camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx', virtual_host: '/virtual-hosts' }

  return (
    <div>
      <PageHeader
        title="Executive Dashboard"
        subtitle="Fleet-wide health, discovery, and operations at a glance"
        icon={LayoutDashboard}
        actions={
          <>
            <span className="muted" style={{ fontSize: 12 }}><Clock size={12} style={{ verticalAlign: -1 }} /> Updated {timeAgo(new Date().toISOString())}</span>
            <button className="btn btn-ghost btn-sm" onClick={() => { dash.refetch(); mon.refetch(); devices.refetch(); jobs.refetch(); alerts.refetch() }}>
              <RefreshCw size={14} /> Refresh
            </button>
          </>
        }
      />

      {/* KPI row */}
      <div className="kpi-grid">
        <Kpi label="Total Devices" value={total} icon={Boxes} tone="info" sub={`${byType.length} categories`} />
        <Kpi label="Online" value={up} icon={Wifi} tone="ok" sub={monitored > 0 ? `${Math.round((up / monitored) * 100)}% of monitored` : 'no checks'} />
        <Kpi label="Offline" value={down} icon={WifiOff} tone={down > 0 ? 'crit' : 'default'} sub={warning > 0 ? `${warning} warning` : 'all clear'} />
        <Kpi label="Active Alerts" value={h.open_alerts ?? 0} icon={Bell} tone={(h.open_alerts ?? 0) > 0 ? 'crit' : 'default'} sub="unresolved" />
        <Kpi label="Open Work Orders" value={h.open_work_orders ?? 0} icon={ClipboardList} tone={(h.open_work_orders ?? 0) > 0 ? 'warn' : 'default'} sub="in progress" />
        <Kpi label="Expiring Systems" value={h.expiring_systems ?? 0} icon={ShieldAlert} tone={(h.expiring_systems ?? 0) > 0 ? 'warn' : 'default'} sub="next 90 days" />
      </div>

      <div className="grid-side">
        {/* Main column */}
        <div className="stack">
          <Panel title="Fleet Health & Availability" icon={Activity}>
            <div className="row" style={{ alignItems: 'center', gap: 32 }}>
              <HealthRing score={health} label="Health Score" />
              {statusDonut.length > 0 ? (
                <>
                  <Donut data={statusDonut} centerValue={monitored} centerLabel="monitored" />
                  <div style={{ flex: 1, minWidth: 160 }}><Legend data={statusDonut} total={monitored} /></div>
                </>
              ) : (
                <div style={{ flex: 1 }}>
                  <EmptyState icon={Activity} title="No monitoring checks yet" message="Seed monitoring checks to track device availability and compute a health score." action={<Link className="btn btn-primary btn-sm" to="/monitoring">Go to Monitoring</Link>} />
                </div>
              )}
            </div>
          </Panel>

          <div className="grid-2">
            <Panel title="Devices by Type" icon={Boxes}>
              {typeDonut.length > 0 ? (
                <div className="row" style={{ alignItems: 'center', gap: 20 }}>
                  <Donut data={typeDonut} centerValue={total} centerLabel="devices" size={140} />
                  <div style={{ flex: 1, minWidth: 140 }}><Legend data={typeDonut} total={total} /></div>
                </div>
              ) : <div className="muted">No devices yet.</div>}
            </Panel>

            <Panel title="Top Vendors" icon={Server}>
              <BarList rows={topVendors} />
            </Panel>
          </div>

          <Panel
            title="Discovery Activity" icon={Radar}
            actions={<Link className="btn btn-ghost btn-sm" to="/discovery">Open Discovery</Link>}
          >
            <div className="row-between" style={{ marginBottom: 12 }}>
              <div>
                <div className="muted" style={{ fontSize: 12 }}>Devices found per recent scan</div>
                {lastJob && <div style={{ fontSize: 13, marginTop: 2 }}>Last scan {timeAgo(lastJob.created_at)} · <StatusPill status={lastJob.status === 'completed' ? 'up' : lastJob.status === 'failed' ? 'down' : 'warning'} label={lastJob.status} /></div>}
              </div>
              {discoveryTrend.length > 1 && <Sparkline points={discoveryTrend} width={180} height={44} color="var(--brand)" />}
            </div>
            {recentJobs.length === 0
              ? <EmptyState icon={Radar} title="No scans run yet" message="Launch a discovery scan to populate your inventory." action={<Link className="btn btn-primary btn-sm" to="/discovery">Start Discovery</Link>} />
              : (
                <table className="data-table">
                  <thead><tr><th>Scope</th><th>Status</th><th>Hosts</th><th>Found</th><th>When</th></tr></thead>
                  <tbody>
                    {recentJobs.slice(0, 5).map((j) => (
                      <tr key={j.id}>
                        <td className="mono">{j.scope_cidr || '—'}</td>
                        <td><StatusPill status={j.status === 'completed' ? 'up' : j.status === 'failed' ? 'down' : 'warning'} label={j.status} /></td>
                        <td>{j.host_count}</td>
                        <td>{j.found_count}</td>
                        <td className="muted">{timeAgo(j.created_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
          </Panel>
        </div>

        {/* Side column */}
        <div className="stack">
          <Panel title="Critical Assets" icon={TriangleAlert} actions={critical.length > 0 ? <Link className="btn btn-ghost btn-sm" to="/monitoring">View all</Link> : undefined}>
            {critical.length === 0
              ? <EmptyState icon={Wifi} title="All systems operational" message="No devices are offline or flagged for attention." />
              : (
                <ul className="activity">
                  {critical.map((d) => {
                    const base = detailBase[d.category]
                    return (
                      <li key={d.id} className="activity-item">
                        <span className="activity-dot tone-crit"><WifiOff size={13} /></span>
                        <div className="activity-body">
                          <div className="activity-title">{base ? <Link to={`${base}/${d.id}`}>{d.name}</Link> : d.name}</div>
                          <div className="activity-meta">{d.primary_ip || '—'} · {d.category.replace(/_/g, ' ')}</div>
                        </div>
                        <StatusPill status={d.status} />
                      </li>
                    )
                  })}
                </ul>
              )}
          </Panel>

          <Panel title="Recent Activity" icon={TrendingUp}>
            <ActivityFeed items={feed} />
          </Panel>
        </div>
      </div>
    </div>
  )
}
