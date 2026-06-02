import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type WorkOrder, type WorkOrderEvent } from '../api'

const PRIORITIES = ['low', 'medium', 'high', 'critical']
const PROBLEM_TYPES = ['hardware', 'software', 'network', 'license', 'other']
const STATUSES = ['open', 'in_progress', 'waiting', 'solved', 'closed']

const prBadge = (p: string) =>
  p === 'critical' ? 'down' : p === 'high' ? 'warning' : p === 'medium' ? 'access' : 'unknown'
const stBadge = (s: string) =>
  s === 'open' ? 'down' : s === 'in_progress' ? 'warning' : s === 'solved' || s === 'closed' ? 'up' : 'unknown'

export function WorkOrders() {
  const qc = useQueryClient()
  const [selected, setSelected] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)

  const list = useQuery({ queryKey: ['work-orders'], queryFn: () => api.get<WorkOrder[]>('/work-orders') })

  return (
    <div>
      <div className="card">
        <h2>Work Orders</h2>
        <p className="muted" style={{ marginBottom: 10 }}>Asset-linked tickets — diagnosis, action, parts, cost, lifecycle + timeline.</p>
        <button onClick={() => setShowCreate((v) => !v)} style={btn}>
          {showCreate ? 'Cancel' : '+ New work order'}
        </button>
      </div>

      {showCreate && <CreateForm onDone={() => { setShowCreate(false); qc.invalidateQueries({ queryKey: ['work-orders'] }) }} />}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && list.data.length === 0 && <div className="muted">No work orders yet.</div>}
        {list.data && list.data.length > 0 && (
          <table>
            <thead><tr><th>Title</th><th>Type</th><th>Priority</th><th>Status</th><th>Assigned</th><th>Cost</th></tr></thead>
            <tbody>
              {list.data.map((w) => (
                <tr key={w.id} style={{ cursor: 'pointer' }} onClick={() => setSelected(w.id === selected ? null : w.id)}>
                  <td><strong>{w.title}</strong></td>
                  <td>{w.problem_type}</td>
                  <td><span className={`badge badge-${prBadge(w.priority)}`}>{w.priority}</span></td>
                  <td><span className={`badge badge-${stBadge(w.status)}`}>{w.status}</span></td>
                  <td>{w.assigned_to ?? '—'}</td>
                  <td>{w.cost ? w.cost.toLocaleString() : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {selected && <Detail id={selected} onChange={() => qc.invalidateQueries({ queryKey: ['work-orders'] })} />}
    </div>
  )
}

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

function CreateForm({ onDone }: { onDone: () => void }) {
  const [title, setTitle] = useState('')
  const [problemType, setProblemType] = useState('other')
  const [priority, setPriority] = useState('medium')
  const [assignedTo, setAssignedTo] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<WorkOrder>('/work-orders', {
      title, problem_type: problemType, priority, assigned_to: assignedTo || null,
    }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New work order</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 10 }}>
        <label>Title<input style={input} value={title} onChange={(e) => setTitle(e.target.value)} /></label>
        <label>Problem type
          <select style={input} value={problemType} onChange={(e) => setProblemType(e.target.value)}>
            {PROBLEM_TYPES.map((p) => <option key={p}>{p}</option>)}
          </select>
        </label>
        <label>Priority
          <select style={input} value={priority} onChange={(e) => setPriority(e.target.value)}>
            {PRIORITIES.map((p) => <option key={p}>{p}</option>)}
          </select>
        </label>
        <label>Assigned to<input style={input} value={assignedTo} onChange={(e) => setAssignedTo(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!title || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </div>
  )
}

function Detail({ id, onChange }: { id: string; onChange: () => void }) {
  const qc = useQueryClient()
  const q = useQuery({
    queryKey: ['work-order', id],
    queryFn: () => api.get<{ work_order: WorkOrder; events: WorkOrderEvent[] }>(`/work-orders/${id}`),
  })
  const [note, setNote] = useState('')
  const update = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.patch<WorkOrder>(`/work-orders/${id}`, body),
    onSuccess: () => { setNote(''); qc.invalidateQueries({ queryKey: ['work-order', id] }); onChange() },
  })
  if (q.isLoading || !q.data) return <div className="card loading">Loading detail…</div>
  const wo = q.data.work_order
  return (
    <div className="card">
      <h2>{wo.title}</h2>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        {STATUSES.map((s) => (
          <button key={s} style={{ ...btn, background: s === wo.status ? '#0d47a1' : '#90a4ae' }}
            onClick={() => update.mutate({ status: s, cost: wo.cost })}>
            {s}
          </button>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        <input style={input} placeholder="Add a timeline note…" value={note} onChange={(e) => setNote(e.target.value)} />
        <button style={btn} disabled={!note} onClick={() => update.mutate({ note, cost: wo.cost })}>Add note</button>
      </div>
      <h3 style={{ fontSize: 13, color: '#888', marginBottom: 8 }}>Timeline</h3>
      <ul style={{ listStyle: 'none', display: 'flex', flexDirection: 'column', gap: 6 }}>
        {q.data.events.map((e) => (
          <li key={e.id} style={{ fontSize: 13 }}>
            <span className="muted">{new Date(e.created_at).toLocaleString()} · {e.event_type}</span>
            {e.note && <> — {e.note}</>}
          </li>
        ))}
      </ul>
    </div>
  )
}
