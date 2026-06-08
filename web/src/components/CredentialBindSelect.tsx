import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { api, type Device, type Credential } from '../api'

// CredentialBindSelect — bind/unbind the credential HIMS uses to collect from a
// device. Shared by the device header and (for controllers) the Manage tab so the
// control lives in exactly one place per page. Reads the device + credential list
// from the shared react-query caches; the PUT clears the binding on "".
export function CredentialBindSelect({ deviceId, align = 'start' }: { deviceId: string; align?: 'start' | 'end' }) {
  const qc = useQueryClient()
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const [msg, setMsg] = useState<string | null>(null)
  const d = (devices.data ?? []).find((x) => x.id === deviceId)

  async function setCredential(credID: string) {
    setMsg(null)
    try {
      await api.put(`/devices/${deviceId}/credential`, { credential_id: credID }) // "" clears the binding
      setMsg(credID ? 'Credential bound. Re-scan or Run Collection to use it.' : 'Credential unbound.')
      qc.invalidateQueries({ queryKey: ['devices'] })
    } catch (e) {
      setMsg(`Failed: ${(e as Error).message}`)
    }
  }

  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 11, alignItems: align === 'end' ? 'flex-end' : 'flex-start' }}
      title="Bind the credential HIMS uses to collect from this device, or set to none to unbind. Picking the wrong kind is the usual reason a device shows managed but collects nothing.">
      <span className="muted" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><KeyRound size={12} /> Collection credential</span>
      <select value={d?.credential_id ?? ''} onChange={(e) => setCredential(e.target.value)} style={{ fontSize: 12, maxWidth: 340, minWidth: 220 }}>
        <option value="">— no credential (unbind) —</option>
        {(creds.data ?? []).map((c) => (
          <option key={c.id} value={c.id}>{c.name} · {c.kind}</option>
        ))}
      </select>
      {msg && <span className="muted" style={{ fontSize: 11 }}>{msg}</span>}
    </label>
  )
}
