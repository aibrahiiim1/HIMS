import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ClipboardList, Plus, CircleDot, Clock, TriangleAlert, Timer, Bell } from 'lucide-react'
import { api, type WorkOrder, type WorkOrderEvent, type WorkOrderAlertLink } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, ActivityFeed, timeAgo } from '../components/ui'

const PRIORITIES = ['low', 'medium', 'high', 'critical']
const PROBLEM_TYPES = ['hardware', 'software', 'network', 'license', 'other']
const STATUSES = ['open', 'in_progress', 'waiting', 'solved', 'closed']

const prCls = (p: string) => (p === 'critical' ? 'badge-down' : p === 'high' ? 'badge-warning' : p === 'medium' ? 'badge-access' : 'badge-unknown')
const stCls = (s: string) => (s === 'open' ? 'badge-down' : s === 'in_progress' ? 'badge-warning' : s === 'solved' || s === 'closed' ? 'badge-up' : 'badge-unknown')
const slaCls = (s?: string) => (s === 'breached' ? 'badge-down' : s === 'due_soon' ? 'badge-warning' : s === 'met' ? 'badge-up' : s === 'on_track' ? 'badge-info' : 'badge-unknown')
const slaLabel = (s?: string) => (s === 'breached' ? 'breached' : s === 'due_soon' ? 'due soon' : s === 'met' ? 'met' : s === 'on_track' ? 'on track' : '—')
const active = (s: string) => s !== 'solved' && s !== 'closed'

export function WorkOrders() {
  const qc = useQueryClient()
  const [selected, setSelected] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [fStatus, setFStatus] = useState('')
  const [fPriority, setFPriority] = useState('')
  const [overdueOnly, setOverdueOnly] = useState(false)
  const list = useQuery({ queryKey: ['work-orders'], queryFn: () => api.get<WorkOrder[]>('/work-orders') })

  const all = list.data ?? []
  const open = all.filter((w) => w.status === 'open').length
  const inProgress = all.filter((w) => w.status === 'in_progress').length
  const breached = all.filter((w) => w.sla_status === 'breached' && active(w.status)).length
  const dueSoon = all.filter((w) => w.sla_status === 'due_soon').length

  const shown = useMemo(() => (list.data ?? []).filter((w) =>
    (!fStatus || w.status === fStatus) &&
    (!fPriority || w.priority === fPriority) &&
    (!overdueOnly || (w.sla_status === 'breached' && active(w.status)))), [list.data, fStatus, fPriority, overdueOnly])

  return (
    <div>
      <PageHeader
        title="Work Orders" icon={ClipboardList}
        subtitle="Asset- and alert-linked tickets with SLA tracking, diagnosis, parts, cost and full timeline"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShowCreate((v) => !v)}><Plus size={14} /> {showCreate ? 'Cancel' : 'New work order'}</button>}
      />

      <div className="kpi-grid">
        <Kpi label="Open" value={open} icon={CircleDot} tone={open > 0 ? 'crit' : 'default'} />
        <Kpi label="In Progress" value={inProgress} icon={Clock} tone={inProgress > 0 ? 'warn' : 'default'} />
        <Kpi label="SLA Breached" value={breached} icon={TriangleAlert} tone={breached > 0 ? 'crit' : 'default'} sub="active tickets" />
        <Kpi label="Due Soon" value={dueSoon} icon={Timer} tone={dueSoon > 0 ? 'warn' : 'default'} />
      </div>

      {showCreate && <CreateForm onDone={() => { setShowCreate(false); qc.invalidateQueries({ queryKey: ['work-orders'] }) }} />}

      <Panel title="Tickets" icon={ClipboardList} subtitle={`${shown.length}${shown.length !== all.length ? ` of ${all.length}` : ''}`} pad={false}
        actions={
          <div className="row" style={{ gap: 6 }}>
            <label className="row" style={{ gap: 4, fontSize: 12 }}><input type="checkbox" checked={overdueOnly} onChange={(e) => setOverdueOnly(e.target.checked)} /> overdue</label>
            <select className="field" value={fPriority} onChange={(e) => setFPriority(e.target.value)}><option value="">any priority</option>{PRIORITIES.map((p) => <option key={p}>{p}</option>)}</select>
            <select className="field" value={fStatus} onChange={(e) => setFStatus(e.target.value)}><option value="">any status</option>{STATUSES.map((s) => <option key={s}>{s}</option>)}</select>
          </div>}>
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && all.length === 0 && <EmptyState icon={ClipboardList} title="No work orders yet" message="Create a ticket, or let alert rules open them automatically." action={<button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>New work order</button>} />}
        {shown.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Title</th><th>Device</th><th>Type</th><th>Priority</th><th>Status</th><th>SLA</th><th>Assigned</th></tr></thead>
            <tbody>
              {shown.map((w) => (
                <tr key={w.id} style={{ cursor: 'pointer' }} onClick={() => setSelected(w.id === selected ? null : w.id)}>
                  <td className="cell-name">{w.title}</td>
                  <td>{w.device_name ? (w.device_id ? <Link to={`/devices/${w.device_id}`} onClick={(e) => e.stopPropagation()}>{w.device_name}</Link> : w.device_name) : <span className="muted">—</span>}</td>
                  <td style={{ textTransform: 'capitalize' }}>{w.problem_type}</td>
                  <td><span className={`badge ${prCls(w.priority)}`}>{w.priority}</span></td>
                  <td><span className={`badge ${stCls(w.status)}`}>{w.status.replace('_', ' ')}</span></td>
                  <td><span className={`badge ${slaCls(w.sla_status)}`} title={w.due_at ? `due ${new Date(w.due_at).toLocaleString()}` : undefined}>{slaLabel(w.sla_status)}</span></td>
                  <td>{w.assigned_to ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {selected && <Detail id={selected} onChange={() => qc.invalidateQueries({ queryKey: ['work-orders'] })} />}
    </div>
  )
}

function CreateForm({ onDone }: { onDone: () => void }) {
  const [title, setTitle] = useState('')
  const [problemType, setProblemType] = useState('other')
  const [priority, setPriority] = useState('medium')
  const [assignedTo, setAssignedTo] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<WorkOrder>('/work-orders', { title, problem_type: problemType, priority, assigned_to: assignedTo || null }),
    onSuccess: onDone,
  })
  return (
    <Panel title="New Work Order" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 12 }}>
        <label className="form-field">Title<input className="field" value={title} onChange={(e) => setTitle(e.target.value)} /></label>
        <label className="form-field">Problem type
          <select className="field" value={problemType} onChange={(e) => setProblemType(e.target.value)}>{PROBLEM_TYPES.map((p) => <option key={p}>{p}</option>)}</select>
        </label>
        <label className="form-field">Priority
          <select className="field" value={priority} onChange={(e) => setPriority(e.target.value)}>{PRIORITIES.map((p) => <option key={p}>{p}</option>)}</select>
        </label>
        <label className="form-field">Assigned to<input className="field" value={assignedTo} onChange={(e) => setAssignedTo(e.target.value)} /></label>
      </div>
      <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>SLA target is derived from priority — critical 4h, high 1 day, medium 3 days, low 7 days.</p>
      <div style={{ marginTop: 8 }}>
        <button className="btn btn-primary" disabled={!title || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

function Detail({ id, onChange }: { id: string; onChange: () => void }) {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['work-order', id], queryFn: () => api.get<{ work_order: WorkOrder; events: WorkOrderEvent[]; linked_alerts: WorkOrderAlertLink[] }>(`/work-orders/${id}`) })
  const [note, setNote] = useState('')
  const update = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.patch<WorkOrder>(`/work-orders/${id}`, body),
    onSuccess: () => { setNote(''); qc.invalidateQueries({ queryKey: ['work-order', id] }); onChange() },
  })
  if (q.isLoading || !q.data) return <Panel title="Detail"><div className="loading">Loading detail…</div></Panel>
  const wo = q.data.work_order
  const alerts = q.data.linked_alerts ?? []
  return (
    <Panel title={wo.title} icon={ClipboardList}
      subtitle={`${wo.problem_type} · ${wo.priority}`}
      actions={<span className={`badge ${slaCls(wo.sla_status)}`}>SLA: {slaLabel(wo.sla_status)}{wo.due_at && active(wo.status) ? ` · due ${new Date(wo.due_at).toLocaleString()}` : ''}</span>}>
      <div className="row" style={{ marginBottom: 14, flexWrap: 'wrap', gap: 6 }}>
        {STATUSES.map((s) => (
          <button key={s} className={'btn btn-sm' + (s === wo.status ? ' btn-primary' : '')} onClick={() => update.mutate({ status: s, cost: wo.cost })}>{s.replace('_', ' ')}</button>
        ))}
      </div>

      {(wo.device_name || alerts.length > 0) && (
        <div className="row" style={{ gap: 18, marginBottom: 14, fontSize: 13, flexWrap: 'wrap' }}>
          {wo.device_name && <div>Device: {wo.device_id ? <Link to={`/devices/${wo.device_id}`}>{wo.device_name}</Link> : wo.device_name}</div>}
          {alerts.length > 0 && <div className="row" style={{ gap: 6 }}><Bell size={14} /> {alerts.length} linked alert{alerts.length > 1 ? 's' : ''}</div>}
        </div>
      )}

      {alerts.length > 0 && (
        <table className="data-table" style={{ marginBottom: 16 }}>
          <thead><tr><th>Alert</th><th>Severity</th><th>Status</th><th>Opened</th></tr></thead>
          <tbody>
            {alerts.map((a) => (
              <tr key={a.id}>
                <td>{a.message}</td>
                <td><span className={`badge ${a.severity === 'critical' ? 'badge-down' : a.severity === 'warning' ? 'badge-warning' : 'badge-unknown'}`}>{a.severity}</span></td>
                <td><span className={`badge ${a.status === 'resolved' ? 'badge-up' : 'badge-down'}`}>{a.status}</span></td>
                <td className="muted">{timeAgo(a.opened_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div className="row" style={{ marginBottom: 18 }}>
        <input className="field" style={{ flex: 1 }} placeholder="Add a timeline note…" value={note} onChange={(e) => setNote(e.target.value)} />
        <button className="btn btn-primary" disabled={!note} onClick={() => update.mutate({ note, cost: wo.cost })}>Add note</button>
      </div>
      <h3 style={{ fontSize: 12, color: 'var(--text-faint)', textTransform: 'uppercase', letterSpacing: '.5px', marginBottom: 8 }}>Timeline</h3>
      <ActivityFeed items={q.data.events.map((e) => ({ title: e.note || e.event_type, meta: e.event_type, time: timeAgo(e.created_at) }))} />
    </Panel>
  )
}
