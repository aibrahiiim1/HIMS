import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { X, Lock, Save } from 'lucide-react'
import { api, locationPaths, type Device, type Location } from '../api'

// Categories the backend accepts (kept in step with internal/api validCategory).
const CATEGORIES = [
  'unknown', 'switch', 'router', 'firewall', 'access_point', 'wireless_controller',
  'server', 'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer',
  'ip_phone', 'pbx', 'voice_gateway', 'database', 'directory', 'dns', 'dhcp',
  'endpoint', 'ups', 'isp_router', 'application',
]
const CRITICALITY = ['', 'low', 'normal', 'high', 'critical']

// EditDevice is the single shared device-edit modal used by every device list,
// the device-detail header, scan results, search and Data Quality lists. It does
// a PARTIAL PATCH (only changed fields), so it never clobbers values it didn't
// show. Locking classification makes the operator's identity authoritative —
// future scans won't overwrite category/vendor/model/serial/name.
export function EditDevice({ device, onClose, onSaved }: {
  device: Device
  onClose: () => void
  onSaved?: (d: Device) => void
}) {
  const qc = useQueryClient()
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPaths = locationPaths(locs.data ?? [])

  const [f, setF] = useState({
    name: device.name ?? '',
    hostname: device.hostname ?? '',
    category: device.category ?? 'unknown',
    subtype: device.subtype ?? '',
    vendor: device.vendor ?? '',
    model: device.model ?? '',
    serial: device.serial ?? '',
    os_version: device.os_version ?? '',
    vlan: device.vlan ?? '',
    class: device.device_class ?? '',
    location_id: device.location_id ?? '',
    notes: device.notes ?? '',
    criticality: device.criticality ?? '',
    monitoring_enabled: device.monitoring_enabled ?? true,
    classification_locked: device.classification_locked ?? false,
    manual_classification_reason: device.manual_classification_reason ?? '',
  })
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF((s) => ({ ...s, [k]: v }))
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function save() {
    if (!f.name.trim()) { setErr('Name is required.'); return }
    setSaving(true); setErr(null)
    try {
      const body = {
        name: f.name.trim(), hostname: f.hostname, category: f.category, subtype: f.subtype,
        vendor: f.vendor, model: f.model, serial: f.serial, os_version: f.os_version,
        vlan: f.vlan, class: f.class, location_id: f.location_id || '',
        notes: f.notes, criticality: f.criticality,
        monitoring_enabled: f.monitoring_enabled, classification_locked: f.classification_locked,
        manual_classification_reason: f.classification_locked ? f.manual_classification_reason : '',
      }
      const updated = await api.patch<Device>(`/devices/${device.id}`, body)
      // Refresh every device-backed view so the edit shows immediately.
      qc.invalidateQueries({ queryKey: ['devices'] })
      qc.invalidateQueries({ queryKey: ['device', device.id] })
      onSaved?.(updated)
      onClose()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const field = (label: string, node: React.ReactNode) => (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>
      <span className="muted">{label}</span>{node}
    </label>
  )
  const input = (k: keyof typeof f) => (
    <input value={f[k] as string} onChange={(e) => set(k, e.target.value as never)} style={{ padding: '6px 8px', fontSize: 13 }} />
  )

  return (
    <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', justifyContent: 'flex-end', zIndex: 1000 }} onClick={onClose}>
      <div className="modal-panel" style={{ width: 460, maxWidth: '100%', height: '100%', background: 'var(--surface)', overflowY: 'auto', boxShadow: '-4px 0 24px rgba(0,0,0,.3)' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 16px', borderBottom: '1px solid var(--border)', position: 'sticky', top: 0, background: 'var(--surface)' }}>
          <strong>Edit device — {device.primary_ip ?? device.name}</strong>
          <button className="btn btn-ghost btn-sm" onClick={onClose}><X size={16} /></button>
        </div>
        <div style={{ padding: 16, display: 'grid', gap: 12 }}>
          {device.classification_locked && (
            <div className="badge badge-warning" style={{ alignSelf: 'start' }}><Lock size={11} style={{ verticalAlign: -1 }} /> manual override active</div>
          )}
          {field('Display name', input('name'))}
          {field('Hostname', input('hostname'))}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {field('Category', (
              <select value={f.category} onChange={(e) => set('category', e.target.value)} style={{ padding: '6px 8px', fontSize: 13 }}>
                {CATEGORIES.map((c) => <option key={c} value={c}>{c.replace(/_/g, ' ')}</option>)}
              </select>
            ))}
            {field('Subtype', input('subtype'))}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {field('Vendor', input('vendor'))}
            {field('Model', input('model'))}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {field('Serial', input('serial'))}
            {field('OS version', input('os_version'))}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {field('VLAN', input('vlan'))}
            {field('Class', input('class'))}
          </div>
          {field('Location', (
            <select value={f.location_id} onChange={(e) => set('location_id', e.target.value)} style={{ padding: '6px 8px', fontSize: 13 }}>
              <option value="">— none —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPaths[l.id] ?? l.name}</option>)}
            </select>
          ))}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {field('Criticality', (
              <select value={f.criticality} onChange={(e) => set('criticality', e.target.value)} style={{ padding: '6px 8px', fontSize: 13 }}>
                {CRITICALITY.map((c) => <option key={c} value={c}>{c || '—'}</option>)}
              </select>
            ))}
            {field('Monitoring', (
              <label style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 13, paddingTop: 6 }}>
                <input type="checkbox" checked={f.monitoring_enabled} onChange={(e) => set('monitoring_enabled', e.target.checked)} /> enabled
              </label>
            ))}
          </div>
          {field('Notes', <textarea value={f.notes} onChange={(e) => set('notes', e.target.value)} rows={2} style={{ padding: '6px 8px', fontSize: 13 }} />)}

          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 12, display: 'grid', gap: 8 }}>
            <label style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
              <input type="checkbox" checked={f.classification_locked} onChange={(e) => set('classification_locked', e.target.checked)} />
              <Lock size={13} /> Lock classification (future scans won&apos;t overwrite category/vendor/model/serial/name)
            </label>
            {f.classification_locked && field('Manual classification reason', input('manual_classification_reason'))}
          </div>

          {err && <div className="error-msg" style={{ fontSize: 12 }}>{err}</div>}
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <button className="btn btn-ghost btn-sm" onClick={onClose} disabled={saving}>Cancel</button>
            <button className="btn btn-primary btn-sm" onClick={save} disabled={saving}><Save size={14} /> {saving ? 'Saving…' : 'Save'}</button>
          </div>
        </div>
      </div>
    </div>
  )
}
