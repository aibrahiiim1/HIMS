import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, TriangleAlert, Building2, Radar, Network, Maximize, Minimize,
  RefreshCw, Clock, ServerCrash, HeartPulse, ShieldCheck, ArrowUpDown,
} from 'lucide-react'
import {
  api, type InfrastructureHealth, type OperationalHealth, type Alert,
  type Device, type Location, type AuditEntry,
  type AvailabilityAnalytics, type DeviceUptime,
} from '../api'

// NOC Wallboard — a large-format operations view for a TV / wall display.
// Self-contained and dark-optimized (fixed palette, independent of app theme),
// fullscreen-capable, auto-refreshing. Every figure comes from a live endpoint;
// there is no synthetic data.

const WALL_CSS = `
.noc { --noc-bg:#0a0f1c; --noc-panel:#121b2e; --noc-panel2:#0f1726; --noc-line:#1e2b44;
  --noc-text:#e8eefb; --noc-muted:#8aa0c6; --noc-ok:#22c55e; --noc-warn:#f59e0b;
  --noc-crit:#ef4444; --noc-info:#38bdf8; --noc-accent:#6366f1;
  position:relative; background:var(--noc-bg); color:var(--noc-text);
  min-height:100%; margin:-24px; padding:20px 24px; font-variant-numeric:tabular-nums; }
.noc.is-fs { margin:0; padding:24px 28px; height:100vh; overflow:auto; }
.noc-bar { display:flex; align-items:center; gap:16px; margin-bottom:18px; }
.noc-bar h1 { font-size:22px; font-weight:800; letter-spacing:.5px; margin:0; display:flex; align-items:center; gap:10px; }
.noc-bar .spacer { flex:1; }
.noc-chip { display:inline-flex; align-items:center; gap:7px; background:var(--noc-panel); border:1px solid var(--noc-line);
  color:var(--noc-muted); padding:7px 12px; border-radius:9px; font-size:13px; font-weight:600; }
.noc-chip b { color:var(--noc-text); }
.noc-btn { background:var(--noc-panel); border:1px solid var(--noc-line); color:var(--noc-text); cursor:pointer;
  padding:7px 12px; border-radius:9px; font-size:13px; font-weight:700; display:inline-flex; align-items:center; gap:7px; }
.noc-btn:hover { border-color:var(--noc-accent); }
.noc-sel { background:var(--noc-panel); border:1px solid var(--noc-line); color:var(--noc-text);
  padding:7px 10px; border-radius:9px; font-size:13px; font-weight:700; }
.noc-grid { display:grid; grid-template-columns:repeat(12,1fr); gap:16px; }
.noc-card { background:var(--noc-panel); border:1px solid var(--noc-line); border-radius:14px; padding:16px 18px; }
.noc-card h2 { font-size:12px; text-transform:uppercase; letter-spacing:.08em; color:var(--noc-muted);
  margin:0 0 12px; font-weight:700; display:flex; align-items:center; gap:8px; }
.noc-big { font-size:46px; font-weight:800; line-height:1; }
.noc-sub { color:var(--noc-muted); font-size:13px; margin-top:6px; }
.noc-statword { font-size:18px; font-weight:800; text-transform:uppercase; letter-spacing:.04em; }
.noc-ring-wrap { display:flex; align-items:center; gap:18px; }
.noc-metric-row { display:flex; justify-content:space-between; align-items:baseline; padding:7px 0; border-bottom:1px dashed var(--noc-line); }
.noc-metric-row:last-child { border-bottom:0; }
.noc-metric-row .v { font-size:20px; font-weight:800; }
.noc-site { display:flex; align-items:center; gap:12px; padding:9px 0; border-bottom:1px solid var(--noc-line); }
.noc-site:last-child { border-bottom:0; }
.noc-site .dot { width:12px; height:12px; border-radius:50%; flex:0 0 auto; }
.noc-site .nm { font-weight:700; flex:1; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.noc-site .ct { color:var(--noc-muted); font-size:13px; }
.noc-bartrack { height:6px; border-radius:4px; background:var(--noc-panel2); overflow:hidden; width:90px; flex:0 0 auto; }
.noc-bartrack > i { display:block; height:100%; }
.noc-list { margin:0; padding:0; list-style:none; }
.noc-list li { display:flex; align-items:center; gap:10px; padding:8px 0; border-bottom:1px solid var(--noc-line); font-size:14px; }
.noc-list li:last-child { border-bottom:0; }
.noc-list .mono { font-family:ui-monospace,monospace; color:var(--noc-muted); font-size:12px; }
.noc-pill { font-size:11px; font-weight:800; text-transform:uppercase; padding:2px 8px; border-radius:999px; }
.noc-ev { display:flex; gap:10px; padding:7px 0; border-bottom:1px solid var(--noc-line); font-size:13px; }
.noc-ev:last-child { border-bottom:0; }
.noc-ev .t { color:var(--noc-muted); font-size:12px; white-space:nowrap; }
.noc-empty { color:var(--noc-muted); font-size:14px; padding:14px 0; }
@media (max-width:1100px){ .noc-grid{ grid-template-columns:repeat(6,1fr);} }
`

type ToneKey = 'ok' | 'warn' | 'crit' | 'info' | 'muted'
const TONE: Record<ToneKey, string> = {
  ok: 'var(--noc-ok)', warn: 'var(--noc-warn)', crit: 'var(--noc-crit)', info: 'var(--noc-info)', muted: 'var(--noc-muted)',
}
function statusTone(s?: string): ToneKey {
  switch ((s || '').toLowerCase()) {
    case 'excellent': case 'good': case 'healthy': case 'up': case 'online': case 'active': return 'ok'
    case 'warning': case 'needs_attention': case 'stale': case 'limited': return 'warn'
    case 'critical': case 'down': case 'offline': case 'failed': return 'crit'
    default: return 'muted'
  }
}
const REFRESH_MS = 30_000

export function NocWallboard() {
  const [refreshMs, setRefreshMs] = useState(REFRESH_MS)
  const [isFs, setIsFs] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(t)
  }, [])
  useEffect(() => {
    const onFs = () => setIsFs(Boolean(document.fullscreenElement))
    document.addEventListener('fullscreenchange', onFs)
    return () => document.removeEventListener('fullscreenchange', onFs)
  }, [])

  const opts = { refetchInterval: refreshMs, refetchIntervalInBackground: true }
  const infra = useQuery({ queryKey: ['noc', 'infra'], queryFn: () => api.get<InfrastructureHealth>('/dashboard/infrastructure-health'), ...opts })
  const ops = useQuery({ queryKey: ['noc', 'ops'], queryFn: () => api.get<OperationalHealth>('/dashboard/operational-health'), ...opts })
  const alerts = useQuery({ queryKey: ['noc', 'alerts'], queryFn: () => api.get<Alert[]>('/alerts'), ...opts })
  const devices = useQuery({ queryKey: ['noc', 'devices'], queryFn: () => api.get<Device[]>('/devices?category=all'), ...opts })
  const locations = useQuery({ queryKey: ['noc', 'locs'], queryFn: () => api.get<Location[]>('/locations/all'), ...opts })
  const events = useQuery({ queryKey: ['noc', 'events'], queryFn: () => api.get<AuditEntry[]>('/audit-log'), ...opts })
  const avail = useQuery({ queryKey: ['noc', 'avail'], queryFn: () => api.get<AvailabilityAnalytics>('/analytics/availability?window=24h'), ...opts })
  const uptime = useQuery({ queryKey: ['noc', 'uptime'], queryFn: () => api.get<DeviceUptime[]>('/analytics/device-uptime?window=24h'), ...opts })

  const lastUpdated = Math.max(infra.dataUpdatedAt, ops.dataUpdatedAt, devices.dataUpdatedAt, alerts.dataUpdatedAt)

  const toggleFs = () => {
    if (document.fullscreenElement) document.exitFullscreen()
    else rootRef.current?.requestFullscreen?.()
  }

  // ---- Site health: roll devices up to their hotel (or topmost ancestor) ----
  const sites = useMemo(() => {
    const locs = locations.data ?? []
    const devs = devices.data ?? []
    if (!locs.length || !devs.length) return [] as SiteHealth[]
    const byId = new Map(locs.map((l) => [l.id, l]))
    const rootOf = (id?: string | null): Location | null => {
      let cur = id ? byId.get(id) : undefined
      let last: Location | null = cur ?? null
      const seen = new Set<string>()
      while (cur && !seen.has(cur.id)) {
        seen.add(cur.id)
        last = cur
        if (cur.kind === 'hotel') return cur
        cur = cur.parent_id ? byId.get(cur.parent_id) : undefined
      }
      return last
    }
    const agg = new Map<string, SiteHealth>()
    for (const d of devs) {
      const root = rootOf(d.location_id)
      const key = root?.id ?? '_unassigned'
      const name = root?.name ?? 'Unassigned'
      let s = agg.get(key)
      if (!s) { s = { id: key, name, total: 0, up: 0, down: 0, warn: 0 }; agg.set(key, s) }
      s.total++
      const t = (d.status || 'unknown').toLowerCase()
      if (t === 'up') s.up++
      else if (t === 'down') s.down++
      else if (t === 'warning') s.warn++
    }
    return [...agg.values()].sort((a, b) => (b.down - a.down) || (b.warn - a.warn) || b.total - a.total)
  }, [locations.data, devices.data])

  const offline = useMemo(() => (devices.data ?? []).filter((d) => (d.status || '').toLowerCase() === 'down'), [devices.data])
  const criticalAlerts = useMemo(
    () => (alerts.data ?? []).filter((a) => a.severity === 'critical' && a.status !== 'resolved').slice(0, 8),
    [alerts.data],
  )

  const overall = infra.data?.overall
  const ah = infra.data?.alerts
  const mon = ops.data?.monitoring
  const disc = ops.data?.discovery
  const topo = ops.data?.topology

  // ---- 24h availability trend + worst performers (real history) ----
  const availSum = avail.data?.summary
  const availPts = (avail.data?.series ?? []).map((p) => p.uptime_pct)
  const availTone: ToneKey = availSum == null ? 'muted' : availSum.uptime_pct >= 99.9 ? 'ok' : availSum.uptime_pct >= 99 ? 'warn' : 'crit'
  const worst = useMemo(
    () => (uptime.data ?? []).filter((d) => d.uptime_pct < 100 || d.flaps > 0).slice(0, 8),
    [uptime.data],
  )

  return (
    <div className={'noc' + (isFs ? ' is-fs' : '')} ref={rootRef}>
      <style>{WALL_CSS}</style>

      <div className="noc-bar">
        <h1><Activity size={22} /> HIMS NOC</h1>
        <span className="noc-chip"><Clock size={14} /> <b>{now.toLocaleTimeString()}</b> · {now.toLocaleDateString()}</span>
        <div className="spacer" />
        <span className="noc-chip"><RefreshCw size={14} /> updated {lastUpdated ? secsAgo(lastUpdated, now) : '—'}</span>
        <select className="noc-sel" value={refreshMs} onChange={(e) => setRefreshMs(Number(e.target.value))} title="Auto-refresh interval">
          <option value={30_000}>Refresh 30s</option>
          <option value={60_000}>Refresh 60s</option>
        </select>
        <button className="noc-btn" onClick={toggleFs}>{isFs ? <Minimize size={15} /> : <Maximize size={15} />}{isFs ? 'Exit' : 'Full screen'}</button>
      </div>

      <div className="noc-grid">
        {/* Overall infrastructure health */}
        <div className="noc-card" style={{ gridColumn: 'span 4' }}>
          <h2><HeartPulse size={14} /> Overall Infrastructure Health</h2>
          <div className="noc-ring-wrap">
            <Gauge score={overall?.score ?? 0} />
            <div>
              <div className="noc-statword" style={{ color: TONE[statusTone(overall?.status)] }}>
                {(overall?.status ?? 'unknown').replace('_', ' ')}
              </div>
              <div className="noc-sub">Confidence: {overall?.confidence ?? '—'}</div>
              {overall?.limited_reasons?.slice(0, 2).map((rr, i) => <div key={i} className="noc-sub">• {rr}</div>)}
            </div>
          </div>
        </div>

        {/* Critical alerts */}
        <div className="noc-card" style={{ gridColumn: 'span 4' }}>
          <h2><TriangleAlert size={14} /> Critical Alerts</h2>
          <div className="noc-big" style={{ color: (ah?.open_critical ?? 0) > 0 ? TONE.crit : TONE.ok }}>{ah?.open_critical ?? 0}</div>
          <div className="noc-sub">
            {ah?.open_warning ?? 0} warning · {ah?.unresolved ?? 0} unresolved · {ah?.acknowledged ?? 0} acked
          </div>
          {criticalAlerts.length > 0 ? (
            <ul className="noc-list" style={{ marginTop: 10 }}>
              {criticalAlerts.slice(0, 4).map((a) => (
                <li key={a.id}>
                  <span className="noc-pill" style={{ background: 'rgba(239,68,68,.15)', color: TONE.crit }}>CRIT</span>
                  <span style={{ flex: 1, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.message}</span>
                  <span className="t" style={{ color: 'var(--noc-muted)', fontSize: 12 }}>{secsAgo(Date.parse(a.opened_at), now)}</span>
                </li>
              ))}
            </ul>
          ) : (
            <div className="noc-sub">Last alert: {ah?.last_alert_at ? secsAgo(Date.parse(ah.last_alert_at), now) : 'none'}</div>
          )}
        </div>

        {/* Monitoring status */}
        <div className="noc-card" style={{ gridColumn: 'span 4' }}>
          <h2><Activity size={14} /> Monitoring</h2>
          <div className="noc-metric-row"><span>Online</span><span className="v" style={{ color: TONE.ok }}>{mon?.online_devices ?? 0}</span></div>
          <div className="noc-metric-row"><span>Offline</span><span className="v" style={{ color: (mon?.offline_devices ?? 0) > 0 ? TONE.crit : TONE.muted }}>{mon?.offline_devices ?? 0}</span></div>
          <div className="noc-metric-row"><span>Monitored</span><span className="v">{mon?.monitored_devices ?? 0}</span></div>
          <div className="noc-metric-row"><span>Collection</span><span style={{ color: TONE[statusTone(mon?.collection_status)] }}>{mon?.collection_status ?? '—'}</span></div>
        </div>

        {/* Sites health */}
        <div className="noc-card" style={{ gridColumn: 'span 5' }}>
          <h2><Building2 size={14} /> Sites Health</h2>
          {sites.length === 0 && <div className="noc-empty">No site data yet.</div>}
          {sites.slice(0, 8).map((s) => {
            const healthy = s.total ? Math.round((s.up / s.total) * 100) : 0
            const tone: ToneKey = s.down > 0 ? 'crit' : s.warn > 0 ? 'warn' : 'ok'
            return (
              <div key={s.id} className="noc-site">
                <span className="dot" style={{ background: TONE[tone] }} />
                <span className="nm">{s.name}</span>
                <span className="ct">{s.up}/{s.total} up{s.down ? ` · ${s.down} down` : ''}</span>
                <span className="noc-bartrack"><i style={{ width: `${healthy}%`, background: TONE[tone] }} /></span>
              </div>
            )
          })}
        </div>

        {/* Offline devices */}
        <div className="noc-card" style={{ gridColumn: 'span 4' }}>
          <h2><ServerCrash size={14} /> Offline Devices <span style={{ color: TONE.crit }}>({offline.length})</span></h2>
          {offline.length === 0 && <div className="noc-empty">All monitored devices are online.</div>}
          <ul className="noc-list">
            {offline.slice(0, 8).map((d) => (
              <li key={d.id}>
                <span className="noc-pill" style={{ background: 'rgba(239,68,68,.15)', color: TONE.crit }}>DOWN</span>
                <span style={{ flex: 1, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{d.name}</span>
                <span className="mono">{d.primary_ip ?? '—'}</span>
              </li>
            ))}
          </ul>
          {offline.length > 8 && <div className="noc-sub">+{offline.length - 8} more</div>}
        </div>

        {/* Discovery + Topology */}
        <div className="noc-card" style={{ gridColumn: 'span 3' }}>
          <h2><Radar size={14} /> Discovery</h2>
          <div className="noc-statword" style={{ color: TONE[statusTone(disc?.status)] }}>{disc?.status ?? '—'}</div>
          <div className="noc-sub">Last scan: {disc?.last_scan_status ?? '—'}</div>
          <div className="noc-sub">Pending jobs: {disc?.pending_job_count ?? 0}</div>
          <div className="noc-sub">Failed: {disc?.failed_scan_count ?? 0}</div>
          <h2 style={{ marginTop: 16 }}><Network size={14} /> Topology Coverage</h2>
          <div className="noc-big" style={{ fontSize: 34, color: TONE[statusTone(topo?.status)] }}>{topo?.coverage_percent ?? 0}%</div>
          <div className="noc-sub">{topo?.mapped_devices ?? 0} mapped · {topo?.unmapped_devices ?? 0} unmapped (switches/routers)</div>
        </div>

        {/* Fleet availability (24h trend) */}
        <div className="noc-card" style={{ gridColumn: 'span 8' }}>
          <h2><ShieldCheck size={14} /> Fleet Availability · 24h</h2>
          <div style={{ display: 'flex', alignItems: 'center', gap: 26, flexWrap: 'wrap' }}>
            <div>
              <div className="noc-big" style={{ color: TONE[availTone] }}>{availSum ? `${availSum.uptime_pct.toFixed(2)}%` : '—'}</div>
              <div className="noc-sub">
                {availSum ? `${availSum.devices} devices · ${availSum.up.toLocaleString()}/${availSum.samples.toLocaleString()} polls up` : 'no history yet'}
              </div>
              <div className="noc-sub">
                avg {availSum?.avg_latency_ms != null ? Math.round(availSum.avg_latency_ms) : '—'} ms · p95 {availSum?.p95_latency_ms != null ? Math.round(availSum.p95_latency_ms) : '—'} ms · SLA 99.9%
              </div>
            </div>
            <div style={{ flex: 1, minWidth: 240 }}>
              {availPts.length > 1 ? <NocSpark points={availPts} color={TONE[availTone]} /> : <div className="noc-empty">Availability history builds as the monitor polls.</div>}
            </div>
          </div>
        </div>

        {/* Worst performers / flapping */}
        <div className="noc-card" style={{ gridColumn: 'span 4' }}>
          <h2><ArrowUpDown size={14} /> Lowest Uptime · 24h</h2>
          {worst.length === 0 && <div className="noc-empty">Every device held 100% with no flaps.</div>}
          <ul className="noc-list">
            {worst.map((d) => (
              <li key={d.device_id}>
                <span className="noc-pill" style={{ background: 'rgba(245,158,11,.15)', color: d.uptime_pct >= 99 ? TONE.warn : TONE.crit }}>{d.uptime_pct.toFixed(1)}%</span>
                <span style={{ flex: 1, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{d.name}</span>
                <span className="mono">{d.flaps > 0 ? `${d.flaps} flaps` : ''}</span>
              </li>
            ))}
          </ul>
        </div>

        {/* Latest events */}
        <div className="noc-card" style={{ gridColumn: 'span 12' }}>
          <h2><ShieldCheck size={14} /> Latest Events</h2>
          {(events.data ?? []).length === 0 && <div className="noc-empty">No recent activity.</div>}
          {(events.data ?? []).slice(0, 8).map((e) => (
            <div key={e.id} className="noc-ev">
              <span className="t">{secsAgo(Date.parse(e.at), now)}</span>
              <span style={{ color: TONE.info, fontWeight: 700 }}>{e.action}</span>
              <span style={{ flex: 1, color: 'var(--noc-text)' }}>{e.summary}</span>
              <span className="t">{e.actor}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

interface SiteHealth { id: string; name: string; total: number; up: number; down: number; warn: number }

function Gauge({ score }: { score: number }) {
  const s = Math.max(0, Math.min(100, Math.round(score)))
  const size = 116, stroke = 12, r = (size - stroke) / 2, c = 2 * Math.PI * r
  const color = s >= 90 ? 'var(--noc-ok)' : s >= 75 ? '#84cc16' : s >= 50 ? 'var(--noc-warn)' : 'var(--noc-crit)'
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="#1e2b44" strokeWidth={stroke} />
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth={stroke}
        strokeDasharray={c} strokeDashoffset={c * (1 - s / 100)} strokeLinecap="round"
        transform={`rotate(-90 ${size / 2} ${size / 2})`} />
      <text x="50%" y="52%" textAnchor="middle" fontSize="30" fontWeight="800" fill="#e8eefb">{s}</text>
      <text x="50%" y="68%" textAnchor="middle" fontSize="11" fill="#8aa0c6">/ 100</text>
    </svg>
  )
}

// NocSpark — a dark-themed sparkline for the wallboard availability trend.
// Auto-scales to its points; the y-range floors a little below the lowest point
// so small dips are visible rather than a flat line.
function NocSpark({ points, color }: { points: number[]; color: string }) {
  const W = 600, H = 90
  const lo = Math.min(...points), hi = Math.max(...points, lo + 0.1)
  const span = hi - lo || 1
  const x = (i: number) => (i / (points.length - 1)) * W
  const y = (v: number) => 6 + (H - 12) * (1 - (v - lo) / span)
  const line = points.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
  return (
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ width: '100%', height: 90, display: 'block' }}>
      <polygon points={`0,${H} ${line} ${W},${H}`} fill={color} opacity={0.12} />
      <polyline points={line} fill="none" stroke={color} strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
  )
}

function secsAgo(ts: number, now: Date): string {
  if (!ts) return '—'
  const s = Math.max(0, Math.round((now.getTime() - ts) / 1000))
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}
