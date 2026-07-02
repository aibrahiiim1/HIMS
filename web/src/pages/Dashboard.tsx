import { useState, type ComponentType } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import {
  LayoutDashboard, Server, Wifi, WifiOff, Bell, ClipboardList, ShieldAlert,
  Radar, Activity, TriangleAlert, RefreshCw, Clock, Boxes, TrendingUp, KeyRound, HeartPulse,
  ShieldCheck, Building2, Layers,
} from 'lucide-react'
import { api, type Device, type Alert, type DiscoveryJob, type MonitoringOverviewRow, type MonitoringCheck, type RoleSummaryRow, type ExpenseByCategory, type InfrastructureHealth, type AvailabilityAnalytics, type DeviceUptime, type SiteRollup, type ActionRequired } from '../api'
import {
  PageHeader, Panel, Kpi, HealthRing, Donut, Legend, BarList, Sparkline, AreaChart,
  ActivityFeed, EmptyState, StatusPill, colorFor, timeAgo, InfoHint,
} from '../components/ui'
import { ManagementAccessCoverage } from '../components/AccessCoverageCard'
import { ReachManageCards } from '../components/ReachManageCards'

// timeAgo for an ISO string, with "Never" for null.

type Win = '1h' | '24h' | '7d' | '30d'
const WINDOWS: { k: Win; label: string }[] = [{ k: '1h', label: '1h' }, { k: '24h', label: '24h' }, { k: '7d', label: '7d' }, { k: '30d', label: '30d' }]
const SLA_TARGET = 99.9
const fmtPct = (v?: number | null) => (v == null ? '—' : `${v.toFixed(v >= 99.95 ? 3 : 2)}%`)
const fmtMs = (v?: number | null) => (v == null ? '—' : `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`)
function bucketLabel(iso: string, bucket: string): string {
  const d = new Date(iso)
  if (bucket === 'hour' || bucket === 'minute') return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

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
    virtual_devices?: number
    total_devices?: number
    discovered_devices?: number
  }
}

const STATUS_DONUT_COLOR: Record<string, string> = { up: '#16a34a', warning: '#d97706', down: '#dc2626', unknown: '#94a3b8' }

const OVERALL_LABEL: Record<string, string> = { excellent: 'Excellent', good: 'Good', needs_attention: 'Needs Attention', critical: 'Critical', unknown: 'Not enough data' }
const OVERALL_BADGE: Record<string, string> = { excellent: 'badge-up', good: 'badge-up', needs_attention: 'badge-warning', critical: 'badge-down', unknown: 'badge-unknown' }

// SectionTitle — a labelled divider so the dashboard reads as distinct sections
// (Posture → Availability → Access → Operations → Inventory) rather than a wall.
function SectionTitle({ icon: Icon, title, hint }: { icon: ComponentType<{ size?: number | string }>; title: string; hint?: string }) {
  return (
    <div className="dash-section">
      <Icon size={15} /><h2>{title}</h2>{hint && <span className="dash-section-hint">{hint}</span>}
    </div>
  )
}

const SEC_DOT: Record<string, string> = { healthy: 'var(--ok)', warning: 'var(--warn)', critical: 'var(--crit)', unknown: 'var(--text-faint)' }

// Overall Infrastructure Health — self-explanatory: score ring + plain-English
// "why", clickable section chips that drill into real filters, the top real
// blockers (worst-first, deep-linked), and the alert-hygiene signal. Every value
// comes from /dashboard/infrastructure-health — no hardcoded reasons/blockers.
function InfraHealthCard({ data }: { data?: InfrastructureHealth }) {
  const navigate = useNavigate()
  if (!data) return <Panel title="Overall Infrastructure Health" icon={Activity}><div className="loading">Loading…</div></Panel>
  const o = data.overall
  const confCls = o.confidence === 'high' ? 'badge-up' : o.confidence === 'limited' ? 'badge-warning' : 'badge-unknown'
  const statusLabel = OVERALL_LABEL[o.status] ?? o.status
  const problems = data.sections.filter((s) => s.included && s.status !== 'healthy' && s.status !== 'unknown')
  const drivers = data.top_drivers ?? []
  const hyg = data.alert_hygiene
  const calcTip = 'Overall = average of the 5 section scores (Healthy 100 · Warning 65 · Critical 25). Sections with no data yet are excluded and lower confidence instead of the score.'
  const sevColor = (s: string) => (s === 'critical' ? 'var(--crit)' : s === 'warning' ? 'var(--warn)' : 'var(--text)')

  return (
    <Panel
      title="Overall Infrastructure Health"
      icon={Activity}
      actions={<span className={`badge ${OVERALL_BADGE[o.status] ?? 'badge-unknown'}`}>{statusLabel}</span>}
    >
      {/* Score + headline + one-line why */}
      <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
        {o.confidence === 'unknown'
          ? <div style={{ fontSize: 40, fontWeight: 800, color: 'var(--text-faint)', width: 120, textAlign: 'center' }}>—</div>
          : <HealthRing score={o.score} size={110} label="Score" />}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 16, fontWeight: 700 }}>{statusLabel}</div>
          <div className="muted" style={{ fontSize: 13, marginTop: 2 }}>{o.summary}</div>
          <div className="muted" style={{ fontSize: 11, marginTop: 6, display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
            <span title={calcTip} style={{ cursor: 'help', textDecoration: 'underline dotted', textUnderlineOffset: 2 }}>How is this calculated?</span>
            <span>·</span>
            <span title={o.confidence_reason} style={{ cursor: 'help' }}>Confidence: <span className={`badge ${confCls}`} style={{ textTransform: 'capitalize' }}>{o.confidence}</span></span>
            {o.calculated_at && <><span>·</span><span title={new Date(o.calculated_at).toLocaleString()}>calculated {timeAgo(o.calculated_at)}</span></>}
          </div>
        </div>
      </div>

      {/* Section chips — click to drill into the real page/filter */}
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 12 }}>
        {data.sections.map((s) => (
          <button key={s.name} className="seg-chip" onClick={() => s.link && navigate(s.link)}
            title={s.reason ? `${s.reason}${s.link ? `  (click to open ${s.link})` : ''}` : s.name}
            style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: s.link ? 'pointer' : 'default', borderColor: s.status !== 'healthy' && s.included ? SEC_DOT[s.status] : undefined }}>
            <span style={{ width: 8, height: 8, borderRadius: '50%', background: SEC_DOT[s.status] ?? 'var(--text-faint)', display: 'inline-block' }} />
            {s.name}
            <span className="muted" style={{ fontSize: 11 }}>{s.included ? s.score : 'n/a'}</span>
          </button>
        ))}
      </div>

      {/* Why needs attention — real reasons for each degraded section */}
      {problems.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="muted" style={{ fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.4 }}>Why needs attention</div>
          {problems.map((s) => (
            <div key={s.name} onClick={() => s.link && navigate(s.link)} title={s.link ? `Open ${s.link}` : undefined}
              style={{ display: 'flex', gap: 8, alignItems: 'baseline', fontSize: 12, padding: '3px 0', cursor: s.link ? 'pointer' : 'default' }}>
              <span style={{ color: SEC_DOT[s.status], fontWeight: 700, minWidth: 92 }}>{s.name}</span>
              <span>{s.reason}</span>
            </div>
          ))}
        </div>
      )}

      {/* Top blockers — the real open critical alerts, worst-first, deep-linked */}
      {drivers.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="muted" style={{ fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.4 }}>Top blockers ({drivers.length})</div>
          <div style={{ marginTop: 4, maxHeight: 168, overflowY: 'auto' }}>
            {drivers.map((d, i) => (
              <Link key={i} to={d.link} title={d.label}
                style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'center', padding: '5px 2px', fontSize: 12, borderBottom: '1px solid var(--border)', color: 'inherit', textDecoration: 'none' }}>
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  <span style={{ width: 7, height: 7, borderRadius: '50%', background: sevColor(d.severity), display: 'inline-block', marginRight: 6 }} />
                  <span className="mono">{d.ip || d.name}</span>
                  {d.protocol && <span className="muted" style={{ marginLeft: 6 }}>{d.protocol}:{d.port}</span>}
                  <span className="muted" style={{ marginLeft: 6 }}>{d.section}</span>
                </span>
                <span style={{ whiteSpace: 'nowrap', color: sevColor(d.severity) }}>
                  {d.severity}{d.last_changed ? <span className="muted" style={{ marginLeft: 6 }}>{timeAgo(d.last_changed)}</span> : null}
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Alert hygiene — surfaced, never auto-closed here */}
      {hyg && hyg.null_check_id_open > 0 && (
        <div onClick={() => navigate(hyg.link)} title={hyg.note}
          style={{ marginTop: 10, padding: '6px 8px', borderRadius: 6, background: 'var(--surface-2, rgba(255,180,0,.08))', border: '1px solid var(--warn)', fontSize: 12, cursor: 'pointer', display: 'flex', gap: 8, alignItems: 'center' }}>
          <span style={{ color: 'var(--warn)' }}>⚠ Alert hygiene</span>
          <span className="muted">{hyg.null_check_id_open} open alert{hyg.null_check_id_open === 1 ? '' : 's'} with no check linkage →</span>
        </div>
      )}
    </Panel>
  )
}

// ActionRequiredCard — the single "what do I do next?" card. One prioritized list
// (worst-first) of REAL open issues from /dashboard/action-required: each row is a
// count + plain-English explanation + a one-click drill-down to the page that fixes
// it. Empty list => an explicit "All clear", never a fabricated problem.
function ActionRequiredCard() {
  const navigate = useNavigate()
  const q = useQuery({ queryKey: ['action-required'], queryFn: () => api.get<ActionRequired>('/dashboard/action-required'), refetchInterval: 30_000, retry: 0 })
  const d = q.data
  const items = d?.items ?? []
  const tone = (s: string) => (s === 'critical' ? 'var(--crit)' : s === 'warning' ? 'var(--warn)' : 'var(--text-muted)')
  return (
    <Panel title="Needs attention now" icon={TriangleAlert} subtitle="the real issues to act on, worst-first" className="fill"
      actions={d ? <span className="muted" style={{ fontSize: 11 }}>updated {timeAgo(d.updated_at)}</span> : undefined}>
      {q.isError ? (
        <div className="muted" style={{ fontSize: 12, padding: '8px 2px' }}>Attention summary is unavailable right now. <button className="btn btn-ghost btn-xs" onClick={() => q.refetch()}>Retry</button></div>
      ) : !d ? <div className="loading">Loading…</div> : items.length === 0 ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 2px' }}>
          <span style={{ width: 22, height: 22, borderRadius: '50%', background: 'var(--ok)', color: '#fff', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontWeight: 700 }}>✓</span>
          <div><div style={{ fontWeight: 600 }}>All clear</div><div className="muted" style={{ fontSize: 12 }}>No open critical alerts or management gaps right now.</div></div>
        </div>
      ) : (
        <div style={{ display: 'grid', gap: 6 }}>
          {items.map((it) => (
            <div key={it.key} onClick={() => navigate(it.route)} title={`${it.explanation}  →  ${it.route}`}
              style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 10px', borderRadius: 8, border: '1px solid var(--border)', borderLeft: `3px solid ${tone(it.status)}`, cursor: 'pointer' }}>
              <span style={{ fontSize: 22, fontWeight: 800, color: tone(it.status), minWidth: 34, textAlign: 'center' }}>{it.count}</span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600, fontSize: 13 }}>{it.label}</div>
                <div className="muted" style={{ fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical' }}>{it.explanation}</div>
              </div>
              <span style={{ color: tone(it.status), fontSize: 18 }}>›</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

export function Dashboard() {
  const navigate = useNavigate()
  const [win, setWin] = useState<Win>('24h')
  const [showRisk, setShowRisk] = useState(false)
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get<DashboardData>('/dashboard'), refetchInterval: 30_000 })
  const mon = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'), refetchInterval: 30_000 })
  const checks = useQuery({ queryKey: ['mon-checks'], queryFn: () => api.get<MonitoringCheck[]>('/monitoring/checks'), refetchInterval: 30_000 })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'), refetchInterval: 30_000 })
  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 30_000 })
  const infra = useQuery({ queryKey: ['infra-health'], queryFn: () => api.get<InfrastructureHealth>('/dashboard/infrastructure-health'), refetchInterval: 30_000, retry: 0 })
  const avail = useQuery({ queryKey: ['analytics-availability', win], queryFn: () => api.get<AvailabilityAnalytics>(`/analytics/availability?window=${win}`), refetchInterval: 60_000, retry: 0 })
  const sites = useQuery({ queryKey: ['sites-overview'], queryFn: () => api.get<SiteRollup[]>('/sites/overview'), refetchInterval: 60_000, retry: 0 })
  const uptime = useQuery({ queryKey: ['analytics-device-uptime', win], queryFn: () => api.get<DeviceUptime[]>(`/analytics/device-uptime?window=${win}`), refetchInterval: 60_000, retry: 0 })

  const h = dash.data?.headline ?? {}
  const devs = devices.data ?? []
  const total = devs.length

  // ---- Manageability: can HIMS actually collect from each device? ----
  // `management` is set on every device that has a management dimension; "managed"
  // = a credential that actually works. Devices with no management state (n/a) are
  // excluded from the ratio so it reflects the manageable fleet, not phones/VMs.
  // `inventory_only` (access opted out, monitored only) and `virtual` (manual
  // placeholder) are NOT manageable gaps — exclude them from the ratio and the
  // unmanaged count so marking a device inventory-only clears it from both.
  const NON_MANAGEABLE = new Set(['inventory_only', 'virtual'])
  const managed = devs.filter((d) => d.management === 'managed').length
  const manageable = devs.filter((d) => !!d.management && !NON_MANAGEABLE.has(d.management)).length
  const unmanagedCount = manageable - managed
  const naCount = total - manageable
  const mgmtPct = manageable > 0 ? Math.round((managed / manageable) * 100) : (total > 0 ? 100 : 0)
  const mgmtTone: 'ok' | 'warn' | 'crit' = unmanagedCount === 0 ? 'ok' : (mgmtPct >= 80 ? 'warn' : 'crit')

  const monMap = new Map((mon.data ?? []).map((r) => [r.status, r.count]))
  const up = monMap.get('up') ?? 0, warning = monMap.get('warning') ?? 0, down = monMap.get('down') ?? 0, unknown = monMap.get('unknown') ?? 0
  const monitored = up + warning + down
  // Extra (supplemental) checks summary for the Online card footer.
  const allChecks = checks.data ?? []
  const extraChecks = allChecks.filter((c) => c.role === 'supplemental')
  const extraDown = extraChecks.filter((c) => (c.last_status || '').toLowerCase() === 'down').length
  const statusDonut = [
    { label: 'Online', value: up, color: STATUS_DONUT_COLOR.up },
    { label: 'Warning', value: warning, color: STATUS_DONUT_COLOR.warning },
    { label: 'Offline', value: down, color: STATUS_DONUT_COLOR.down },
    { label: 'Unknown', value: unknown, color: STATUS_DONUT_COLOR.unknown },
  ].filter((d) => d.value > 0)

  const byType = Object.entries(devs.reduce<Record<string, number>>((m, d) => { m[d.category] = (m[d.category] ?? 0) + 1; return m }, {})).sort((a, b) => b[1] - a[1])
  const typeDonut = byType.slice(0, 7).map(([label, value]) => ({ label: label.replace(/_/g, ' '), value, color: colorFor(label) }))
  // Per-type breakdown (ALL types) with offline (reachability=offline) and unmanaged
  // (a real management gap — inventory-only/virtual are NOT gaps) counts, one pass.
  const typeStats = devs.reduce<Record<string, { count: number; offline: number; unmanaged: number }>>((acc, d) => {
    const t = d.category || 'unknown'
    const s = acc[t] ?? (acc[t] = { count: 0, offline: 0, unmanaged: 0 })
    s.count++
    if ((d.reachability ?? '') === 'offline') s.offline++
    if (d.management && d.management !== 'managed' && !NON_MANAGEABLE.has(d.management)) s.unmanaged++
    return acc
  }, {})
  const typeBreakdown = Object.entries(typeStats).map(([type, s]) => ({ type, ...s })).sort((a, b) => b.count - a.count)
  const typeOfflineTotal = typeBreakdown.reduce((n, t) => n + t.offline, 0)
  const typeUnmanagedTotal = typeBreakdown.reduce((n, t) => n + t.unmanaged, 0)
  const topVendors = Object.entries(devs.reduce<Record<string, number>>((m, d) => { const v = d.vendor || 'Unknown'; m[v] = (m[v] ?? 0) + 1; return m }, {})).sort((a, b) => b[1] - a[1]).slice(0, 6).map(([label, value]) => ({ label, value, color: colorFor(label) }))

  const critical = devs.filter((d) => ['down', 'needs_attention', 'offline'].includes((d.status || '').toLowerCase())).slice(0, 6)

  const recentJobs = [...(jobs.data ?? [])].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  const discoveryTrend = [...recentJobs].reverse().slice(-12).map((j) => j.found_count)
  const lastJob = recentJobs[0]

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

  // ---- Availability analytics (windowed) ----
  // Treat zero collected polls as "no history" — never imply uptime on an empty system.
  const aSum = (avail.data?.summary?.samples ?? 0) > 0 ? avail.data!.summary : undefined
  const aBucket = avail.data?.bucket ?? 'hour'
  const aSeries = avail.data?.series ?? []
  const aLabels = aSeries.map((p) => bucketLabel(p.ts, aBucket))
  const aPts = aSeries.map((p) => p.uptime_pct)
  const aMin = Math.min(SLA_TARGET, ...(aPts.length ? aPts : [100]))
  const availTone = aSum == null ? 'default' : aSum.uptime_pct >= SLA_TARGET ? 'ok' : aSum.uptime_pct >= 99 ? 'warn' : 'crit'

  // ---- Site health matrix ----
  const siteRows = [...(sites.data ?? [])]
    .map((s) => ({ ...s, avail: s.devices > 0 ? (s.up / s.devices) * 100 : 0 }))
    .sort((a, b) => (b.down - a.down === 0 ? a.avail - b.avail : b.down - a.down))

  // ---- Worst performers ----
  // At-risk devices dragging fleet availability this window: below 100% uptime OR
  // flapping. Sorted worst-first (lowest uptime, then most flaps). `worst` (top 6)
  // still feeds the Lowest Uptime panel; `atRisk` is the full click-through list.
  const atRisk = (uptime.data ?? []).filter((d) => d.uptime_pct < 100 || d.flaps > 0)
    .sort((a, b) => a.uptime_pct - b.uptime_pct || b.flaps - a.flaps)

  return (
    <div>
      <PageHeader
        title="Executive Dashboard"
        subtitle="Your whole network at a glance — is it healthy, reachable, and under management? Hover any “?” for a plain-language explanation."
        icon={LayoutDashboard}
        actions={
          <>
            <div className="seg" role="tablist" aria-label="Analysis window">
              {WINDOWS.map((wn) => (
                <button key={wn.k} className={win === wn.k ? 'active' : ''} onClick={() => setWin(wn.k)}>{wn.label}</button>
              ))}
            </div>
            <span className="muted" style={{ fontSize: 12 }}><Clock size={12} style={{ verticalAlign: -1 }} /> {timeAgo(new Date().toISOString())}</span>
            <button className="btn btn-ghost btn-sm" onClick={() => { dash.refetch(); mon.refetch(); devices.refetch(); jobs.refetch(); alerts.refetch(); avail.refetch(); uptime.refetch() }}>
              <RefreshCw size={14} /> Refresh
            </button>
          </>
        }
      />

      {/* ===== Posture hero: infra health · availability · manageability ===== */}
      <div className="grid-hero">
        <InfraHealthCard data={infra.data} />
          <Panel
            title={`Availability · ${win}`} icon={ShieldCheck} subtitle="did devices stay reachable?"
            actions={aSum ? <span className={`badge ${availTone === 'ok' ? 'badge-up' : availTone === 'warn' ? 'badge-warning' : availTone === 'crit' ? 'badge-down' : 'badge-unknown'}`} title={`Target SLA — the goal line we aim for (${SLA_TARGET}% uptime), not the measured value.`}>Target {SLA_TARGET}%</span> : undefined}
          >
            {aSum ? (
              <>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
                  <div style={{ fontSize: 40, fontWeight: 800, color: availTone === 'ok' ? 'var(--ok)' : availTone === 'warn' ? 'var(--warn)' : availTone === 'crit' ? 'var(--crit)' : 'var(--text)' }}>{fmtPct(aSum.uptime_pct)}</div>
                  <div className="muted" style={{ fontSize: 12 }}>
                    uptime<InfoHint text="Share of monitoring checks that got a response in this window. 100% means every check succeeded." label="Uptime" /><br />
                    {aSum.up.toLocaleString()}/{aSum.samples.toLocaleString()} checks OK · {aSum.devices} devices
                  </div>
                </div>
                {aPts.length > 1 && <div style={{ marginTop: 8 }}><AreaChart points={aPts} labels={aLabels} height={70} min={Math.max(0, aMin - 0.4)} max={100} baseline={SLA_TARGET} color="var(--ok)" valueFmt={(v) => fmtPct(v)} ariaLabel="Availability trend" /></div>}
                <div className="stat-strip" style={{ marginTop: 12 }}>
                  <div className="s-item"><b>{fmtMs(aSum.avg_latency_ms)}</b><small>avg latency<InfoHint text="Average round-trip response time across all checks — how quickly devices reply." label="Average latency" /></small></div>
                  <div className="s-item"><b>{fmtMs(aSum.p95_latency_ms)}</b><small>p95 latency<InfoHint text="95% of responses were faster than this. A worst-case-ish figure that ignores rare spikes." label="p95 latency" /></small></div>
                  <div className="s-item" style={{ cursor: atRisk.length > 0 ? 'pointer' : undefined }}
                    onClick={atRisk.length > 0 ? () => setShowRisk((v) => !v) : undefined}
                    title={atRisk.length > 0 ? 'Click to list the devices dragging availability down' : undefined}>
                    <b style={{ color: atRisk.length > 0 ? 'var(--warn)' : undefined }}>{atRisk.length}{atRisk.length > 0 ? (showRisk ? ' ▾' : ' ›') : ''}</b><small>at risk<InfoHint text="Devices below 100% uptime or that flapped (went down and up) in this window. Click to see them." label="At risk" /></small>
                  </div>
                </div>
                {showRisk && atRisk.length > 0 && (
                  <div style={{ marginTop: 10, borderTop: '1px solid var(--border)', paddingTop: 8, maxHeight: 220, overflowY: 'auto' }}>
                    {atRisk.map((d) => (
                      <Link key={d.device_id} to={`/devices/${d.device_id}`} title="Open device"
                        style={{ display: 'flex', justifyContent: 'space-between', gap: 8, padding: '4px 2px', fontSize: 12, borderBottom: '1px solid var(--border)', color: 'inherit', textDecoration: 'none' }}>
                        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {d.name || d.primary_ip || '—'}
                          <span className="muted" style={{ marginLeft: 6 }}>{d.category?.replace(/_/g, ' ')}</span>
                        </span>
                        <span style={{ whiteSpace: 'nowrap' }}>
                          <b style={{ color: d.uptime_pct >= 99 ? 'var(--warn)' : 'var(--crit)' }}>{fmtPct(d.uptime_pct)}</b>
                          {d.flaps > 0 && <span className="muted" style={{ marginLeft: 6 }}>{d.flaps} flap{d.flaps === 1 ? '' : 's'}</span>}
                        </span>
                      </Link>
                    ))}
                  </div>
                )}
              </>
            ) : <EmptyState icon={HeartPulse} title="No availability history" message="Seed monitoring checks and run a sweep to build SLA history." action={<Link className="btn btn-primary btn-sm" to="/monitoring">Go to Monitoring</Link>} />}
          </Panel>
          <div className="stack">
          <Panel
            title="Manageability" icon={KeyRound} subtitle="can HIMS log in and collect?"
            actions={<span className={`badge ${mgmtTone === 'ok' ? 'badge-up' : mgmtTone === 'warn' ? 'badge-warning' : 'badge-down'}`} title="Managed devices as a share of the manageable fleet.">{mgmtPct}% managed</span>}
          >
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
              <div style={{ fontSize: 40, fontWeight: 800, color: 'var(--ok)' }}>{managed.toLocaleString()}</div>
              <div className="muted" style={{ fontSize: 12 }}>
                of {manageable.toLocaleString()} manageable devices<InfoHint text="Manageable = devices HIMS should be able to log into (switches, servers, Windows/Linux hosts…). Phones, VMs and inventory-only devices are excluded." label="Manageable" /><br />
                {mgmtPct}% have a working credential
              </div>
            </div>
            <div style={{ marginTop: 10, height: 8, borderRadius: 4, background: 'var(--border)', overflow: 'hidden' }} title={`${mgmtPct}% of manageable devices are managed`}>
              <div style={{ width: `${manageable > 0 ? (managed / manageable) * 100 : 0}%`, height: '100%', background: mgmtTone === 'crit' ? 'var(--crit)' : mgmtTone === 'warn' ? 'var(--warn)' : 'var(--ok)' }} />
            </div>
            <div className="stat-strip" style={{ marginTop: 12 }}>
              <div className="s-item"><b style={{ color: 'var(--ok)' }}>{managed.toLocaleString()}</b><small>managed<InfoHint text="HIMS has a credential that actually authenticates and collects — proven access, not just an open port." label="Managed" /></small></div>
              <div className="s-item" style={{ cursor: unmanagedCount > 0 ? 'pointer' : undefined }} onClick={unmanagedCount > 0 ? () => navigate('/inventory/unmanaged') : undefined} title={unmanagedCount > 0 ? 'Open the Unmanaged Devices list' : undefined}>
                <b style={{ color: unmanagedCount > 0 ? 'var(--warn)' : undefined }}>{unmanagedCount.toLocaleString()}{unmanagedCount > 0 ? ' ›' : ''}</b><small>unmanaged<InfoHint text="Manageable devices with no proven working access yet — need a credential or an agent. Click to fix." label="Unmanaged" /></small>
              </div>
              <div className="s-item" style={{ cursor: naCount > 0 ? 'pointer' : undefined }} onClick={naCount > 0 ? () => navigate('/inventory?management=inventory_only') : undefined} title={naCount > 0 ? 'View inventory-only devices' : undefined}>
                <b>{naCount.toLocaleString()}{naCount > 0 ? ' ›' : ''}</b><small>INV<InfoHint text="Inventory only — recorded and monitored for reachability, but access is deliberately opted out (no credential expected). Includes virtual placeholders. Not counted as unmanaged. Click to view them." label="Inventory only" /></small></div>
            </div>
          </Panel>
          <Panel title="Critical Assets" icon={TriangleAlert} subtitle="offline or flagged, needing attention now" className="fill" actions={critical.length > 0 ? <Link className="btn btn-ghost btn-sm" to="/inventory?reachability=offline">View all</Link> : undefined}>
            {critical.length === 0
              ? <EmptyState icon={Wifi} title="All systems operational" message="No devices are offline or flagged for attention." />
              : (
                <ul className="activity">
                  {critical.map((d) => {
                    const base = detailBase[d.category] ?? '/devices'
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
          </div>
      </div>

      {/* KPI row */}
      <div className="kpi-grid kpi-6">
        <Kpi label="Total Devices" value={total} icon={Boxes} tone="info"
          hint="Every device in inventory — discovered on the network plus any manually-added (virtual) placeholders."
          sub={h.virtual_devices ? `${h.discovered_devices ?? (total - h.virtual_devices)} discovered · ${h.virtual_devices} virtual` : `${byType.length} categories`}
          footerRight={manageable > 0 ? <span style={{ color: 'var(--ok)' }}>{managed.toLocaleString()} managed</span> : undefined} />
        <Kpi
          label="Online"
          value={up}
          icon={Wifi}
          tone="ok"
          hint="Devices responding to their monitoring check right now. 'Online' is about reachability, not whether HIMS can manage them."
          sub={monitored > 0 ? `${Math.round((up / monitored) * 100)}% of monitored` : 'no checks'}
          footerLeft={extraChecks.length > 0 ? `${extraChecks.length} extra check${extraChecks.length !== 1 ? 's' : ''}` : undefined}
          footerRight={extraChecks.length > 0
            ? (extraDown > 0
                ? <span style={{ color: 'var(--crit)', cursor: 'pointer' }} title="Extra checks currently offline — the devices show as Degraded, not offline" onClick={() => navigate('/inventory?reachability=warning')}>{extraDown} offline ›</span>
                : <span style={{ color: 'var(--ok)' }}>all OK</span>)
            : undefined}
        />
        <Kpi label="Offline" value={down} icon={WifiOff} tone={down > 0 ? 'crit' : 'default'}
          hint="Devices whose monitoring check failed (no response). Click to see them and why."
          sub={down > 0 ? 'view offline →' : (warning > 0 ? `${warning} warning` : 'all clear')} onClick={down > 0 ? () => navigate('/inventory?reachability=offline') : undefined} />
        <Kpi label="Active Alerts" value={h.open_alerts ?? 0} icon={Bell} tone={(h.open_alerts ?? 0) > 0 ? 'crit' : 'default'}
          hint="Open, unresolved alerts (critical or warning) that need a look — outages, stale collection, low disk, etc."
          sub="unresolved" onClick={(h.open_alerts ?? 0) > 0 ? () => navigate('/alerts') : undefined} />
        <Kpi label="Open Work Orders" value={h.open_work_orders ?? 0} icon={ClipboardList} tone={(h.open_work_orders ?? 0) > 0 ? 'warn' : 'default'}
          hint="Maintenance/remediation tasks that are still in progress." sub="in progress" />
        <Kpi label="Expiring Systems" value={h.expiring_systems ?? 0} icon={ShieldAlert} tone={(h.expiring_systems ?? 0) > 0 ? 'warn' : 'default'}
          hint="Devices with a warranty, licence or certificate expiring within 90 days." sub="next 90 days" />
      </div>

      {/* ===== B · Needs attention now + reachability/management posture, side by side ===== */}
      <SectionTitle icon={TriangleAlert} title="Needs attention now" hint="what to act on now — and how reachable vs managed the fleet is (two separate signals)" />
      <div className="grid-2" style={{ alignItems: 'stretch' }}>
        <div className="stack"><ActionRequiredCard /></div>
        <div className="stack"><ReachManageCards /></div>
      </div>

      {/* ===== C · Collection & Trust — what HIMS can collect from ===== */}
      <SectionTitle icon={ShieldCheck} title="Collection & Trust" hint="what HIMS can log into and collect from — by protocol and by site" />
      <div className="grid-2" style={{ alignItems: 'stretch' }}>
        <div className="stack"><ManagementAccessCoverage /></div>
        <div className="stack">
          <Panel title="Live Fleet Health" icon={HeartPulse} subtitle="status of every monitored device right now">
            {statusDonut.length > 0 ? (
              <div className="row" style={{ alignItems: 'center', gap: 18, flexWrap: 'wrap' }}>
                <Donut data={statusDonut} centerValue={monitored} centerLabel="monitored" size={128} thickness={15} rounded />
                <div style={{ flex: 1, minWidth: 130 }}><Legend data={statusDonut} total={monitored} /></div>
              </div>
            ) : <EmptyState icon={Activity} title="No monitoring checks yet" message="Seed checks to compute a health score." action={<Link className="btn btn-primary btn-sm" to="/monitoring">Go to Monitoring</Link>} />}
          </Panel>
          {siteRows.length > 0 && (
            <Panel title="Site Health" icon={Building2} className="fill" subtitle="up / down / open alerts per site · worst first" actions={<Link className="btn btn-ghost btn-sm" to="/sites">Multi-Site →</Link>}>
              <table className="site-matrix">
                <thead><tr><th>Site</th><th>Devices</th><th>On</th><th>Off</th><th>Availability</th><th>Alerts</th></tr></thead>
                <tbody>
                  {siteRows.slice(0, 8).map((s) => {
                    const tone = s.down > 0 ? 'var(--crit)' : s.warning > 0 ? 'var(--warn)' : 'var(--ok)'
                    return (
                      <tr key={s.site_id}>
                        <td><Link className="cell-name" to="/sites">{s.site_name}</Link></td>
                        <td>{s.devices}</td>
                        <td style={{ color: 'var(--ok)' }}>{s.up}</td>
                        <td style={{ color: s.down > 0 ? 'var(--crit)' : undefined, fontWeight: s.down > 0 ? 600 : undefined }}>{s.down}</td>
                        <td><div style={{ display: 'flex', alignItems: 'center', gap: 8 }}><div className="avail-track"><div className="avail-fill" style={{ width: `${s.avail}%`, background: tone }} /></div><span className="mono" style={{ fontSize: 12 }}>{Math.round(s.avail)}%</span></div></td>
                        <td>{s.open_alerts > 0 ? <span className="badge badge-down">{s.open_alerts}</span> : <span className="muted">0</span>}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </Panel>
          )}
        </div>
      </div>

      {/* ===== D · Fleet Overview — what's on the network ===== */}
      <SectionTitle icon={Boxes} title="Fleet Overview" hint={`what's on the network — ${total} devices across ${byType.length} types`} />
      <div className="grid-2">
        <Panel title="Devices by Type" icon={Layers} subtitle={`all ${byType.length} types · offline & unmanaged per type`}>
          {typeBreakdown.length === 0 ? <div className="muted">No devices yet.</div> : (
            <>
              <div className="row" style={{ alignItems: 'center', gap: 18, marginBottom: 14 }}>
                <Donut data={typeDonut} centerValue={total} centerLabel="devices" size={110} />
                <div style={{ fontSize: 12 }}>
                  <div><b style={{ fontSize: 20 }}>{total.toLocaleString()}</b> <span className="muted">devices · {byType.length} types</span></div>
                  <div style={{ marginTop: 4, display: 'flex', gap: 14 }}>
                    <span><b style={{ color: typeOfflineTotal > 0 ? 'var(--crit)' : 'var(--text)' }}>{typeOfflineTotal}</b> <span className="muted">offline</span></span>
                    <span><b style={{ color: typeUnmanagedTotal > 0 ? 'var(--warn)' : 'var(--text)' }}>{typeUnmanagedTotal}</b> <span className="muted">unmanaged</span></span>
                  </div>
                </div>
              </div>
              {/* ALL types in two columns; per type: total, offline (red), unmanaged (orange). */}
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(232px, 1fr))', gap: '0 22px' }}>
                {[typeBreakdown.slice(0, Math.ceil(typeBreakdown.length / 2)), typeBreakdown.slice(Math.ceil(typeBreakdown.length / 2))].map((col, i) => (
                  <div key={i}>
                    <div className="dbt-head">TYPE<span>TOTAL</span><span title="offline devices">OFF</span><span title="unmanaged devices">UNM</span></div>
                    {col.map((t) => (
                      <Link key={t.type} to={`/inventory?category=${t.type}`} className="dbt-row" title={`${t.type.replace(/_/g, ' ')} — ${t.count} total · ${t.offline} offline · ${t.unmanaged} unmanaged`}>
                        <span className="dbt-name"><span className="dbt-dot" style={{ background: colorFor(t.type) }} />{t.type.replace(/_/g, ' ')}</span>
                        <span className="dbt-num">{t.count}</span>
                        <span className="dbt-num" style={{ color: t.offline > 0 ? 'var(--crit)' : 'var(--text-faint)', fontWeight: t.offline > 0 ? 700 : 400 }}>{t.offline || '—'}</span>
                        <span className="dbt-num" style={{ color: t.unmanaged > 0 ? 'var(--warn)' : 'var(--text-faint)', fontWeight: t.unmanaged > 0 ? 700 : 400 }}>{t.unmanaged || '—'}</span>
                      </Link>
                    ))}
                  </div>
                ))}
              </div>
            </>
          )}
        </Panel>
        <Panel title="Top Vendors" icon={Server} subtitle="most common hardware makers in inventory"><BarList rows={topVendors} /></Panel>
      </div>

      {/* ===== E · Recent Activity — what changed recently ===== */}
      <SectionTitle icon={TrendingUp} title="Recent Activity" hint="what changed recently — alerts opened/resolved and discovery scans" />
      <div className="grid-2" style={{ alignItems: 'stretch' }}>
        <div className="stack">
          <Panel title="Latest Events" icon={TrendingUp} className="fill" subtitle="most recent alerts and scans" actions={<Link className="btn btn-ghost btn-sm" to="/alerts">All alerts →</Link>}>
            <ActivityFeed items={feed} />
          </Panel>
        </div>
        <div className="stack">
          <Panel title="Discovery Activity" icon={Radar} className="fill" subtitle="recent scans that find and refresh devices" actions={<Link className="btn btn-ghost btn-sm" to="/discovery">Open Discovery</Link>}>
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
                        <td className="mono" style={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={j.scope ?? j.scope_cidr ?? ''}>{j.scope || j.scope_cidr || '—'}</td>
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
      </div>
    </div>
  )
}

