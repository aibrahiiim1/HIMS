import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Wrench, Plus, CircleCheck, Clock, ShieldAlert, DollarSign } from 'lucide-react'
import { api, type SystemLicense } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

const stCls = (s: string) =>
  s === 'expired' || s === 'critical' ? 'badge-down'
    : s === 'due_soon' || s === 'expiring' ? 'badge-warning'
    : s === 'active' ? 'badge-up' : 'badge-unknown'

export function Systems() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const list = useQuery({ queryKey: ['systems'], queryFn: () => api.get<SystemLicense[]>('/systems') })

  const all = list.data ?? []
  const active = all.filter((s) => s.overall_status === 'active').length
  const expiring = all.filter((s) => ['expiring', 'due_soon'].includes(s.overall_status)).length
  const expired = all.filter((s) => ['expired', 'critical'].includes(s.overall_status)).length
  const cost = all.reduce((a, s) => a + (s.cost ?? 0), 0)

  return (
    <div>
      <PageHeader
        title="Systems & Licenses" icon={Wrench}
        subtitle="Software systems and contracts with live license/support expiry status"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShow((v) => !v)}><Plus size={14} /> {show ? 'Cancel' : 'New system'}</button>}
      />

      <div className="kpi-grid">
        <Kpi label="Systems" value={all.length} icon={Wrench} tone="info" />
        <Kpi label="Active" value={active} icon={CircleCheck} tone="ok" />
        <Kpi label="Expiring" value={expiring} icon={Clock} tone={expiring > 0 ? 'warn' : 'default'} sub="≤ 90 days" />
        <Kpi label="Expired / Critical" value={expired} icon={ShieldAlert} tone={expired > 0 ? 'crit' : 'default'} />
        <Kpi label="Annual Cost" value={cost ? cost.toLocaleString() : '0'} icon={DollarSign} tone="default" />
      </div>

      {show && <CreateForm onDone={() => { setShow(false); qc.invalidateQueries({ queryKey: ['systems'] }) }} />}

      <Panel title="Systems" icon={Wrench} subtitle={`${all.length}`} pad={false}>
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && all.length === 0 && <EmptyState icon={Wrench} title="No systems registered" message="Track software systems, licenses and support contracts with expiry alerts." action={<button className="btn btn-primary btn-sm" onClick={() => setShow(true)}>New system</button>} />}
        {all.length > 0 && (
          <table className="data-table">
            <thead><tr><th>System</th><th>Vendor</th><th>License expiry</th><th>Support expiry</th><th>Cost</th><th>Status</th></tr></thead>
            <tbody>
              {all.map((s) => (
                <tr key={s.id}>
                  <td className="cell-name">{s.name}</td>
                  <td>{s.vendor ?? '—'}</td>
                  <td>{s.license_expiry ?? '—'}</td>
                  <td>{s.support_expiry ?? '—'}</td>
                  <td>{s.cost ? s.cost.toLocaleString() : '—'}</td>
                  <td><span className={`badge ${stCls(s.overall_status)}`}>{s.overall_status.replace('_', ' ')}</span></td>
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
  const [vendor, setVendor] = useState('')
  const [licenseExpiry, setLicenseExpiry] = useState('')
  const [supportExpiry, setSupportExpiry] = useState('')
  const [cost, setCost] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<SystemLicense>('/systems', {
      name, vendor: vendor || null, license_expiry: licenseExpiry || null,
      support_expiry: supportExpiry || null, cost: cost ? Number(cost) : 0,
    }),
    onSuccess: onDone,
  })
  return (
    <Panel title="New System / License" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 12 }}>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label className="form-field">Vendor<input className="field" value={vendor} onChange={(e) => setVendor(e.target.value)} /></label>
        <label className="form-field">License expiry<input className="field" type="date" value={licenseExpiry} onChange={(e) => setLicenseExpiry(e.target.value)} /></label>
        <label className="form-field">Support expiry<input className="field" type="date" value={supportExpiry} onChange={(e) => setSupportExpiry(e.target.value)} /></label>
        <label className="form-field">Annual cost<input className="field" type="number" value={cost} onChange={(e) => setCost(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!name || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
