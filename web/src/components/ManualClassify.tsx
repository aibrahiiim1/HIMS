import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Tag } from 'lucide-react'
import { api, type Device } from '../api'

// ManualClassify is an operator-safe classification override for a device. It marks the
// device as POS / Biometric / another endpoint subtype (or clears the manual lock), which:
//   - sets category + locks classification (classification_locked=true) so automatic
//     discovery will NOT silently overwrite the operator's decision (apply.reconcile honors
//     the lock), and records a manual_classification_reason → classification_source shows
//     "manual_override".
//   - is AUDITED server-side (device.update audit event).
//   - changes ONLY type/subtype + the lock — never vendor/model/serial (those stay as
//     collected unless the operator edits them in Edit Device).
const OPTIONS: { label: string; category: string }[] = [
  { label: 'Point of Sale', category: 'pos' },
  { label: 'Biometric Device', category: 'biometric' },
  { label: 'Workstation (endpoint)', category: 'endpoint' },
  { label: 'Server', category: 'server' },
  { label: 'Printer', category: 'printer' },
]

export function ManualClassify({ device }: { device: Device }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState('')
  const m = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.patch<Device>(`/devices/${device.id}`, body),
    onSuccess: () => {
      setOpen(false); setMsg('')
      qc.invalidateQueries({ queryKey: ['group-inventory'] })
      qc.invalidateQueries({ queryKey: ['inventory-bmc'] })
      qc.invalidateQueries({ queryKey: ['device-category-counts'] })
    },
    onError: (e) => setMsg((e as Error).message),
  })
  const mark = (category: string, label: string) =>
    m.mutate({ category, classification_locked: true, manual_classification_reason: `operator marked as ${label}` })
  const clear = () => m.mutate({ classification_locked: false, manual_classification_reason: '' })

  return (
    <div style={{ position: 'relative', display: 'inline-block' }}>
      <button className="btn btn-ghost btn-xs" title="Manually classify (operator override)" onClick={() => setOpen((v) => !v)}>
        <Tag size={12} />
      </button>
      {open && (
        <div style={{ position: 'absolute', right: 0, top: '100%', zIndex: 20, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8, minWidth: 200, boxShadow: '0 6px 20px rgba(0,0,0,.3)', padding: 6 }}>
          <div className="muted" style={{ fontSize: 11, padding: '4px 8px' }}>Mark device as…</div>
          {OPTIONS.map((o) => (
            <button key={o.category} className="btn btn-ghost btn-xs" style={{ display: 'block', width: '100%', textAlign: 'left' }}
              disabled={m.isPending} onClick={() => mark(o.category, o.label)}>{o.label}</button>
          ))}
          {device.classification_locked && (
            <button className="btn btn-ghost btn-xs" style={{ display: 'block', width: '100%', textAlign: 'left', color: 'var(--warn)' }}
              disabled={m.isPending} onClick={clear}>Clear manual classification</button>
          )}
          {msg && <div className="error-msg" style={{ fontSize: 11, margin: '4px 8px 0' }}>{msg}</div>}
        </div>
      )}
    </div>
  )
}
