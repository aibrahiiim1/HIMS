import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type SystemLicense } from '../api'

const statusBadge = (s: string) =>
  s === 'expired' || s === 'critical' ? 'down'
    : s === 'due_soon' || s === 'expiring' ? 'warning'
    : s === 'active' ? 'up' : 'unknown'

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

export function Systems() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const list = useQuery({ queryKey: ['systems'], queryFn: () => api.get<SystemLicense[]>('/systems') })

  return (
    <div>
      <div className="card">
        <h2>Systems &amp; Licenses</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Software systems + contracts with license/support expiry. Status is computed live
          (active / expiring 90d / due-soon 30d / critical 7d / expired).
        </p>
        <button style={btn} onClick={() => setShow((v) => !v)}>{show ? 'Cancel' : '+ New system'}</button>
      </div>

      {show && <CreateForm onDone={() => { setShow(false); qc.invalidateQueries({ queryKey: ['systems'] }) }} />}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.data && list.data.length === 0 && <div className="muted">No systems registered yet.</div>}
        {list.data && list.data.length > 0 && (
          <table>
            <thead>
              <tr><th>System</th><th>Vendor</th><th>License expiry</th><th>Support expiry</th><th>Cost</th><th>Status</th></tr>
            </thead>
            <tbody>
              {list.data.map((s) => (
                <tr key={s.id}>
                  <td><strong>{s.name}</strong></td>
                  <td>{s.vendor ?? '—'}</td>
                  <td>{s.license_expiry ?? '—'}</td>
                  <td>{s.support_expiry ?? '—'}</td>
                  <td>{s.cost ? s.cost.toLocaleString() : '—'}</td>
                  <td><span className={`badge badge-${statusBadge(s.overall_status)}`}>{s.overall_status}</span></td>
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
  const [vendor, setVendor] = useState('')
  const [licenseExpiry, setLicenseExpiry] = useState('')
  const [supportExpiry, setSupportExpiry] = useState('')
  const [cost, setCost] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<SystemLicense>('/systems', {
      name, vendor: vendor || null,
      license_expiry: licenseExpiry || null,
      support_expiry: supportExpiry || null,
      cost: cost ? Number(cost) : 0,
    }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New system / license</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 10 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>Vendor<input style={input} value={vendor} onChange={(e) => setVendor(e.target.value)} /></label>
        <label>License expiry<input style={input} type="date" value={licenseExpiry} onChange={(e) => setLicenseExpiry(e.target.value)} /></label>
        <label>Support expiry<input style={input} type="date" value={supportExpiry} onChange={(e) => setSupportExpiry(e.target.value)} /></label>
        <label>Annual cost<input style={input} type="number" value={cost} onChange={(e) => setCost(e.target.value)} /></label>
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
