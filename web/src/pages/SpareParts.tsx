import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Package, Plus, TriangleAlert, CircleX, DollarSign } from 'lucide-react'
import { api, type SparePart } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

const stockCls = (s: string) => (s === 'out' ? 'badge-down' : s === 'low' ? 'badge-warning' : 'badge-up')
const CATEGORIES = ['cable', 'transceiver', 'disk', 'memory', 'psu', 'fan', 'board', 'peripheral', 'consumable', 'other']

export function SpareParts() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const list = useQuery({ queryKey: ['spare-parts'], queryFn: () => api.get<SparePart[]>('/spare-parts') })
  const invalidate = () => qc.invalidateQueries({ queryKey: ['spare-parts'] })

  const adjust = useMutation({ mutationFn: ({ id, quantity }: { id: string; quantity: number }) => api.patch(`/spare-parts/${id}/stock`, { quantity }), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: (id: string) => api.del(`/spare-parts/${id}`), onSuccess: invalidate })

  const all = list.data ?? []
  const low = all.filter((p) => p.stock_status === 'low').length
  const out = all.filter((p) => p.stock_status === 'out').length
  const value = all.reduce((a, p) => a + (p.unit_cost ?? 0) * p.quantity, 0)

  return (
    <div>
      <PageHeader
        title="Spare Parts" icon={Package}
        subtitle="Stock inventory with reorder thresholds — consumed atomically by work orders"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShow((v) => !v)}><Plus size={14} /> {show ? 'Cancel' : 'New part'}</button>}
      />

      <div className="kpi-grid">
        <Kpi label="SKUs" value={all.length} icon={Package} tone="info" />
        <Kpi label="Low Stock" value={low} icon={TriangleAlert} tone={low > 0 ? 'warn' : 'default'} sub="at/below reorder" />
        <Kpi label="Out of Stock" value={out} icon={CircleX} tone={out > 0 ? 'crit' : 'default'} />
        <Kpi label="Inventory Value" value={value ? value.toLocaleString() : '0'} icon={DollarSign} tone="default" />
      </div>

      {show && <CreateForm onDone={() => { setShow(false); invalidate() }} />}

      <Panel title="Parts" icon={Package} subtitle={`${all.length}`} pad={false}>
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && all.length === 0 && <EmptyState icon={Package} title="No spare parts yet" message="Track stock levels and reorder points for hardware spares." action={<button className="btn btn-primary btn-sm" onClick={() => setShow(true)}>New part</button>} />}
        {all.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>SKU</th><th>Category</th><th>On hand</th><th>Min</th><th>Unit cost</th><th>Status</th><th></th></tr></thead>
            <tbody>
              {all.map((p) => (
                <tr key={p.id}>
                  <td className="cell-name">{p.name}</td>
                  <td className="mono">{p.sku ?? '—'}</td>
                  <td style={{ textTransform: 'capitalize' }}>{p.category}</td>
                  <td>{p.quantity}</td>
                  <td>{p.min_quantity}</td>
                  <td>{p.unit_cost ? p.unit_cost.toLocaleString() : '—'}</td>
                  <td><span className={`badge ${stockCls(p.stock_status)}`}>{p.stock_status}</span></td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" onClick={() => {
                      const v = prompt(`Set on-hand quantity for ${p.name}`, String(p.quantity))
                      if (v != null && !Number.isNaN(Number(v))) adjust.mutate({ id: p.id, quantity: Number(v) })
                    }}>Adjust</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => remove.mutate(p.id)}>Delete</button>
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

function CreateForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [sku, setSku] = useState('')
  const [category, setCategory] = useState('other')
  const [quantity, setQuantity] = useState('0')
  const [minQuantity, setMinQuantity] = useState('0')
  const [unitCost, setUnitCost] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<SparePart>('/spare-parts', {
      name, sku: sku || null, category, quantity: Number(quantity) || 0,
      min_quantity: Number(minQuantity) || 0, unit_cost: unitCost ? Number(unitCost) : 0,
    }),
    onSuccess: onDone,
  })
  return (
    <Panel title="New Spare Part" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 12 }}>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label className="form-field">SKU<input className="field" value={sku} onChange={(e) => setSku(e.target.value)} /></label>
        <label className="form-field">Category
          <select className="field" value={category} onChange={(e) => setCategory(e.target.value)}>{CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}</select>
        </label>
        <label className="form-field">Quantity<input className="field" type="number" value={quantity} onChange={(e) => setQuantity(e.target.value)} /></label>
        <label className="form-field">Min quantity<input className="field" type="number" value={minQuantity} onChange={(e) => setMinQuantity(e.target.value)} /></label>
        <label className="form-field">Unit cost<input className="field" type="number" value={unitCost} onChange={(e) => setUnitCost(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!name || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
