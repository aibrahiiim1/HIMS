import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { X, Wifi } from 'lucide-react'
import { api, type Location } from '../api'

interface Props {
  onClose: () => void
  onAdded?: (deviceID: string) => void
}

type Vendor = 'extreme_xcc' | 'ruckus_zd'

const VENDOR_LABEL: Record<Vendor, string> = {
  extreme_xcc: 'Extreme — ExtremeCloud IQ Controller / XCC (REST :5825)',
  ruckus_zd: 'Ruckus ZoneDirector — Web-XML (AJAX :443)',
}
const DEFAULT_PORT: Record<Vendor, number> = { extreme_xcc: 5825, ruckus_zd: 443 }

// AddWirelessController is the one-step "Add controller" form. It posts to
// POST /wireless/controllers, which seals the credential, creates the device +
// an enabled vendor profile, and kicks the REST/XML collection (the primary path).
export function AddWirelessController({ onClose, onAdded }: Props) {
  const qc = useQueryClient()
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const [vendor, setVendor] = useState<Vendor>('extreme_xcc')
  const [f, setF] = useState({
    ip: '', name: '', username: 'admin', password: '', port: '', api_base: '', location_id: '',
  })
  const [ignoreTLS, setIgnoreTLS] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const set = (k: keyof typeof f, v: string) => setF((p) => ({ ...p, [k]: v }))

  const add = useMutation({
    mutationFn: () =>
      api.post<{ device_id: string; profile_id: string; source: string; detail: string }>('/wireless/controllers', {
        vendor,
        ip: f.ip.trim(),
        name: f.name.trim(),
        location_id: f.location_id || null,
        username: f.username.trim(),
        password: f.password,
        port: f.port ? Number(f.port) : 0,
        api_base: f.api_base.trim(),
        ssl_verify: !ignoreTLS,
      }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      onAdded?.(r.device_id)
      onClose()
    },
    onError: (e) => setErr((e as Error).message),
  })

  function submit() {
    setErr(null)
    if (!f.ip.trim()) { setErr('Controller IP is required.'); return }
    if (!f.username.trim() || !f.password) { setErr('Admin username and password are required.'); return }
    add.mutate()
  }

  const field = (label: string, node: React.ReactNode, hint?: string) => (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>
      <span className="muted">{label}</span>
      {node}
      {hint && <span className="muted" style={{ fontSize: 11 }}>{hint}</span>}
    </label>
  )
  const inputStyle = { padding: '6px 8px', fontSize: 13 } as const

  return (
    <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', justifyContent: 'flex-end', zIndex: 1000 }} onClick={onClose}>
      <div className="modal-panel" style={{ width: 460, maxWidth: '100%', height: '100%', background: 'var(--surface)', overflowY: 'auto', boxShadow: '-4px 0 24px rgba(0,0,0,.3)' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 16px', borderBottom: '1px solid var(--border)', position: 'sticky', top: 0, background: 'var(--surface)' }}>
          <strong style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}><Wifi size={16} /> Add wireless controller</strong>
          <button className="btn btn-ghost btn-sm" onClick={onClose}><X size={16} /></button>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, padding: 16 }}>
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            Collected via the vendor management API/XML as the <strong>primary</strong> method — even if the
            controller was already discovered by SNMP/SSH. The password is encrypted at rest (never stored in plain text).
          </p>

          {field('Vendor', (
            <select value={vendor} onChange={(e) => setVendor(e.target.value as Vendor)} style={inputStyle}>
              <option value="extreme_xcc">{VENDOR_LABEL.extreme_xcc}</option>
              <option value="ruckus_zd">{VENDOR_LABEL.ruckus_zd}</option>
            </select>
          ))}
          {field('Controller IP', <input value={f.ip} onChange={(e) => set('ip', e.target.value)} placeholder="172.21.96.100" style={inputStyle} />)}
          {field('Name (optional)', <input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="Aqua XIQC" style={inputStyle} />)}
          {field('Site (optional)', (
            <select value={f.location_id} onChange={(e) => set('location_id', e.target.value)} style={inputStyle}>
              <option value="">— none —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </select>
          ))}
          {field('Admin username', <input value={f.username} onChange={(e) => set('username', e.target.value)} autoComplete="off" style={inputStyle} />)}
          {field('Admin password', <input type="password" value={f.password} onChange={(e) => set('password', e.target.value)} autoComplete="new-password" style={inputStyle} />)}
          {field('Port', <input value={f.port} onChange={(e) => set('port', e.target.value)} placeholder={String(DEFAULT_PORT[vendor])} style={inputStyle} />, `Default ${DEFAULT_PORT[vendor]}`)}
          {vendor === 'extreme_xcc' && field('API base (optional)', <input value={f.api_base} onChange={(e) => set('api_base', e.target.value)} placeholder="/management/v1" style={inputStyle} />)}

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
            <input type="checkbox" checked={ignoreTLS} onChange={(e) => setIgnoreTLS(e.target.checked)} />
            Ignore TLS certificate (self-signed mgmt cert)
          </label>

          {err && <div className="error-msg" style={{ fontSize: 12 }}>{err}</div>}

          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <button className="btn btn-ghost btn-sm" onClick={onClose} disabled={add.isPending}>Cancel</button>
            <button className="btn btn-primary btn-sm" onClick={submit} disabled={add.isPending}>
              <Wifi size={14} /> {add.isPending ? 'Adding…' : 'Add & collect'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
