import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, TriangleAlert, CircleCheck, ListChecks, Play, Plus } from 'lucide-react'
import { api, type Alert, type AlertRule } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, StatusPill, timeAgo } from '../components/ui'

const sevCls = (s: string) => (s === 'critical' ? 'badge-down' : s === 'warning' ? 'badge-warning' : 'badge-unknown')
const SeverityBadge = ({ s }: { s: string }) => <span className={`badge ${sevCls(s)}`}>{s}</span>

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
  const toggleRule = useMutation({ mutationFn: (r: AlertRule) => api.patch(`/alert-rules/${r.id}`, { enabled: !r.enabled }), onSuccess: invalidate })
  const delRule = useMutation({ mutationFn: (id: string) => api.del(`/alert-rules/${id}`), onSuccess: invalidate })

  const list = alerts.data ?? []
  const open = list.filter((a) => a.status === 'open').length
  const critical = list.filter((a) => a.severity === 'critical' && a.status !== 'resolved').length
  const acked = list.filter((a) => a.status === 'acknowledged').length
  const activeRules = (rules.data ?? []).filter((r) => r.enabled).length

  return (
    <div>
      <PageHeader
        title="Alerts" icon={Bell}
        subtitle="Rule-driven alerting on monitoring state — auto work-orders, auto-resolve on recovery"
        actions={
          <>
            <button className="btn btn-sm" disabled={evaluate.isPending} onClick={() => evaluate.mutate()}>
              <Play size={14} /> {evaluate.isPending ? 'Evaluating…' : 'Evaluate now'}
            </button>
            <button className="btn btn-primary btn-sm" onClick={() => setShowRule((v) => !v)}>
              <Plus size={14} /> {showRule ? 'Cancel' : 'New rule'}
            </button>
          </>
        }
      />

      <div className="kpi-grid">
        <Kpi label="Open Alerts" value={open} icon={Bell} tone={open > 0 ? 'crit' : 'default'} sub="unresolved" />
        <Kpi label="Critical" value={critical} icon={TriangleAlert} tone={critical > 0 ? 'crit' : 'default'} sub="active" />
        <Kpi label="Acknowledged" value={acked} icon={CircleCheck} tone={acked > 0 ? 'warn' : 'default'} sub="in handling" />
        <Kpi label="Active Rules" value={activeRules} icon={ListChecks} tone="info" sub={`${rules.data?.length ?? 0} total`} />
      </div>

      {showRule && <RuleForm onDone={() => { setShowRule(false); invalidate() }} />}

      <Panel title="Active & Recent Alerts" icon={Bell} subtitle={`${list.length}`} pad={false}>
        {alerts.isLoading && <div className="loading">Loading…</div>}
        {alerts.data && list.length === 0 && <EmptyState icon={CircleCheck} title="No active alerts" message="All monitored devices are within their alerting thresholds." />}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Severity</th><th>Status</th><th>Message</th><th>Opened</th><th>WO</th><th></th></tr></thead>
            <tbody>
              {list.map((a) => (
                <tr key={a.id}>
                  <td><SeverityBadge s={a.severity} /></td>
                  <td><StatusPill status={a.status === 'open' ? 'down' : a.status === 'acknowledged' ? 'warning' : 'up'} label={a.status} /></td>
                  <td className="cell-name">{a.message}</td>
                  <td className="muted">{timeAgo(a.opened_at)}</td>
                  <td>{a.work_order_id ? '✓' : '—'}</td>
                  <td className="cell-actions">
                    {a.status === 'open' && <button className="btn btn-ghost btn-xs" onClick={() => ack.mutate(a.id)}>Ack</button>}
                    {a.status !== 'resolved' && <button className="btn btn-ghost btn-xs" onClick={() => resolve.mutate(a.id)}>Resolve</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Alert Rules" icon={ListChecks} subtitle={`${rules.data?.length ?? 0}`} pad={false}>
        {rules.data && rules.data.length === 0 && <EmptyState icon={ListChecks} title="No alert rules" message="Add a rule to start alerting on monitoring state." action={<button className="btn btn-primary btn-sm" onClick={() => setShowRule(true)}>New rule</button>} />}
        {rules.data && rules.data.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Trigger</th><th>Min fails</th><th>Category</th><th>Severity</th><th>Auto WO</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {rules.data.map((r) => (
                <tr key={r.id}>
                  <td className="cell-name">{r.name}</td>
                  <td>{r.trigger_status}</td>
                  <td>{r.min_failures}</td>
                  <td>{r.device_category ?? 'any'}</td>
                  <td><SeverityBadge s={r.severity} /></td>
                  <td>{r.auto_work_order ? `yes (${r.work_order_priority})` : 'no'}</td>
                  <td>{r.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" onClick={() => toggleRule.mutate(r)}>{r.enabled ? 'Disable' : 'Enable'}</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => delRule.mutate(r.id)}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
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
    <Panel title="New Alert Rule" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label className="form-field">Trigger status
          <select className="field" value={triggerStatus} onChange={(e) => setTriggerStatus(e.target.value)}>
            <option value="down">down</option><option value="warning">warning</option>
          </select>
        </label>
        <label className="form-field">Min failures<input className="field" type="number" value={minFailures} onChange={(e) => setMinFailures(e.target.value)} /></label>
        <label className="form-field">Category (blank = any)<input className="field" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="switch / firewall / …" /></label>
        <label className="form-field">Severity
          <select className="field" value={severity} onChange={(e) => setSeverity(e.target.value)}>
            <option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
          </select>
        </label>
        <label className="form-field">Auto work-order
          <select className="field" value={autoWo ? 'yes' : 'no'} onChange={(e) => setAutoWo(e.target.value === 'yes')}>
            <option value="no">no</option><option value="yes">yes</option>
          </select>
        </label>
        {autoWo && (
          <label className="form-field">WO priority
            <select className="field" value={woPriority} onChange={(e) => setWoPriority(e.target.value)}>
              <option value="low">low</option><option value="medium">medium</option>
              <option value="high">high</option><option value="critical">critical</option>
            </select>
          </label>
        )}
      </div>
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!name || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create rule'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
