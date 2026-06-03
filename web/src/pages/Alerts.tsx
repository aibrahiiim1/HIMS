import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Alert, type AlertRule } from '../api'

const sevBadge = (s: string) => (s === 'critical' ? 'down' : s === 'warning' ? 'warning' : 'unknown')
const statusBadge = (s: string) => (s === 'open' ? 'down' : s === 'acknowledged' ? 'warning' : 'up')

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 10px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

export function Alerts() {
  const qc = useQueryClient()
  const [showRule, setShowRule] = useState(false)
  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 15_000 })
  const rules = useQuery({ queryKey: ['alert-rules'], queryFn: () => api.get<AlertRule[]>('/alert-rules') })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['alerts'] })
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
  }
  const evaluate = useMutation({ mutationFn: () => api.post('/alerts/evaluate', {}), onSuccess: invalidate })
  const ack = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/ack`, {}), onSuccess: invalidate })
  const resolve = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/resolve`, {}), onSuccess: invalidate })
  const toggleRule = useMutation({
    mutationFn: (r: AlertRule) => api.patch(`/alert-rules/${r.id}`, { enabled: !r.enabled }),
    onSuccess: invalidate,
  })
  const delRule = useMutation({ mutationFn: (id: string) => api.del(`/alert-rules/${id}`), onSuccess: invalidate })

  return (
    <div>
      <div className="card">
        <h2>Alerts</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Rules match monitoring state (a check's status + consecutive failures). A match opens an
          alert; rules flagged <em>auto work-order</em> also create a linked ticket. Alerts
          auto-resolve when the check recovers. Evaluation runs after each monitoring sweep.
        </p>
        <div style={{ display: 'flex', gap: 10 }}>
          <button style={btn} disabled={evaluate.isPending} onClick={() => evaluate.mutate()}>
            {evaluate.isPending ? 'Evaluating…' : 'Evaluate now'}
          </button>
          <button style={btn} onClick={() => setShowRule((v) => !v)}>{showRule ? 'Cancel' : '+ New rule'}</button>
        </div>
      </div>

      {showRule && <RuleForm onDone={() => { setShowRule(false); invalidate() }} />}

      <div className="card">
        <h3>Active &amp; recent alerts</h3>
        {alerts.isLoading && <div className="loading">Loading…</div>}
        {alerts.data && alerts.data.length === 0 && <div className="muted">No alerts.</div>}
        {alerts.data && alerts.data.length > 0 && (
          <table>
            <thead>
              <tr><th>Severity</th><th>Status</th><th>Message</th><th>Opened</th><th>WO</th><th></th></tr>
            </thead>
            <tbody>
              {alerts.data.map((a) => (
                <tr key={a.id}>
                  <td><span className={`badge badge-${sevBadge(a.severity)}`}>{a.severity}</span></td>
                  <td><span className={`badge badge-${statusBadge(a.status)}`}>{a.status}</span></td>
                  <td>{a.message}</td>
                  <td>{new Date(a.opened_at).toLocaleString()}</td>
                  <td>{a.work_order_id ? '✓' : '—'}</td>
                  <td style={{ display: 'flex', gap: 6 }}>
                    {a.status === 'open' && <button style={ghost} onClick={() => ack.mutate(a.id)}>Ack</button>}
                    {a.status !== 'resolved' && <button style={ghost} onClick={() => resolve.mutate(a.id)}>Resolve</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h3>Rules</h3>
        {rules.data && rules.data.length === 0 && <div className="muted">No rules — add one to start alerting.</div>}
        {rules.data && rules.data.length > 0 && (
          <table>
            <thead>
              <tr><th>Name</th><th>Trigger</th><th>Min fails</th><th>Category</th><th>Severity</th><th>Auto WO</th><th>Enabled</th><th></th></tr>
            </thead>
            <tbody>
              {rules.data.map((r) => (
                <tr key={r.id}>
                  <td><strong>{r.name}</strong></td>
                  <td>{r.trigger_status}</td>
                  <td>{r.min_failures}</td>
                  <td>{r.device_category ?? 'any'}</td>
                  <td><span className={`badge badge-${sevBadge(r.severity)}`}>{r.severity}</span></td>
                  <td>{r.auto_work_order ? `yes (${r.work_order_priority})` : 'no'}</td>
                  <td>{r.enabled ? 'yes' : 'no'}</td>
                  <td style={{ display: 'flex', gap: 6 }}>
                    <button style={ghost} onClick={() => toggleRule.mutate(r)}>{r.enabled ? 'Disable' : 'Enable'}</button>
                    <button style={ghost} onClick={() => delRule.mutate(r.id)}>Delete</button>
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

function RuleForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [triggerStatus, setTriggerStatus] = useState('down')
  const [minFailures, setMinFailures] = useState('2')
  const [category, setCategory] = useState('')
  const [severity, setSeverity] = useState('warning')
  const [autoWo, setAutoWo] = useState(false)
  const [woPriority, setWoPriority] = useState('high')
  const m = useMutation({
    mutationFn: () => api.post<AlertRule>('/alert-rules', {
      name, trigger_status: triggerStatus, min_failures: Number(minFailures) || 0,
      device_category: category || null, severity,
      auto_work_order: autoWo, work_order_priority: woPriority,
    }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New alert rule</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 10 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>Trigger status
          <select style={input} value={triggerStatus} onChange={(e) => setTriggerStatus(e.target.value)}>
            <option value="down">down</option><option value="warning">warning</option>
          </select>
        </label>
        <label>Min failures<input style={input} type="number" value={minFailures} onChange={(e) => setMinFailures(e.target.value)} /></label>
        <label>Category (blank = any)<input style={input} value={category} onChange={(e) => setCategory(e.target.value)} placeholder="switch / firewall / …" /></label>
        <label>Severity
          <select style={input} value={severity} onChange={(e) => setSeverity(e.target.value)}>
            <option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
          </select>
        </label>
        <label>Auto work-order
          <select style={input} value={autoWo ? 'yes' : 'no'} onChange={(e) => setAutoWo(e.target.value === 'yes')}>
            <option value="no">no</option><option value="yes">yes</option>
          </select>
        </label>
        {autoWo && (
          <label>WO priority
            <select style={input} value={woPriority} onChange={(e) => setWoPriority(e.target.value)}>
              <option value="low">low</option><option value="medium">medium</option>
              <option value="high">high</option><option value="critical">critical</option>
            </select>
          </label>
        )}
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!name || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create rule'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </div>
  )
}
