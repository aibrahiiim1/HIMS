import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type ExpenseByCategory, type ExpenseByLocation, type Purchase } from '../api'

const CATEGORIES = ['hardware', 'software', 'license', 'contract', 'internet', 'repair', 'part', 'other']

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

export function Expenses() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const purchases = useQuery({ queryKey: ['purchases'], queryFn: () => api.get<Purchase[]>('/purchases') })
  const byCat = useQuery({ queryKey: ['exp-cat'], queryFn: () => api.get<ExpenseByCategory[]>('/expenses/by-category') })
  const byLoc = useQuery({ queryKey: ['exp-loc'], queryFn: () => api.get<ExpenseByLocation[]>('/expenses/by-location') })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['purchases'] })
    qc.invalidateQueries({ queryKey: ['exp-cat'] })
    qc.invalidateQueries({ queryKey: ['exp-loc'] })
  }
  const remove = useMutation({ mutationFn: (id: string) => api.del(`/purchases/${id}`), onSuccess: invalidate })

  const grandTotal = (byCat.data ?? []).reduce((a, r) => a + r.total, 0)

  return (
    <div>
      <div className="card">
        <h2>Expenses</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          The purchases ledger (hardware / software / licenses / contracts / internet / repairs /
          parts) is the source of truth; the rollups below derive from it, so totals never drift.
          Total recorded: <strong>{grandTotal.toLocaleString()}</strong>.
        </p>
        <button style={btn} onClick={() => setShow((v) => !v)}>{show ? 'Cancel' : '+ New purchase'}</button>
      </div>

      {show && <CreateForm onDone={() => { setShow(false); invalidate() }} />}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <div className="card">
          <h3>By category</h3>
          <table>
            <thead><tr><th>Category</th><th>Total</th><th>#</th></tr></thead>
            <tbody>
              {(byCat.data ?? []).map((r) => (
                <tr key={r.category}><td>{r.category}</td><td>{r.total.toLocaleString()}</td><td>{r.count}</td></tr>
              ))}
              {byCat.data && byCat.data.length === 0 && <tr><td colSpan={3} className="muted">No purchases.</td></tr>}
            </tbody>
          </table>
        </div>
        <div className="card">
          <h3>By location</h3>
          <table>
            <thead><tr><th>Location</th><th>Total</th><th>#</th></tr></thead>
            <tbody>
              {(byLoc.data ?? []).map((r) => (
                <tr key={r.location_id ?? 'none'}>
                  <td>{r.location_name ?? '(unassigned)'}</td><td>{r.total.toLocaleString()}</td><td>{r.count}</td>
                </tr>
              ))}
              {byLoc.data && byLoc.data.length === 0 && <tr><td colSpan={3} className="muted">No purchases.</td></tr>}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card">
        <h3>Purchases</h3>
        {purchases.isLoading && <div className="loading">Loading…</div>}
        {purchases.data && purchases.data.length > 0 && (
          <table>
            <thead>
              <tr><th>Date</th><th>Description</th><th>Vendor</th><th>Category</th><th>Amount</th><th></th></tr>
            </thead>
            <tbody>
              {purchases.data.map((p) => (
                <tr key={p.id}>
                  <td>{p.purchased_at?.slice(0, 10)}</td>
                  <td><strong>{p.description}</strong></td>
                  <td>{p.vendor ?? '—'}</td>
                  <td>{p.category}</td>
                  <td>{p.amount.toLocaleString()}</td>
                  <td>
                    <button style={{ ...btn, padding: '4px 10px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', fontSize: 12 }}
                      onClick={() => remove.mutate(p.id)}>Delete</button>
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
  const [description, setDescription] = useState('')
  const [vendor, setVendor] = useState('')
  const [category, setCategory] = useState('other')
  const [amount, setAmount] = useState('')
  const [purchasedAt, setPurchasedAt] = useState('')
  const [invoiceRef, setInvoiceRef] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<Purchase>('/purchases', {
      description, vendor: vendor || null, category,
      amount: amount ? Number(amount) : 0,
      purchased_at: purchasedAt || null,
      invoice_ref: invoiceRef || null,
    }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New purchase</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 10 }}>
        <label>Description<input style={input} value={description} onChange={(e) => setDescription(e.target.value)} /></label>
        <label>Vendor<input style={input} value={vendor} onChange={(e) => setVendor(e.target.value)} /></label>
        <label>Category
          <select style={input} value={category} onChange={(e) => setCategory(e.target.value)}>
            {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <label>Amount<input style={input} type="number" value={amount} onChange={(e) => setAmount(e.target.value)} /></label>
        <label>Date<input style={input} type="date" value={purchasedAt} onChange={(e) => setPurchasedAt(e.target.value)} /></label>
        <label>Invoice ref<input style={input} value={invoiceRef} onChange={(e) => setInvoiceRef(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!description || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </div>
  )
}
