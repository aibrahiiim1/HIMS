import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type SparePart } from '../api'

const stockBadge = (s: string) => (s === 'out' ? 'down' : s === 'low' ? 'warning' : 'up')

const CATEGORIES = ['cable', 'transceiver', 'disk', 'memory', 'psu', 'fan', 'board', 'peripheral', 'consumable', 'other']

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

export function SpareParts() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const list = useQuery({ queryKey: ['spare-parts'], queryFn: () => api.get<SparePart[]>('/spare-parts') })
  const invalidate = () => qc.invalidateQueries({ queryKey: ['spare-parts'] })

  const adjust = useMutation({
    mutationFn: ({ id, quantity }: { id: string; quantity: number }) =>
      api.patch(`/spare-parts/${id}/stock`, { quantity }),
    onSuccess: invalidate,
  })
  const remove = useMutation({ mutationFn: (id: string) => api.del(`/spare-parts/${id}`), onSuccess: invalidate })

  return (
    <div>
      <div className="card">
        <h2>Spare Parts</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Stock inventory with reorder thresholds. Status is <em>out</em> (none on hand),
          <em> low</em> (at/below reorder point), or <em>ok</em>. Parts are consumed by work
          orders, which atomically decrement stock.
        </p>
        <button style={btn} onClick={() => setShow((v) => !v)}>{show ? 'Cancel' : '+ New part'}</button>
      </div>

      {show && <CreateForm onDone={() => { setShow(false); invalidate() }} />}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && list.data.length === 0 && <div className="muted">No parts yet.</div>}
        {list.data && list.data.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Name</th><th>SKU</th><th>Category</th><th>On hand</th><th>Min</th>
                <th>Unit cost</th><th>Status</th><th></th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((p) => (
                <tr key={p.id}>
                  <td><strong>{p.name}</strong></td>
                  <td>{p.sku ?? '—'}</td>
                  <td>{p.category}</td>
                  <td>{p.quantity}</td>
                  <td>{p.min_quantity}</td>
                  <td>{p.unit_cost ? p.unit_cost.toLocaleString() : '—'}</td>
                  <td><span className={`badge badge-${stockBadge(p.stock_status)}`}>{p.stock_status}</span></td>
                  <td style={{ display: 'flex', gap: 6 }}>
                    <button style={ghost} onClick={() => {
                      const v = prompt(`Set on-hand quantity for ${p.name}`, String(p.quantity))
                      if (v != null && !Number.isNaN(Number(v))) adjust.mutate({ id: p.id, quantity: Number(v) })
                    }}>Adjust</button>
                    <button style={ghost} onClick={() => remove.mutate(p.id)}>Delete</button>
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

function CreateForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [sku, setSku] = useState('')
  const [category, setCategory] = useState('other')
  const [quantity, setQuantity] = useState('0')
  const [minQuantity, setMinQuantity] = useState('0')
  const [unitCost, setUnitCost] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<SparePart>('/spare-parts', {
      name, sku: sku || null, category,
      quantity: Number(quantity) || 0, min_quantity: Number(minQuantity) || 0,
      unit_cost: unitCost ? Number(unitCost) : 0,
    }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New spare part</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 10 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>SKU<input style={input} value={sku} onChange={(e) => setSku(e.target.value)} /></label>
        <label>Category
          <select style={input} value={category} onChange={(e) => setCategory(e.target.value)}>
            {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <label>Quantity<input style={input} type="number" value={quantity} onChange={(e) => setQuantity(e.target.value)} /></label>
        <label>Min quantity<input style={input} type="number" value={minQuantity} onChange={(e) => setMinQuantity(e.target.value)} /></label>
        <label>Unit cost<input style={input} type="number" value={unitCost} onChange={(e) => setUnitCost(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!name || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </div>
  )
}
