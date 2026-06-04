import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ClipboardList, Plus, CircleDot, Clock, TriangleAlert, DollarSign } from 'lucide-react'
import { api, type WorkOrder, type WorkOrderEvent } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, ActivityFeed, timeAgo } from '../components/ui'

const PRIORITIES = ['low', 'medium', 'high', 'critical']
const PROBLEM_TYPES = ['hardware', 'software', 'network', 'license', 'other']
const STATUSES = ['open', 'in_progress', 'waiting', 'solved', 'closed']

const prCls = (p: string) => (p === 'critical' ? 'badge-down' : p === 'high' ? 'badge-warning' : p === 'medium' ? 'badge-access' : 'badge-unknown')
const stCls = (s: string) => (s === 'open' ? 'badge-down' : s === 'in_progress' ? 'badge-warning' : s === 'solved' || s === 'closed' ? 'badge-up' : 'badge-unknown')

export function WorkOrders() {
  const qc = useQueryClient()
  const [selected, setSelected] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const list = useQuery({ queryKey: ['work-orders'], queryFn: () => api.get<WorkOrder[]>('/work-orders') })

  const all = list.data ?? []
  const open = all.filter((w) => w.status === 'open').length
  const inProgress = all.filter((w) => w.status === 'in_progress').length
  const critical = all.filter((w) => w.priority === 'critical' && w.status !== 'closed' && w.status !== 'solved').length
  const totalCost = all.reduce((a, w) => a + (w.cost ?? 0), 0)

  return (
    <div>
      <PageHeader
        title="Work Orders" icon={ClipboardList}
        subtitle="Asset-linked tickets — diagnosis, action, parts, cost, lifecycle and timeline"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShowCreate((v) => !v)}><Plus size={14} /> {showCreate ? 'Cancel' : 'New work order'}</button>}
      />

      <div className="kpi-grid">
        <Kpi label="Open" value={open} icon={CircleDot} tone={open > 0 ? 'crit' : 'default'} />
        <Kpi label="In Progress" value={inProgress} icon={Clock} tone={inProgress > 0 ? 'warn' : 'default'} />
        <Kpi label="Critical" value={critical} icon={TriangleAlert} tone={critical > 0 ? 'crit' : 'default'} sub="active priority" />
        <Kpi label="Total Cost" value={totalCost ? totalCost.toLocaleString() : '0'} icon={DollarSign} tone="info" sub="all tickets" />
      </div>

      {showCreate && <CreateForm onDone={() => { setShowCreate(false); qc.invalidateQueries({ queryKey: ['work-orders'] }) }} />}

      <Panel title="Tickets" icon={ClipboardList} subtitle={`${all.length}`} pad={false}>
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && all.length === 0 && <EmptyState icon={ClipboardList} title="No work orders yet" message="Create a ticket, or let alert rules open them automatically." action={<button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>New work order</button>} />}
        {all.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Title</th><th>Type</th><th>Priority</th><th>Status</th><th>Assigned</th><th>Cost</th></tr></thead>
            <tbody>
              {all.map((w) => (
                <tr key={w.id} style={{ cursor: 'pointer' }} onClick={() => setSelected(w.id === selected ? null : w.id)}>
                  <td className="cell-name">{w.title}</td>
                  <td style={{ textTransform: 'capitalize' }}>{w.problem_type}</td>
                  <td><span className={`badge ${prCls(w.priority)}`}>{w.priority}</span></td>
                  <td><span className={`badge ${stCls(w.status)}`}>{w.status.replace('_', ' ')}</span></td>
                  <td>{w.assigned_to ?? '—'}</td>
                  <td>{w.cost ? w.cost.toLocaleString() : '—'}</td>
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
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!title || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

function Detail({ id, onChange }: { id: string; onChange: () => void }) {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['work-order', id], queryFn: () => api.get<{ work_order: WorkOrder; events: WorkOrderEvent[] }>(`/work-orders/${id}`) })
  const [note, setNote] = useState('')
  const update = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.patch<WorkOrder>(`/work-orders/${id}`, body),
    onSuccess: () => { setNote(''); qc.invalidateQueries({ queryKey: ['work-order', id] }); onChange() },
  })
  if (q.isLoading || !q.data) return <Panel title="Detail"><div className="loading">Loading detail…</div></Panel>
  const wo = q.data.work_order
  return (
    <Panel title={wo.title} icon={ClipboardList} subtitle={`${wo.problem_type} · ${wo.priority}`}>
      <div className="row" style={{ marginBottom: 14 }}>
        {STATUSES.map((s) => (
          <button key={s} className={'btn btn-sm' + (s === wo.status ? ' btn-primary' : '')} onClick={() => update.mutate({ status: s, cost: wo.cost })}>{s.replace('_', ' ')}</button>
        ))}
      </div>
      <div className="row" style={{ marginBottom: 18 }}>
        <input className="field" style={{ flex: 1 }} placeholder="Add a timeline note…" value={note} onChange={(e) => setNote(e.target.value)} />
        <button className="btn btn-primary" disabled={!note} onClick={() => update.mutate({ note, cost: wo.cost })}>Add note</button>
      </div>
      <h3 style={{ fontSize: 12, color: 'var(--text-faint)', textTransform: 'uppercase', letterSpacing: '.5px', marginBottom: 8 }}>Timeline</h3>
      <ActivityFeed items={q.data.events.map((e) => ({ title: e.note || e.event_type, meta: e.event_type, time: timeAgo(e.created_at) }))} />
    </Panel>
  )
}
