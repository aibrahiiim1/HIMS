import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LayoutTemplate, Plus } from 'lucide-react'
import { api, type DeviceTemplate } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

const blank = { name: '', vendor: '', device_type: '', discovery_rules: '{}', monitoring_rules: '{}', classification_rules: '{}', enabled: true }
const pretty = (v: unknown) => JSON.stringify(v ?? {}, null, 0)

export function DeviceTemplates() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['device-templates'], queryFn: () => api.get<DeviceTemplate[]>('/device-templates') })
  const inv = () => qc.invalidateQueries({ queryKey: ['device-templates'] })
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState(blank)
  const [msg, setMsg] = useState('')

  const parseRules = () => {
    try {
      return {
        name: form.name, vendor: form.vendor, device_type: form.device_type, enabled: form.enabled,
        discovery_rules: JSON.parse(form.discovery_rules || '{}'),
        monitoring_rules: JSON.parse(form.monitoring_rules || '{}'),
        classification_rules: JSON.parse(form.classification_rules || '{}'),
      }
    } catch (e) { setMsg('Rules must be valid JSON: ' + (e as Error).message); return null }
  }
  const save = useMutation({
    mutationFn: (body: object) => editId ? api.patch(`/device-templates/${editId}`, body) : api.post('/device-templates', body),
    onSuccess: () => { setForm(blank); setEditId(null); setMsg('Saved.'); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/device-templates/${id}`), onSuccess: inv })

  const rows = q.data ?? []
  const startEdit = (t: DeviceTemplate) => {
    setEditId(t.id)
    setForm({ name: t.name, vendor: t.vendor, device_type: t.device_type, enabled: t.enabled, discovery_rules: pretty(t.discovery_rules), monitoring_rules: pretty(t.monitoring_rules), classification_rules: pretty(t.classification_rules) })
  }

  return (
    <div>
      <PageHeader title="Device Templates" icon={LayoutTemplate} subtitle="Reusable profiles: vendor + device type with discovery, monitoring and classification rules" />
      <div className="kpi-grid">
        <Kpi label="Templates" value={rows.length} icon={LayoutTemplate} tone="info" />
        <Kpi label="Enabled" value={rows.filter((t) => t.enabled).length} tone="ok" />
        <Kpi label="Vendors" value={new Set(rows.map((t) => t.vendor).filter(Boolean)).size} tone="default" />
      </div>

      <Panel title={editId ? 'Edit Template' : 'New Template'} icon={Plus}>
        <div className="row" style={{ marginBottom: 10 }}>
          <input className="field" placeholder="name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <input className="field" placeholder="vendor" value={form.vendor} onChange={(e) => setForm({ ...form, vendor: e.target.value })} />
          <input className="field" placeholder="device type" value={form.device_type} onChange={(e) => setForm({ ...form, device_type: e.target.value })} />
          <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} /> enabled</label>
        </div>
        <div className="grid-3">
          {(['discovery_rules', 'monitoring_rules', 'classification_rules'] as const).map((k) => (
            <label key={k} className="form-field">{k.replace('_', ' ')}
              <textarea className="field" rows={5} style={{ fontFamily: 'monospace', fontSize: 12 }} value={form[k]} onChange={(e) => setForm({ ...form, [k]: e.target.value })} />
            </label>
          ))}
        </div>
        <div className="row" style={{ marginTop: 12 }}>
          <button className="btn btn-primary" disabled={!form.name || save.isPending} onClick={() => { const b = parseRules(); if (b) save.mutate(b) }}>{editId ? 'Update' : 'Create'}</button>
          {editId && <button className="btn btn-ghost" onClick={() => { setEditId(null); setForm(blank) }}>Cancel</button>}
          {msg && <span className="muted" style={{ fontSize: 12 }}>{msg}</span>}
        </div>
      </Panel>

      <Panel title="Templates" icon={LayoutTemplate} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={LayoutTemplate} title="No templates yet" message="Create a template to standardize discovery, monitoring and classification per vendor/device-type." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Vendor</th><th>Device type</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id}>
                  <td className="cell-name">{t.name}</td>
                  <td>{t.vendor || '—'}</td>
                  <td>{t.device_type || '—'}</td>
                  <td>{t.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" onClick={() => startEdit(t)}>Edit</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(t.id)}>Delete</button>
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
