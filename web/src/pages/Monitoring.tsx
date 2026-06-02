import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type MonitoringCheck, type MonitoringOverviewRow } from '../api'

const statusBadge = (s: string) =>
  s === 'down' ? 'down' : s === 'warning' ? 'warning' : s === 'up' ? 'up' : 'unknown'

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 10px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12,
}

export function Monitoring() {
  const qc = useQueryClient()
  const overview = useQuery({
    queryKey: ['mon-overview'],
    queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview'),
    refetchInterval: 15_000,
  })
  const checks = useQuery({
    queryKey: ['mon-checks'],
    queryFn: () => api.get<MonitoringCheck[]>('/monitoring/checks'),
    refetchInterval: 15_000,
  })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['mon-checks'] })
    qc.invalidateQueries({ queryKey: ['mon-overview'] })
  }
  const seed = useMutation({ mutationFn: () => api.post('/monitoring/seed', {}), onSuccess: invalidate })
  const run = useMutation({ mutationFn: () => api.post('/monitoring/run', {}), onSuccess: invalidate })
  const toggle = useMutation({
    mutationFn: (c: MonitoringCheck) => api.patch(`/monitoring/checks/${c.id}`, { enabled: !c.enabled }),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/monitoring/checks/${id}`),
    onSuccess: invalidate,
  })

  const counts = new Map((overview.data ?? []).map((r) => [r.status, r.count]))

  return (
    <div>
      <div className="card">
        <h2>Monitoring</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Reachability engine: each device has a TCP check polled on its interval. Status uses
          hysteresis (a single missed poll is <em>warning</em>; sustained misses past the
          threshold are <em>down</em>). History is recorded per poll.
        </p>
        <div style={{ display: 'flex', gap: 18, margin: '12px 0' }}>
          {['up', 'warning', 'down', 'unknown'].map((s) => (
            <div key={s} style={{ textAlign: 'center' }}>
              <div style={{ fontSize: 26, fontWeight: 700 }}>{counts.get(s) ?? 0}</div>
              <span className={`badge badge-${statusBadge(s)}`}>{s}</span>
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 10 }}>
          <button style={btn} disabled={seed.isPending} onClick={() => seed.mutate()}>
            {seed.isPending ? 'Seeding…' : 'Seed default checks'}
          </button>
          <button style={btn} disabled={run.isPending} onClick={() => run.mutate()}>
            {run.isPending ? 'Running…' : 'Run sweep now'}
          </button>
        </div>
      </div>

      <div className="card">
        {checks.isLoading && <div className="loading">Loading…</div>}
        {checks.data && checks.data.length === 0 && (
          <div className="muted">No checks yet — click “Seed default checks”.</div>
        )}
        {checks.data && checks.data.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Kind</th><th>Port</th><th>Interval</th><th>Status</th>
                <th>Latency</th><th>Fails</th><th>Last run</th><th>Enabled</th><th></th>
              </tr>
            </thead>
            <tbody>
              {checks.data.map((c) => (
                <tr key={c.id}>
                  <td>{c.kind}</td>
                  <td>{c.target_port ?? '—'}</td>
                  <td>{c.interval_seconds}s</td>
                  <td><span className={`badge badge-${statusBadge(c.last_status)}`}>{c.last_status}</span></td>
                  <td>{c.last_latency_ms != null ? `${c.last_latency_ms.toFixed(1)} ms` : '—'}</td>
                  <td>{c.consecutive_failures}</td>
                  <td>{c.last_run_at ? new Date(c.last_run_at).toLocaleString() : 'never'}</td>
                  <td>{c.enabled ? 'yes' : 'no'}</td>
                  <td style={{ display: 'flex', gap: 6 }}>
                    <button style={ghost} onClick={() => toggle.mutate(c)}>
                      {c.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button style={ghost} onClick={() => remove.mutate(c.id)}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
