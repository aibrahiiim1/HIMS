import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { BadgeCheck, Plus, ShieldCheck, CalendarX, CircleDollarSign, X, Wrench } from 'lucide-react'
import { api, type AssetLifecycle, type AssetRegister, type Device, type WorkOrder } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

// Map the derived license-status enum to warranty / EOL display.
const warrantyLabel = (s: string) => ({ active: 'in warranty', expiring: 'expiring', due_soon: 'expiring soon', critical: 'expiring soon', expired: 'out of warranty', unknown: '—' }[s] ?? s)
const eolLabel = (s: string) => ({ active: 'supported', expiring: 'approaching EOL', due_soon: 'approaching EOL', critical: 'approaching EOL', expired: 'end of life', unknown: '—' }[s] ?? s)
const statusCls = (s: string) => (s === 'expired' ? 'badge-down' : s === 'critical' || s === 'due_soon' ? 'badge-warning' : s === 'expiring' ? 'badge-access' : s === 'active' ? 'badge-up' : 'badge-unknown')

export function AssetLifecycle() {
  const qc = useQueryClient()
  const reg = useQuery({ queryKey: ['asset-register'], queryFn: () => api.get<AssetRegister>('/assets/lifecycle') })
  const [edit, setEdit] = useState<string | null>(null) // device_id being edited
  const [adding, setAdding] = useState(false)
  const inv = () => qc.invalidateQueries({ queryKey: ['asset-register'] })

  const data = reg.data
  const assets = data?.assets ?? []
  const sm = data?.summary ?? {}

  return (
    <div>
      <PageHeader title="Asset Lifecycle" icon={BadgeCheck}
        subtitle="Warranty, end-of-life, owner, cost and maintenance history per asset"
        actions={<button className="btn btn-primary btn-sm" onClick={() => { setAdding(true); setEdit(null) }}><Plus size={14} /> Track asset</button>} />

      <div className="kpi-grid">
        <Kpi label="In Warranty" value={sm.in_warranty ?? 0} icon={ShieldCheck} tone="ok" />
        <Kpi label="Warranty Expiring" value={sm.warranty_expiring ?? 0} icon={ShieldCheck} tone={(sm.warranty_expiring ?? 0) > 0 ? 'warn' : 'default'} />
        <Kpi label="Out of Warranty" value={sm.warranty_expired ?? 0} icon={CalendarX} tone={(sm.warranty_expired ?? 0) > 0 ? 'crit' : 'default'} />
        <Kpi label="End of Life" value={sm.eol ?? 0} icon={CalendarX} tone={(sm.eol ?? 0) > 0 ? 'crit' : 'default'} sub={`${sm.eol_approaching ?? 0} approaching`} />
        <Kpi label="Asset Value" value={data ? `$${(data.total_cost).toLocaleString()}` : '—'} icon={CircleDollarSign} tone="info" sub={`${data?.total ?? 0} tracked`} />
      </div>

      {adding && <AssetEditor onClose={() => setAdding(false)} onSaved={() => { setAdding(false); inv() }} />}

      <Panel title="Asset Register" icon={BadgeCheck} subtitle={`${assets.length}`} pad={false}>
        {reg.isLoading && <div className="loading">Loading…</div>}
        {data && assets.length === 0 && (
          <EmptyState icon={BadgeCheck} title="No assets tracked yet"
            message="Use “Track asset” to record purchase date, warranty, end-of-life, owner and cost for a device. Warranty/EOL status is computed from the dates." />
        )}
        {assets.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Device</th><th>Owner</th><th>Purchased</th><th>Warranty</th><th>EOL</th><th>Cost</th><th></th></tr></thead>
            <tbody>
              {assets.map((a) => (
                <tr key={a.device_id}>
                  <td className="cell-name"><Link to={`/devices/${a.device_id}`}>{a.device_name}</Link><div className="muted" style={{ fontSize: 11 }}>{a.category}{a.primary_ip ? ` · ${a.primary_ip}` : ''}</div></td>
                  <td>{a.owner || '—'}</td>
                  <td className="muted">{a.purchase_date || '—'}</td>
                  <td><span className={`badge ${statusCls(a.warranty_status)}`}>{warrantyLabel(a.warranty_status)}</span>{a.warranty_expiry ? <div className="muted" style={{ fontSize: 11 }}>{a.warranty_expiry}</div> : null}</td>
                  <td><span className={`badge ${statusCls(a.eol_status)}`}>{eolLabel(a.eol_status)}</span>{a.eol_date ? <div className="muted" style={{ fontSize: 11 }}>{a.eol_date}</div> : null}</td>
                  <td>{a.cost ? `$${a.cost.toLocaleString()}` : '—'}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" onClick={() => { setEdit(a.device_id); setAdding(false) }}>Edit</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {edit && <AssetEditor deviceId={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); inv() }} />}
    </div>
  )
}

function AssetEditor({ deviceId, onClose, onSaved }: { deviceId?: string; onClose: () => void; onSaved: () => void }) {
  const isNew = !deviceId
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api.get<Device[]>('/devices?category=all'), enabled: isNew })
  const existing = useQuery({ queryKey: ['lifecycle', deviceId], queryFn: () => api.get<AssetLifecycle>(`/devices/${deviceId}/lifecycle`), enabled: !isNew })
  const [pick, setPick] = useState('')
  const id = deviceId ?? pick
  const wo = useQuery({ queryKey: ['device-wos', id], queryFn: () => api.get<WorkOrder[]>(`/devices/${id}/work-orders`), enabled: !!id })

  const [form, setForm] = useState<Partial<AssetLifecycle>>({})
  const cur = useMemo(() => ({ ...(existing.data ?? {}), ...form }), [existing.data, form])
  const set = (k: keyof AssetLifecycle, v: string | number) => setForm((p) => ({ ...p, [k]: v }))

  const save = useMutation({
    mutationFn: () => api.put(`/devices/${id}/lifecycle`, {
      owner: cur.owner ?? '', supplier: cur.supplier ?? '',
      purchase_date: cur.purchase_date || null, warranty_expiry: cur.warranty_expiry || null, eol_date: cur.eol_date || null,
      cost: Number(cur.cost ?? 0), notes: cur.notes ?? '',
    }),
    onSuccess: onSaved,
  })

  return (
    <Panel title={isNew ? 'Track Asset' : 'Edit Asset Lifecycle'} icon={BadgeCheck}
      actions={<button className="btn btn-ghost btn-sm" onClick={onClose}><X size={14} /></button>}>
      {isNew && (
        <label className="form-field" style={{ maxWidth: 360, marginBottom: 12 }}>Device
          <select className="field" value={pick} onChange={(e) => setPick(e.target.value)}>
            <option value="">Select a device…</option>
            {(devices.data ?? []).map((d) => <option key={d.id} value={d.id}>{d.name} ({d.category})</option>)}
          </select>
        </label>
      )}
      {id && (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 12 }}>
            <label className="form-field">Owner<input className="field" value={cur.owner ?? ''} onChange={(e) => set('owner', e.target.value)} placeholder="IT / Front Office…" /></label>
            <label className="form-field">Supplier<input className="field" value={cur.supplier ?? ''} onChange={(e) => set('supplier', e.target.value)} /></label>
            <label className="form-field">Purchase date<input className="field" type="date" value={cur.purchase_date ?? ''} onChange={(e) => set('purchase_date', e.target.value)} /></label>
            <label className="form-field">Warranty expiry<input className="field" type="date" value={cur.warranty_expiry ?? ''} onChange={(e) => set('warranty_expiry', e.target.value)} /></label>
            <label className="form-field">End-of-life date<input className="field" type="date" value={cur.eol_date ?? ''} onChange={(e) => set('eol_date', e.target.value)} /></label>
            <label className="form-field">Cost<input className="field" type="number" value={cur.cost ?? 0} onChange={(e) => set('cost', Number(e.target.value))} /></label>
            <label className="form-field" style={{ gridColumn: '1 / -1' }}>Notes<input className="field" value={cur.notes ?? ''} onChange={(e) => set('notes', e.target.value)} /></label>
          </div>
          <div style={{ marginTop: 12 }}>
            <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{save.isPending ? 'Saving…' : 'Save'}</button>
          </div>

          <h4 style={{ margin: '18px 0 6px', display: 'flex', alignItems: 'center', gap: 6 }}><Wrench size={15} /> Maintenance History <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>(linked work orders)</span></h4>
          {(wo.data ?? []).length === 0
            ? <p className="muted" style={{ fontSize: 13 }}>No work orders linked to this device.</p>
            : (
              <table className="data-table">
                <thead><tr><th>Title</th><th>Priority</th><th>Status</th><th>Cost</th></tr></thead>
                <tbody>
                  {(wo.data ?? []).map((w) => (
                    <tr key={w.id}><td className="cell-name">{w.title}</td><td>{w.priority}</td><td>{w.status.replace('_', ' ')}</td><td>{w.cost ? `$${w.cost.toLocaleString()}` : '—'}</td></tr>
                  ))}
                </tbody>
              </table>
            )}
        </>
      )}
    </Panel>
  )
}
