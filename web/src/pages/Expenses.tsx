import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { DollarSign, Plus, Receipt, Tag, MapPin } from 'lucide-react'
import { api, type ExpenseByCategory, type ExpenseByLocation, type Purchase } from '../api'
import { PageHeader, Panel, Kpi, BarList, EmptyState, colorFor } from '../components/ui'

const CATEGORIES = ['hardware', 'software', 'license', 'contract', 'internet', 'repair', 'part', 'other']

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

  const cats = byCat.data ?? []
  const grandTotal = cats.reduce((a, r) => a + r.total, 0)
  const topCat = [...cats].sort((a, b) => b.total - a.total)[0]
  const catRows = cats.map((r) => ({ label: r.category, value: Math.round(r.total), color: colorFor(r.category) }))
  const locRows = (byLoc.data ?? []).map((r) => ({ label: r.location_name ?? '(unassigned)', value: Math.round(r.total), color: colorFor(r.location_name ?? 'x') }))

  return (
    <div>
      <PageHeader
        title="Expenses" icon={DollarSign}
        subtitle="Purchase ledger — the source of truth; category and site rollups derive from it"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShow((v) => !v)}><Plus size={14} /> {show ? 'Cancel' : 'New purchase'}</button>}
      />

      <div className="kpi-grid">
        <Kpi label="Total Spend" value={grandTotal.toLocaleString()} icon={DollarSign} tone="info" />
        <Kpi label="Purchases" value={purchases.data?.length ?? 0} icon={Receipt} tone="default" />
        <Kpi label="Categories" value={cats.length} icon={Tag} tone="default" />
        <Kpi label="Top Category" value={topCat ? topCat.category : '—'} icon={Tag} tone="default" sub={topCat ? topCat.total.toLocaleString() : undefined} />
      </div>

      {show && <CreateForm onDone={() => { setShow(false); invalidate() }} />}

      <div className="grid-2">
        <Panel title="By Category" icon={Tag}>
          {catRows.length ? <BarList rows={catRows} /> : <div className="muted">No purchases.</div>}
        </Panel>
        <Panel title="By Location" icon={MapPin}>
          {locRows.length ? <BarList rows={locRows} /> : <div className="muted">No purchases.</div>}
        </Panel>
      </div>

      <Panel title="Purchases" icon={Receipt} subtitle={`${purchases.data?.length ?? 0}`} pad={false}>
        {purchases.isLoading && <div className="loading">Loading…</div>}
        {purchases.data && purchases.data.length === 0 && <EmptyState icon={Receipt} title="No purchases recorded" message="Log hardware, software, license, contract and repair spend here." action={<button className="btn btn-primary btn-sm" onClick={() => setShow(true)}>New purchase</button>} />}
        {purchases.data && purchases.data.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Date</th><th>Description</th><th>Vendor</th><th>Category</th><th>Amount</th><th></th></tr></thead>
            <tbody>
              {purchases.data.map((p) => (
                <tr key={p.id}>
                  <td className="muted">{p.purchased_at?.slice(0, 10)}</td>
                  <td className="cell-name">{p.description}</td>
                  <td>{p.vendor ?? '—'}</td>
                  <td style={{ textTransform: 'capitalize' }}>{p.category}</td>
                  <td>{p.amount.toLocaleString()}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => remove.mutate(p.id)}>Delete</button></td>
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
  const [description, setDescription] = useState('')
  const [vendor, setVendor] = useState('')
  const [category, setCategory] = useState('other')
  const [amount, setAmount] = useState('')
  const [purchasedAt, setPurchasedAt] = useState('')
  const [invoiceRef, setInvoiceRef] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<Purchase>('/purchases', {
      description, vendor: vendor || null, category, amount: amount ? Number(amount) : 0,
      purchased_at: purchasedAt || null, invoice_ref: invoiceRef || null,
    }),
    onSuccess: onDone,
  })
  return (
    <Panel title="New Purchase" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 12 }}>
        <label className="form-field">Description<input className="field" value={description} onChange={(e) => setDescription(e.target.value)} /></label>
        <label className="form-field">Vendor<input className="field" value={vendor} onChange={(e) => setVendor(e.target.value)} /></label>
        <label className="form-field">Category
          <select className="field" value={category} onChange={(e) => setCategory(e.target.value)}>{CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}</select>
        </label>
        <label className="form-field">Amount<input className="field" type="number" value={amount} onChange={(e) => setAmount(e.target.value)} /></label>
        <label className="form-field">Date<input className="field" type="date" value={purchasedAt} onChange={(e) => setPurchasedAt(e.target.value)} /></label>
        <label className="form-field">Invoice ref<input className="field" value={invoiceRef} onChange={(e) => setInvoiceRef(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!description || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
