import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  HeartPulse, Activity, Play, Wifi, WifiOff, TriangleAlert,
  Gauge, Zap, CircleDot, ArrowUpDown, ShieldCheck,
} from 'lucide-react'
import {
  api, type MonitoringCheck, type MonitoringOverviewRow,
  type AvailabilityAnalytics, type DeviceUptime,
} from '../api'
import {
  PageHeader, Panel, Kpi, HealthRing, Donut, Legend, BarList, Meter,
  StatusPill, EmptyState, ActivityFeed, AreaChart, timeAgo,
} from '../components/ui'

const STATUS_DONUT_COLOR: Record<string, string> = { up: '#16a34a', warning: '#d97706', down: '#dc2626', unknown: '#94a3b8' }
type Win = '1h' | '24h' | '7d' | '30d'
const WINDOWS: { k: Win; label: string }[] = [{ k: '1h', label: '1h' }, { k: '24h', label: '24h' }, { k: '7d', label: '7d' }, { k: '30d', label: '30d' }]

// SLA target line drawn on the availability trend. 99.9% ≈ "three nines".
const SLA_TARGET = 99.9

// bucketLabel renders an ISO bucket as a compact axis/tooltip label.
function bucketLabel(iso: string, bucket: string): string {
  const d = new Date(iso)
  if (bucket === 'hour' || bucket === 'minute') return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}
const fmtMs = (v?: number | null) => (v == null ? '—' : `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`)
const fmtPct = (v?: number | null) => (v == null ? '—' : `${v.toFixed(v >= 99.95 ? 3 : 2)}%`)

export function Monitoring() {
  const qc = useQueryClient()
  const [win, setWin] = useState<Win>('24h')

  const overview = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'), refetchInterval: 15_000 })
  const checks = useQuery({ queryKey: ['mon-checks'], queryFn: () => api.get<MonitoringCheck[]>('/monitoring/checks'), refetchInterval: 15_000 })
  const avail = useQuery({ queryKey: ['analytics-availability', win], queryFn: () => api.get<AvailabilityAnalytics>(`/analytics/availability?window=${win}`), refetchInterval: 60_000 })
  const uptime = useQuery({ queryKey: ['analytics-device-uptime', win], queryFn: () => api.get<DeviceUptime[]>(`/analytics/device-uptime?window=${win}`), refetchInterval: 60_000 })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['mon-checks'] })
    qc.invalidateQueries({ queryKey: ['mon-overview'] })
    qc.invalidateQueries({ queryKey: ['analytics-availability'] })
    qc.invalidateQueries({ queryKey: ['analytics-device-uptime'] })
  }
  const seed = useMutation({ mutationFn: () => api.post('/monitoring/seed', {}), onSuccess: invalidate })
  const run = useMutation({ mutationFn: () => api.post('/monitoring/run', {}), onSuccess: invalidate })
  const toggle = useMutation({ mutationFn: (c: MonitoringCheck) => api.patch(`/monitoring/checks/${c.id}`, { enabled: !c.enabled }), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: (id: string) => api.del(`/monitoring/checks/${id}`), onSuccess: invalidate })

  // ---- Live status snapshot (current poll) --------------------------------
  const map = new Map((overview.data ?? []).map((r) => [r.status, r.count]))
  const up = map.get('up') ?? 0, warning = map.get('warning') ?? 0, down = map.get('down') ?? 0, unknown = map.get('unknown') ?? 0
  const monitored = up + warning + down
  const health = monitored > 0 ? Math.round(((up + warning * 0.5) / monitored) * 100) : 0
  const all = checks.data ?? []

  const statusDonut = [
    { label: 'Online', value: up, color: STATUS_DONUT_COLOR.up },
    { label: 'Warning', value: warning, color: STATUS_DONUT_COLOR.warning },
    { label: 'Offline', value: down, color: STATUS_DONUT_COLOR.down },
    { label: 'Unknown', value: unknown, color: STATUS_DONUT_COLOR.unknown },
  ].filter((d) => d.value > 0)

  // ---- Window analytics (historical) --------------------------------------
  const sum = avail.data?.summary
  const bucket = avail.data?.bucket ?? 'hour'
  const series = avail.data?.series ?? []
  const labels = useMemo(() => series.map((p) => bucketLabel(p.ts, bucket)), [series, bucket])
  const uptimePoints = series.map((p) => p.uptime_pct)
  const latencyPoints = series.map((p) => p.avg_latency_ms ?? 0)
  // Floor the uptime chart a little below the lowest point (or the SLA line) so
  // small dips are visible instead of a flat 100% line.
  const uMin = Math.min(SLA_TARGET, ...(uptimePoints.length ? uptimePoints : [100]))
  const slaTone = (v?: number | null) => (v == null ? 'default' : v >= SLA_TARGET ? 'ok' : v >= 99 ? 'warn' : 'crit')

  const ranked = uptime.data ?? []
  const flapping = ranked.filter((d) => d.flaps > 0).slice(0, 8)
  const slowest = [...ranked].filter((d) => d.max_latency_ms != null).sort((a, b) => (b.max_latency_ms ?? 0) - (a.max_latency_ms ?? 0)).slice(0, 6)
  const maxLat = Math.max(50, ...slowest.map((d) => d.max_latency_ms ?? 0))

  const eventStream = [...all]
    .filter((c) => c.last_run_at)
    .sort((a, b) => new Date(b.last_run_at!).getTime() - new Date(a.last_run_at!).getTime())
    .slice(0, 8)
    .map((c) => ({
      icon: c.last_status === 'down' ? WifiOff : c.last_status === 'warning' ? TriangleAlert : Wifi,
      tone: (c.last_status === 'down' ? 'crit' : c.last_status === 'warning' ? 'warn' : 'ok') as 'crit' | 'warn' | 'ok',
      title: `${c.kind.toUpperCase()} check ${c.last_status}${c.target_port ? ` :${c.target_port}` : ''}`,
      meta: c.last_latency_ms != null ? `${c.last_latency_ms.toFixed(1)} ms · ${c.consecutive_failures} fails` : `${c.consecutive_failures} fails`,
      time: timeAgo(c.last_run_at),
    }))

  return (
    <div>
      <PageHeader
        title="Health Overview"
        subtitle="Reachability engine — hysteresis availability with full per-poll history, latency trends, and worst-performer analysis"
        icon={HeartPulse}
        actions={
          <>
            <div className="seg" role="tablist" aria-label="Analysis window">
              {WINDOWS.map((wn) => (
                <button key={wn.k} className={win === wn.k ? 'active' : ''} onClick={() => setWin(wn.k)}>{wn.label}</button>
              ))}
            </div>
            <button className="btn btn-sm" disabled={seed.isPending} onClick={() => seed.mutate()}>
              <CircleDot size={14} /> {seed.isPending ? 'Seeding…' : 'Seed checks'}
            </button>
            <button className="btn btn-primary btn-sm" disabled={run.isPending} onClick={() => run.mutate()}>
              <Play size={14} /> {run.isPending ? 'Running…' : 'Run sweep'}
            </button>
          </>
        }
      />

      <div className="kpi-grid">
        <Kpi label={`Availability · ${win}`} value={fmtPct(sum?.uptime_pct)} icon={ShieldCheck} tone={slaTone(sum?.uptime_pct)} sub={sum ? `${sum.samples.toLocaleString()} polls · SLA ${SLA_TARGET}%` : 'no data'} />
        <Kpi label="Online now" value={up} icon={Wifi} tone="ok" sub={monitored ? `${Math.round((up / monitored) * 100)}% of ${monitored}` : '—'} />
        <Kpi label="Degraded" value={warning} icon={TriangleAlert} tone={warning > 0 ? 'warn' : 'default'} sub="warning" />
        <Kpi label="Offline now" value={down} icon={WifiOff} tone={down > 0 ? 'crit' : 'default'} sub="down" />
        <Kpi label={`Avg latency · ${win}`} value={fmtMs(sum?.avg_latency_ms)} icon={Zap} tone="default" sub={`p95 ${fmtMs(sum?.p95_latency_ms)}`} />
        <Kpi label="Flapping" value={flapping.length} icon={ArrowUpDown} tone={flapping.length > 0 ? 'warn' : 'default'} sub={`devices · ${win}`} />
      </div>

      <div className="grid-side">
        <div className="stack">
          <Panel
            title={`Fleet Availability · ${win}`}
            icon={Activity}
            subtitle={sum ? `${fmtPct(sum.uptime_pct)} uptime over ${sum.devices} devices · ${sum.up.toLocaleString()}/${sum.samples.toLocaleString()} polls up` : undefined}
          >
            {series.length > 0 ? (
              <AreaChart points={uptimePoints} labels={labels} height={150} min={Math.max(0, uMin - 0.4)} max={100} unit="%" baseline={SLA_TARGET} color="var(--ok)" valueFmt={(v) => fmtPct(v)} ariaLabel="Fleet availability over time" />
            ) : (
              <EmptyState icon={HeartPulse} title="No availability history yet" message="Seed reachability checks and run a sweep; history builds up as the monitoring engine polls." action={<button className="btn btn-primary btn-sm" disabled={seed.isPending} onClick={() => seed.mutate()}>Seed default checks</button>} />
            )}
          </Panel>

          <Panel title={`Latency Trend · ${win}`} icon={Zap} subtitle={sum ? `avg ${fmtMs(sum.avg_latency_ms)} · p95 ${fmtMs(sum.p95_latency_ms)} (TCP round-trip)` : undefined}>
            {series.some((p) => p.avg_latency_ms != null) ? (
              <AreaChart points={latencyPoints} labels={labels} height={120} min={0} unit=" ms" color="var(--brand)" valueFmt={(v) => fmtMs(v)} ariaLabel="Average latency over time" />
            ) : <div className="chart-empty" style={{ height: 120 }}>No latency samples in this window</div>}
          </Panel>

          <Panel title="Availability by device" icon={ArrowUpDown} subtitle={`worst first · ${win}`} pad={false}>
            {uptime.isLoading && <div className="loading">Loading…</div>}
            {ranked.length === 0 && !uptime.isLoading && <EmptyState icon={Activity} title="No per-device history yet" message="Run a monitoring sweep to build per-device uptime." />}
            {ranked.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Device</th><th>IP</th><th>Type</th><th>Uptime</th><th>Flaps</th><th>Avg</th><th>Max</th><th>Polls</th></tr></thead>
                <tbody>
                  {ranked.slice(0, 18).map((d) => (
                    <tr key={d.device_id}>
                      <td><Link className="cell-name" to={`/devices/${d.device_id}`}>{d.name}</Link></td>
                      <td className="mono">{d.primary_ip ?? '—'}</td>
                      <td style={{ textTransform: 'capitalize' }}>{(d.category || '—').replace(/_/g, ' ')}</td>
                      <td><span className={`badge badge-${d.uptime_pct >= SLA_TARGET ? 'up' : d.uptime_pct >= 99 ? 'warning' : 'down'}`}>{fmtPct(d.uptime_pct)}</span></td>
                      <td style={{ color: d.flaps > 0 ? 'var(--warn)' : undefined, fontWeight: d.flaps > 0 ? 600 : undefined }}>{d.flaps}</td>
                      <td className="mono">{fmtMs(d.avg_latency_ms)}</td>
                      <td className="mono">{fmtMs(d.max_latency_ms)}</td>
                      <td className="muted">{d.samples.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>

          <Panel title="Checks" icon={CircleDot} subtitle={`${all.length} configured`} pad={false}>
            {checks.isLoading && <div className="loading">Loading…</div>}
            {all.length === 0 && <EmptyState icon={CircleDot} title="No checks configured" message="Click “Seed checks” to create reachability checks." />}
            {all.length > 0 && (
              <table className="data-table">
                <thead>
                  <tr><th>Kind</th><th>Port</th><th>Interval</th><th>Status</th><th>Latency</th><th>Fails</th><th>Last run</th><th></th></tr>
                </thead>
                <tbody>
                  {all.map((c) => (
                    <tr key={c.id}>
                      <td style={{ textTransform: 'uppercase', fontWeight: 600 }}>{c.kind}</td>
                      <td>{c.target_port ?? '—'}</td>
                      <td>{c.interval_seconds}s</td>
                      <td><StatusPill status={c.last_status} /></td>
                      <td className="mono">{c.last_latency_ms != null ? `${c.last_latency_ms.toFixed(1)} ms` : '—'}</td>
                      <td>{c.consecutive_failures}</td>
                      <td className="muted">{timeAgo(c.last_run_at)}</td>
                      <td className="cell-actions">
                        <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(c)}>{c.enabled ? 'Disable' : 'Enable'}</button>
                        <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => remove.mutate(c.id)}>Delete</button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </div>

        <div className="stack">
          <Panel title="Live Status" icon={HeartPulse}>
            {monitored > 0 ? (
              <div className="row" style={{ alignItems: 'center', gap: 24, flexWrap: 'wrap' }}>
                <HealthRing score={health} label="Now" />
                <Donut data={statusDonut} centerValue={monitored} centerLabel="checks" />
                <div style={{ flex: 1, minWidth: 150 }}><Legend data={statusDonut} total={monitored} /></div>
              </div>
            ) : (
              <EmptyState icon={HeartPulse} title="No checks yet" message="Seed default reachability checks, then run a sweep." action={<button className="btn btn-primary btn-sm" disabled={seed.isPending} onClick={() => seed.mutate()}>Seed default checks</button>} />
            )}
          </Panel>

          <Panel title="Flapping Devices" icon={ArrowUpDown} subtitle={`most status changes · ${win}`}>
            {flapping.length > 0 ? (
              <BarList rows={flapping.map((d) => ({ label: d.name, value: d.flaps, color: 'var(--warn)', to: `/devices/${d.device_id}` }))} />
            ) : <div className="muted" style={{ fontSize: 13 }}>No flapping — every device held a steady status across the window.</div>}
          </Panel>

          <Panel title="Highest Latency" icon={Gauge} subtitle={`peak round-trip · ${win}`}>
            {slowest.length === 0 ? <div className="muted">No latency samples yet.</div> : (
              <div className="stack" style={{ gap: 12 }}>
                {slowest.map((d) => (
                  <Meter key={d.device_id} label={d.name} value={d.max_latency_ms ?? 0} max={maxLat} unit=" ms" />
                ))}
              </div>
            )}
          </Panel>

          <Panel title="Event Stream" icon={Zap} subtitle="latest poll per check">
            <ActivityFeed items={eventStream} />
          </Panel>
        </div>
      </div>
    </div>
  )
}
