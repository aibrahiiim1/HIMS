import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  LayoutDashboard, Server, Wifi, WifiOff, Bell, ClipboardList, ShieldAlert,
  Radar, Activity, TriangleAlert, RefreshCw, Clock, Boxes, TrendingUp, Lock, KeyRound, HeartPulse, Network,
} from 'lucide-react'
import { api, type Device, type Alert, type DiscoveryJob, type MonitoringOverviewRow, type RoleSummaryRow, type ExpenseByCategory, type EncryptionStatus, type OperationalHealth, type InfrastructureHealth } from '../api'
import {
  PageHeader, Panel, Kpi, HealthRing, Donut, Legend, BarList, Sparkline,
  ActivityFeed, EmptyState, StatusPill, OperationalHealthPanel, colorFor, timeAgo,
} from '../components/ui'

// timeAgo for an ISO string, with "Never" for null.
const ago = (iso?: string | null) => (iso ? timeAgo(iso) : 'Never')

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

// Day-granularity label for security timestamps ("Today" / "45 days ago").
function dayLabel(iso?: string | null): string {
  if (!iso) return 'never'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const days = Math.floor((Date.now() - t) / 86_400_000)
  if (days <= 0) return 'Today'
  if (days === 1) return 'Yesterday'
  return `${days} days ago`
}

interface SecRow { label: string; value: React.ReactNode }

const SEC_BADGE: Record<string, string> = { healthy: 'badge-up', warning: 'badge-warning', critical: 'badge-down', unknown: 'badge-unknown' }
const OVERALL_LABEL: Record<string, string> = { excellent: 'Excellent', good: 'Good', needs_attention: 'Needs Attention', critical: 'Critical', unknown: 'Not enough data' }
const OVERALL_BADGE: Record<string, string> = { excellent: 'badge-up', good: 'badge-up', needs_attention: 'badge-warning', critical: 'badge-down', unknown: 'badge-unknown' }

function InfraHealthCard({ data }: { data?: InfrastructureHealth }) {
  if (!data) return <Panel title="Overall Infrastructure Health" icon={Activity}><div className="loading">Loading…</div></Panel>
  const o = data.overall
  const confCls = o.confidence === 'high' ? 'badge-up' : o.confidence === 'limited' ? 'badge-warning' : 'badge-unknown'
  return (
    <Panel title="Overall Infrastructure Health" icon={Activity} actions={<span className={`badge ${OVERALL_BADGE[o.status] ?? 'badge-unknown'}`}>{OVERALL_LABEL[o.status] ?? o.status}</span>}>
      <div className="infra-card">
        <div className="infra-score">
          {o.confidence === 'unknown'
            ? <div style={{ fontSize: 40, fontWeight: 800, color: 'var(--text-faint)' }}>—</div>
            : <HealthRing score={o.score} size={120} label="Score" />}
          <span className="infra-score-label">{OVERALL_LABEL[o.status] ?? o.status}</span>
        </div>
        <div className="infra-sections">
          {data.sections.map((s) => (
            <div key={s.name} className="infra-sec-row">
              <span className="muted">{s.name}{!s.included && s.reason ? <small> — {s.reason}</small> : null}</span>
              <span className={`badge ${SEC_BADGE[s.status] ?? 'badge-unknown'}`}>{s.status === 'unknown' ? 'Not collected' : s.status}</span>
            </div>
          ))}
          <div className="infra-conf">
            <span className="muted" style={{ fontSize: 12 }}>Confidence:</span>
            <span className={`badge ${confCls}`} style={{ textTransform: 'capitalize' }}>{o.confidence}</span>
            {o.confidence === 'limited' && o.limited_reasons.length > 0 && (
              <span className="infra-reason">Reason: {o.limited_reasons.join('; ')}</span>
            )}
          </div>
        </div>
      </div>
    </Panel>
  )
}

export function Dashboard() {
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get<DashboardData>('/dashboard'), refetchInterval: 30_000 })
  const mon = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'), refetchInterval: 30_000 })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'), refetchInterval: 30_000 })
  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 30_000 })
  const enc = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), refetchInterval: 60_000, retry: 0 })
  const oph = useQuery({ queryKey: ['operational-health'], queryFn: () => api.get<OperationalHealth>('/dashboard/operational-health'), refetchInterval: 30_000, retry: 0 })
  const infra = useQuery({ queryKey: ['infra-health'], queryFn: () => api.get<InfrastructureHealth>('/dashboard/infrastructure-health'), refetchInterval: 30_000, retry: 0 })

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

      <InfraHealthCard data={infra.data} />

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
          <Panel title="Security Health" icon={Lock} actions={<Link className="btn btn-ghost btn-sm" to="/security/encryption">Manage</Link>}>
            {(() => {
              const e = enc.data
              const health = !e ? { label: 'Unknown', cls: 'badge-unknown' }
                : e.status === 'enabled' && e.fingerprint_match && e.undecryptable_count === 0 ? { label: 'Healthy', cls: 'badge-up' }
                : e.status === 'enabled' ? { label: 'Degraded', cls: 'badge-warning' }
                : e.status === 'pending_restart' ? { label: 'Pending restart', cls: 'badge-warning' }
                : { label: 'Not configured', cls: 'badge-down' }
              const rows: SecRow[] = [
                { label: 'Encryption', value: <span className={`badge ${health.cls}`}>{health.label}</span> },
                { label: 'Credentials', value: e?.encrypted_count ?? '—' },
                { label: 'Needs Re-entry', value: <span style={{ color: (e?.needs_reset_count ?? 0) > 0 ? 'var(--crit)' : 'var(--text)', fontWeight: 600 }}>{e?.needs_reset_count ?? 0}</span> },
                { label: 'Last Validation', value: dayLabel(e?.last_validation_at) },
                { label: 'Last Rotation', value: dayLabel(e?.last_rotation_at) },
              ]
              return (
                <ul className="sec-health">
                  {rows.map((r) => (
                    <li key={r.label}><span className="muted">{r.label}</span><span className="sec-val">{r.value}</span></li>
                  ))}
                </ul>
              )
            })()}
            {enc.data && enc.data.status === 'missing' && (
              <div style={{ marginTop: 10 }}><Link className="badge badge-down" to="/security/encryption" style={{ textDecoration: 'none' }}><KeyRound size={11} style={{ verticalAlign: -1 }} /> Configure encryption →</Link></div>
            )}
          </Panel>
          {(() => {
            const o = oph.data
            const d = o?.discovery, m = o?.monitoring, tp = o?.topology
            const a = infra.data?.alerts
            return (
              <>
                <OperationalHealthPanel
                  title="Discovery Health" icon={Radar} status={d?.status ?? 'unknown'}
                  notCollectedReason="No discovery scans have run yet — launch a scan to populate inventory."
                  rows={d ? [
                    { label: 'Last Scan', value: ago(d.last_scan_at) },
                    { label: 'Last Scan Status', value: <span style={{ textTransform: 'capitalize' }}>{d.last_scan_status}</span> },
                    { label: 'Successful Scans', value: d.successful_scan_percent == null ? 'Not collected yet' : `${d.successful_scan_percent}%` },
                    { label: 'Failed Scans', value: <span style={{ color: d.failed_scan_count > 0 ? 'var(--crit)' : undefined }}>{d.failed_scan_count}</span> },
                    { label: 'Credential Failures', value: d.credential_failure_count == null ? 'Not collected yet' : d.credential_failure_count },
                    { label: 'Pending Jobs', value: d.pending_job_count },
                  ] : []}
                  impact="Discovery keeps the inventory current; failures mean devices may be missing or stale."
                  action={<Link className="btn btn-ghost btn-sm" to="/discovery">Open Discovery Center →</Link>}
                />
                <OperationalHealthPanel
                  title="Monitoring Health" icon={HeartPulse} status={m?.status ?? 'unknown'}
                  notCollectedReason="No monitoring checks are configured — seed checks to track availability."
                  rows={m ? [
                    { label: 'Monitored Devices', value: m.monitored_devices },
                    { label: 'Online', value: m.online_devices },
                    { label: 'Offline', value: <span style={{ color: m.offline_devices > 0 ? 'var(--crit)' : undefined }}>{m.offline_devices}</span> },
                    { label: 'Critical Alerts', value: <span style={{ color: m.critical_alerts > 0 ? 'var(--crit)' : undefined }}>{m.critical_alerts}</span> },
                    { label: 'Warning Alerts', value: m.warning_alerts },
                    { label: 'Last Collection', value: ago(m.last_collection_at) },
                    { label: 'Collection Status', value: <span style={{ textTransform: 'capitalize' }}>{m.collection_status}</span> },
                  ] : []}
                  impact={m && !m.last_collection_at ? 'Monitoring collection has not run yet — run a sweep to populate availability.' : 'Monitoring detects outages; stale collection or offline devices need attention.'}
                  action={<Link className="btn btn-ghost btn-sm" to="/monitoring">Open Monitoring →</Link>}
                />
                <OperationalHealthPanel
                  title="Topology Health" icon={Network} status={tp?.status ?? 'unknown'}
                  notCollectedReason="No topology links computed yet — discover switches to gather LLDP/CDP neighbors."
                  rows={tp ? [
                    { label: 'Mapped Devices', value: tp.mapped_devices },
                    { label: 'Unmapped Devices', value: tp.unmapped_devices },
                    { label: 'Missing Neighbors', value: tp.missing_neighbors },
                    { label: 'Topology Coverage', value: tp.coverage_percent == null ? 'Not collected yet' : `${tp.coverage_percent}%` },
                    { label: 'LLDP/CDP Data Age', value: ago(tp.lldp_cdp_data_age) },
                    { label: 'Last Refresh', value: ago(tp.last_topology_refresh_at) },
                  ] : []}
                  impact="Topology reflects physical links; low coverage means the network map is incomplete."
                  action={<Link className="btn btn-ghost btn-sm" to="/topology">Open Topology →</Link>}
                />
                <OperationalHealthPanel
                  title="Alert Health" icon={Bell} status={a?.status ?? 'unknown'}
                  notCollectedReason="Alert data is unavailable."
                  rows={a ? [
                    { label: 'Open Critical Alerts', value: <span style={{ color: a.open_critical > 0 ? 'var(--crit)' : undefined }}>{a.open_critical}</span> },
                    { label: 'Open Warning Alerts', value: a.open_warning },
                    { label: 'Acknowledged', value: a.acknowledged },
                    { label: 'Unresolved', value: a.unresolved },
                    { label: 'Last Alert', value: ago(a.last_alert_at) },
                    { label: 'Active Rules', value: a.active_rules },
                  ] : []}
                  impact="Unresolved critical alerts mean active incidents needing attention."
                  action={<Link className="btn btn-ghost btn-sm" to="/alerts">Open Alerts →</Link>}
                />
              </>
            )
          })()}
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
