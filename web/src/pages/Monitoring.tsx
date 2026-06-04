import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  HeartPulse, Activity, Play, Wifi, WifiOff, TriangleAlert,
  Gauge, Zap, CircleDot,
} from 'lucide-react'
import { api, type MonitoringCheck, type MonitoringOverviewRow } from '../api'
import {
  PageHeader, Panel, Kpi, HealthRing, Donut, Legend, BarList, Meter,
  StatusPill, EmptyState, ActivityFeed, timeAgo,
} from '../components/ui'

const STATUS_DONUT_COLOR: Record<string, string> = { up: '#16a34a', warning: '#d97706', down: '#dc2626', unknown: '#94a3b8' }

export function Monitoring() {
  const qc = useQueryClient()
  const overview = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'), refetchInterval: 15_000 })
  const checks = useQuery({ queryKey: ['mon-checks'], queryFn: () => api.get<MonitoringCheck[]>('/monitoring/checks'), refetchInterval: 15_000 })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['mon-checks'] })
    qc.invalidateQueries({ queryKey: ['mon-overview'] })
  }
  const seed = useMutation({ mutationFn: () => api.post('/monitoring/seed', {}), onSuccess: invalidate })
  const run = useMutation({ mutationFn: () => api.post('/monitoring/run', {}), onSuccess: invalidate })
  const toggle = useMutation({ mutationFn: (c: MonitoringCheck) => api.patch(`/monitoring/checks/${c.id}`, { enabled: !c.enabled }), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: (id: string) => api.del(`/monitoring/checks/${id}`), onSuccess: invalidate })

  const map = new Map((overview.data ?? []).map((r) => [r.status, r.count]))
  const up = map.get('up') ?? 0, warning = map.get('warning') ?? 0, down = map.get('down') ?? 0, unknown = map.get('unknown') ?? 0
  const monitored = up + warning + down
  const health = monitored > 0 ? Math.round(((up + warning * 0.5) / monitored) * 100) : 0

  const all = checks.data ?? []
  const withLatency = all.filter((c) => c.last_latency_ms != null)
  const avgLatency = withLatency.length ? withLatency.reduce((a, c) => a + (c.last_latency_ms ?? 0), 0) / withLatency.length : null
  const maxLat = Math.max(50, ...withLatency.map((c) => c.last_latency_ms ?? 0))

  const statusDonut = [
    { label: 'Online', value: up, color: STATUS_DONUT_COLOR.up },
    { label: 'Warning', value: warning, color: STATUS_DONUT_COLOR.warning },
    { label: 'Offline', value: down, color: STATUS_DONUT_COLOR.down },
    { label: 'Unknown', value: unknown, color: STATUS_DONUT_COLOR.unknown },
  ].filter((d) => d.value > 0)

  const statusBars = [
    { label: 'online', value: up, color: STATUS_DONUT_COLOR.up },
    { label: 'warning', value: warning, color: STATUS_DONUT_COLOR.warning },
    { label: 'offline', value: down, color: STATUS_DONUT_COLOR.down },
    { label: 'unknown', value: unknown, color: STATUS_DONUT_COLOR.unknown },
  ].filter((r) => r.value > 0)

  const slowest = [...withLatency].sort((a, b) => (b.last_latency_ms ?? 0) - (a.last_latency_ms ?? 0)).slice(0, 6)

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
        title="Monitoring"
        subtitle="Reachability engine — hysteresis-based availability with per-poll history"
        icon={HeartPulse}
        actions={
          <>
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
        <Kpi label="Monitored" value={monitored} icon={Activity} tone="info" sub={`${all.length} checks`} />
        <Kpi label="Online" value={up} icon={Wifi} tone="ok" sub={monitored ? `${Math.round((up / monitored) * 100)}%` : '—'} />
        <Kpi label="Degraded" value={warning} icon={TriangleAlert} tone={warning > 0 ? 'warn' : 'default'} sub="warning" />
        <Kpi label="Offline" value={down} icon={WifiOff} tone={down > 0 ? 'crit' : 'default'} sub="down" />
        <Kpi label="Avg Latency" value={avgLatency != null ? `${avgLatency.toFixed(0)} ms` : '—'} icon={Zap} tone="default" sub="across checks" />
      </div>

      <div className="grid-side">
        <div className="stack">
          <Panel title="Availability" icon={Activity}>
            {monitored > 0 ? (
              <div className="row" style={{ alignItems: 'center', gap: 32 }}>
                <HealthRing score={health} label="Health Score" />
                <Donut data={statusDonut} centerValue={monitored} centerLabel="checks" />
                <div style={{ flex: 1, minWidth: 160 }}><Legend data={statusDonut} total={monitored} /></div>
              </div>
            ) : (
              <EmptyState icon={HeartPulse} title="No monitoring checks yet" message="Seed default reachability checks for your devices, then run a sweep to populate availability." action={<button className="btn btn-primary btn-sm" disabled={seed.isPending} onClick={() => seed.mutate()}>Seed default checks</button>} />
            )}
          </Panel>

          <Panel title="Checks" icon={CircleDot} subtitle={`${all.length}`} pad={false}>
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
          <Panel title="Health by Status" icon={HeartPulse}>
            {statusBars.length > 0 ? <BarList rows={statusBars} /> : <div className="muted">No data.</div>}
          </Panel>

          <Panel title="Highest Latency" icon={Gauge}>
            {slowest.length === 0 ? <div className="muted">No latency samples yet.</div> : (
              <div className="stack" style={{ gap: 12 }}>
                {slowest.map((c) => (
                  <Meter key={c.id} label={`${c.kind.toUpperCase()}${c.target_port ? ` :${c.target_port}` : ''}`} value={c.last_latency_ms ?? 0} max={maxLat} unit=" ms" />
                ))}
              </div>
            )}
          </Panel>

          <Panel title="Event Stream" icon={Zap}>
            <ActivityFeed items={eventStream} />
          </Panel>
        </div>
      </div>
    </div>
  )
}
