import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, TriangleAlert, CircleCheck, ListChecks, Play, Plus, Wrench, ArrowUpCircle, Clock, X, Trash2 } from 'lucide-react'
import { api, type Alert, type AlertRule, type AlertEvent, type MaintenanceWindow, type Device, type Location, locationPaths } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, StatusPill, TabBar, timeAgo } from '../components/ui'

const sevCls = (s: string) => (s === 'critical' ? 'badge-down' : s === 'warning' ? 'badge-warning' : 'badge-unknown')
const SeverityBadge = ({ s }: { s: string }) => <span className={`badge ${sevCls(s)}`}>{s}</span>
type Tab = 'alerts' | 'rules' | 'maintenance'

export function Alerts() {
  const qc = useQueryClient()
  const [tab, setTab] = useState<Tab>('alerts')
  const [showRule, setShowRule] = useState(false)
  const [timelineFor, setTimelineFor] = useState<Alert | null>(null)
  const [statusF, setStatusF] = useState('active')
  const [now] = useState(() => Date.now())

  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 15_000 })
  const rules = useQuery({ queryKey: ['alert-rules'], queryFn: () => api.get<AlertRule[]>('/alert-rules') })
  const windows = useQuery({ queryKey: ['maintenance-windows'], queryFn: () => api.get<MaintenanceWindow[]>('/maintenance-windows') })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['alerts'] })
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    qc.invalidateQueries({ queryKey: ['maintenance-windows'] })
  }
  const evaluate = useMutation({ mutationFn: () => api.post('/alerts/evaluate', {}), onSuccess: invalidate })
  const ack = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/ack`, {}), onSuccess: invalidate })
  const resolve = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/resolve`, {}), onSuccess: invalidate })
  const toggleRule = useMutation({ mutationFn: (r: AlertRule) => api.patch(`/alert-rules/${r.id}`, { enabled: !r.enabled }), onSuccess: invalidate })
  const delRule = useMutation({ mutationFn: (id: string) => api.del(`/alert-rules/${id}`), onSuccess: invalidate })

  const list = useMemo(() => alerts.data ?? [], [alerts.data])
  const open = list.filter((a) => a.status === 'open').length
  const critical = list.filter((a) => a.severity === 'critical' && a.status !== 'resolved').length
  const acked = list.filter((a) => a.status === 'acknowledged').length
  const escalated = list.filter((a) => a.escalated && a.status !== 'resolved').length
  const activeRules = (rules.data ?? []).filter((r) => r.enabled).length
  const activeWindows = (windows.data ?? []).filter((w) => Date.parse(w.ends_at) > now && Date.parse(w.starts_at) <= now).length

  const shownAlerts = useMemo(() => list.filter((a) =>
    statusF === 'all' ? true : statusF === 'active' ? a.status !== 'resolved' : a.status === statusF), [list, statusF])

  return (
    <div>
      <PageHeader
        title="Alerts" icon={Bell}
        subtitle="Rule-driven alerting — dedup, auto work-orders, auto-resolve, escalation, maintenance suppression"
        actions={
          <>
            <button className="btn btn-sm" disabled={evaluate.isPending} onClick={() => evaluate.mutate()}>
              <Play size={14} /> {evaluate.isPending ? 'Evaluating…' : 'Evaluate now'}
            </button>
            {tab === 'rules' && <button className="btn btn-primary btn-sm" onClick={() => setShowRule((v) => !v)}><Plus size={14} /> {showRule ? 'Cancel' : 'New rule'}</button>}
          </>
        }
      />

      <div className="kpi-grid">
        <Kpi label="Open Alerts" value={open} icon={Bell} tone={open > 0 ? 'crit' : 'default'} sub="unresolved" />
        <Kpi label="Critical" value={critical} icon={TriangleAlert} tone={critical > 0 ? 'crit' : 'default'} sub="active" />
        <Kpi label="Escalated" value={escalated} icon={ArrowUpCircle} tone={escalated > 0 ? 'crit' : 'default'} sub="unacknowledged" />
        <Kpi label="Acknowledged" value={acked} icon={CircleCheck} tone={acked > 0 ? 'warn' : 'default'} sub="in handling" />
        <Kpi label="Maintenance" value={activeWindows} icon={Wrench} tone={activeWindows > 0 ? 'info' : 'default'} sub="active windows" />
      </div>

      <TabBar
        tabs={[
          { key: 'alerts', label: 'Alerts', icon: Bell, count: open || undefined },
          { key: 'rules', label: 'Rules', icon: ListChecks, count: activeRules || undefined },
          { key: 'maintenance', label: 'Maintenance', icon: Wrench, count: activeWindows || undefined },
        ]}
        active={tab} onChange={(k) => setTab(k as Tab)}
      />

      {tab === 'alerts' && (
        <Panel title="Active & Recent Alerts" icon={Bell} subtitle={`${shownAlerts.length}`} pad={false}
          actions={
            <select className="field" style={{ width: 150 }} value={statusF} onChange={(e) => setStatusF(e.target.value)}>
              <option value="active">Active (open+ack)</option>
              <option value="open">Open</option>
              <option value="acknowledged">Acknowledged</option>
              <option value="resolved">Resolved</option>
              <option value="all">All</option>
            </select>
          }>
          {alerts.isLoading && <div className="loading">Loading…</div>}
          {alerts.data && shownAlerts.length === 0 && <EmptyState icon={CircleCheck} title="No alerts" message="Nothing matches this filter — monitored devices are within their alerting thresholds." />}
          {shownAlerts.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Severity</th><th>Status</th><th>Message</th><th>Opened</th><th>WO</th><th></th></tr></thead>
              <tbody>
                {shownAlerts.map((a) => (
                  <tr key={a.id}>
                    <td><SeverityBadge s={a.severity} />{a.escalated && <span className="badge badge-down" style={{ marginLeft: 6 }}><ArrowUpCircle size={11} /> esc</span>}</td>
                    <td><StatusPill status={a.status === 'open' ? 'down' : a.status === 'acknowledged' ? 'warning' : 'up'} label={a.status} /></td>
                    <td className="cell-name">{a.message}{a.acknowledged_by && <small className="muted"> · ack by {a.acknowledged_by}</small>}</td>
                    <td className="muted">{timeAgo(a.opened_at)}</td>
                    <td>{a.work_order_id ? '✓' : '—'}</td>
                    <td className="cell-actions">
                      <button className="btn btn-ghost btn-xs" onClick={() => setTimelineFor(a)}><Clock size={12} /> Timeline</button>
                      {a.status === 'open' && <button className="btn btn-ghost btn-xs" onClick={() => ack.mutate(a.id)}>Ack</button>}
                      {a.status !== 'resolved' && <button className="btn btn-ghost btn-xs" onClick={() => resolve.mutate(a.id)}>Resolve</button>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'rules' && (
        <>
          {showRule && <RuleForm onDone={() => { setShowRule(false); invalidate() }} />}
          <Panel title="Alert Rules" icon={ListChecks} subtitle={`${rules.data?.length ?? 0}`} pad={false}>
            {rules.data && rules.data.length === 0 && <EmptyState icon={ListChecks} title="No alert rules" message="Add a rule to start alerting on monitoring state." action={<button className="btn btn-primary btn-sm" onClick={() => setShowRule(true)}>New rule</button>} />}
            {rules.data && rules.data.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Name</th><th>Trigger</th><th>Min fails</th><th>Category</th><th>Severity</th><th>Auto WO</th><th>Escalate</th><th>Enabled</th><th></th></tr></thead>
                <tbody>
                  {rules.data.map((r) => (
                    <tr key={r.id}>
                      <td className="cell-name">{r.name}</td>
                      <td>{r.trigger_status}</td>
                      <td>{r.min_failures}</td>
                      <td>{r.device_category ?? 'any'}</td>
                      <td><SeverityBadge s={r.severity} /></td>
                      <td>{r.auto_work_order ? `yes (${r.work_order_priority})` : 'no'}</td>
                      <td>{r.escalate_after_minutes > 0 ? `${r.escalate_after_minutes}m` : '—'}</td>
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
        </>
      )}

      {tab === 'maintenance' && <MaintenanceTab windows={windows.data ?? []} onChange={invalidate} />}

      {timelineFor && <TimelineDrawer alert={timelineFor} onClose={() => setTimelineFor(null)} onChange={invalidate} />}
    </div>
  )
}

function TimelineDrawer({ alert, onClose, onChange }: { alert: Alert; onClose: () => void; onChange: () => void }) {
  const qc = useQueryClient()
  const [note, setNote] = useState('')
  const events = useQuery({ queryKey: ['alert-timeline', alert.id], queryFn: () => api.get<AlertEvent[]>(`/alerts/${alert.id}/timeline`) })
  const addNote = useMutation({
    mutationFn: () => api.post(`/alerts/${alert.id}/note`, { note }),
    onSuccess: () => { setNote(''); qc.invalidateQueries({ queryKey: ['alert-timeline', alert.id] }); onChange() },
  })
  const kindColor: Record<string, string> = { opened: 'var(--crit)', acknowledged: 'var(--warn)', resolved: 'var(--ok)', escalated: 'var(--crit)', note: 'var(--text-muted)', suppressed: 'var(--text-muted)' }
  return (
    <div className="drawer-scrim" onClick={onClose}>
      <div className="drawer" onClick={(e) => e.stopPropagation()} style={{ position: 'fixed', top: 0, right: 0, height: '100vh', width: 'min(460px,92vw)', background: 'var(--surface)', borderLeft: '1px solid var(--border)', boxShadow: '-8px 0 24px rgba(0,0,0,.18)', padding: 20, overflowY: 'auto', zIndex: 60 }}>
        <div className="row" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <div><h3 style={{ margin: 0, fontSize: 15 }}>Alert Timeline</h3><div className="muted" style={{ fontSize: 12, marginTop: 4 }}>{alert.message}</div></div>
          <button className="btn btn-ghost btn-xs" onClick={onClose}><X size={15} /></button>
        </div>
        <div className="stack" style={{ gap: 0, marginTop: 16 }}>
          {events.isLoading && <div className="loading">Loading…</div>}
          {(events.data ?? []).map((ev) => (
            <div key={ev.id} style={{ display: 'flex', gap: 10, padding: '8px 0', borderBottom: '1px solid var(--surface-3)' }}>
              <span style={{ width: 8, height: 8, borderRadius: '50%', background: kindColor[ev.kind] ?? 'var(--text-muted)', marginTop: 5, flex: '0 0 auto' }} />
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 13, fontWeight: 600, textTransform: 'capitalize' }}>{ev.kind} <span className="muted" style={{ fontWeight: 400 }}>· {ev.actor}</span></div>
                {ev.note && <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>{ev.note}</div>}
                <div className="muted" style={{ fontSize: 11, marginTop: 2 }}>{new Date(ev.at).toLocaleString()} · {timeAgo(ev.at)}</div>
              </div>
            </div>
          ))}
          {events.data && events.data.length === 0 && <div className="muted" style={{ fontSize: 13 }}>No timeline events yet.</div>}
        </div>
        <div style={{ marginTop: 16 }}>
          <textarea className="field" rows={2} style={{ width: '100%', resize: 'vertical' }} value={note} onChange={(e) => setNote(e.target.value)} placeholder="Add a note…" />
          <button className="btn btn-primary btn-sm" style={{ marginTop: 8 }} disabled={!note.trim() || addNote.isPending} onClick={() => addNote.mutate()}>Add note</button>
        </div>
      </div>
    </div>
  )
}

function MaintenanceTab({ windows, onChange }: { windows: MaintenanceWindow[]; onChange: () => void }) {
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])
  const devName = useMemo(() => new Map((devices.data ?? []).map((d) => [d.id, d.name])), [devices.data])

  const [scope, setScope] = useState('device')
  const [deviceId, setDeviceId] = useState('')
  const [locationId, setLocationId] = useState('')
  const [reason, setReason] = useState('')
  const [hours, setHours] = useState('2')
  const [now] = useState(() => Date.now())

  const create = useMutation({
    mutationFn: () => {
      const now = new Date()
      const end = new Date(now.getTime() + (Number(hours) || 1) * 3600_000)
      return api.post('/maintenance-windows', {
        scope, reason,
        device_id: scope === 'device' ? deviceId : null,
        location_id: scope === 'site' ? locationId : null,
        starts_at: now.toISOString(), ends_at: end.toISOString(),
      })
    },
    onSuccess: () => { setReason(''); setDeviceId(''); onChange() },
  })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/maintenance-windows/${id}`), onSuccess: onChange })
  const active = (w: MaintenanceWindow) => Date.parse(w.ends_at) > now && Date.parse(w.starts_at) <= now

  return (
    <>
      <Panel title="Schedule Maintenance Window" icon={Wrench} subtitle="Suppresses new alerts for the scope until it ends">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12, alignItems: 'end' }}>
          <label className="form-field">Scope
            <select className="field" value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="device">Device</option><option value="site">Site</option><option value="global">Global (all)</option>
            </select>
          </label>
          {scope === 'device' && (
            <label className="form-field">Device
              <select className="field" value={deviceId} onChange={(e) => setDeviceId(e.target.value)}>
                <option value="">Select…</option>
                {(devices.data ?? []).map((d) => <option key={d.id} value={d.id}>{d.name} {d.primary_ip ? `(${d.primary_ip})` : ''}</option>)}
              </select>
            </label>
          )}
          {scope === 'site' && (
            <label className="form-field">Site / location
              <select className="field" value={locationId} onChange={(e) => setLocationId(e.target.value)}>
                <option value="">Select…</option>
                {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id] ?? l.name}</option>)}
              </select>
            </label>
          )}
          <label className="form-field">Duration (hours)<input className="field" type="number" min="1" value={hours} onChange={(e) => setHours(e.target.value)} /></label>
          <label className="form-field">Reason<input className="field" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. firmware upgrade" /></label>
        </div>
        <button className="btn btn-primary btn-sm" style={{ marginTop: 14 }}
          disabled={create.isPending || (scope === 'device' && !deviceId) || (scope === 'site' && !locationId)}
          onClick={() => create.mutate()}>
          <Plus size={14} /> {create.isPending ? 'Scheduling…' : 'Schedule window'}
        </button>
        {create.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(create.error as Error).message}</span>}
      </Panel>

      <Panel title="Maintenance Windows" icon={Wrench} subtitle={`${windows.length}`} pad={false}>
        {windows.length === 0 && <EmptyState icon={Wrench} title="No maintenance windows" message="Schedule one to suppress alerts during planned work." />}
        {windows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Scope</th><th>Target</th><th>Reason</th><th>Window</th><th>State</th><th></th></tr></thead>
            <tbody>
              {windows.map((w) => (
                <tr key={w.id}>
                  <td>{w.scope}</td>
                  <td className="cell-name">{w.scope === 'global' ? 'All devices' : w.scope === 'device' ? (devName.get(w.device_id ?? '') ?? w.device_id) : (locPath[w.location_id ?? ''] ?? w.location_id)}</td>
                  <td className="muted">{w.reason || '—'}</td>
                  <td className="muted">{new Date(w.starts_at).toLocaleString()} → {new Date(w.ends_at).toLocaleTimeString()}</td>
                  <td>{active(w) ? <span className="badge badge-warning">active</span> : Date.parse(w.starts_at) > now ? <span className="badge badge-unknown">scheduled</span> : <span className="badge badge-disabled">ended</span>}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(w.id)}><Trash2 size={12} /> Cancel</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
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
  const [escalate, setEscalate] = useState('0')
  const m = useMutation({
    mutationFn: () => api.post<AlertRule>('/alert-rules', {
      name, trigger_status: triggerStatus, min_failures: Number(minFailures) || 0,
      device_category: category || null, severity,
      auto_work_order: autoWo, work_order_priority: woPriority,
      escalate_after_minutes: Number(escalate) || 0,
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
        <label className="form-field">Escalate after (min, 0 = never)<input className="field" type="number" min="0" value={escalate} onChange={(e) => setEscalate(e.target.value)} /></label>
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
