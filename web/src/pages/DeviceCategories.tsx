import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Tags, Plus, Trash2, Eye, EyeOff, Save, X, Lock } from 'lucide-react'
import { api, type DeviceCategory } from '../api'
import { Panel, StatusPill, EmptyState } from '../components/ui'

// DeviceCategories is the Settings → Device Categories management page. Built-in
// categories (produced by auto-classification, routed by the detail pages, used by
// existing devices) can be relabelled and HIDDEN but never deleted. Operators add
// custom categories for MANUAL classification — fully editable and deletable; deleting
// one reassigns its devices to "unknown" so none are orphaned.
export function DeviceCategories() {
  const qc = useQueryClient()
  const cats = useQuery({ queryKey: ['device-categories'], queryFn: () => api.get<DeviceCategory[]>('/device-categories') })
  const [msg, setMsg] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [nv, setNv] = useState({ value: '', label: '', icon: '' })
  const [editRow, setEditRow] = useState<string | null>(null)
  const [edit, setEdit] = useState({ label: '', icon: '' })

  const rows = cats.data ?? []
  const refresh = () => { qc.invalidateQueries({ queryKey: ['device-categories'] }) }
  const flash = (m: string) => { setMsg(m); setTimeout(() => setMsg(null), 3500) }

  async function create() {
    const value = nv.value.trim().toLowerCase()
    if (!/^[a-z][a-z0-9_]{1,39}$/.test(value)) { flash('✗ value must be a slug (lowercase letter, then letters/digits/underscore)'); return }
    try {
      await api.post('/device-categories', { value, label: nv.label.trim() || value, icon: nv.icon.trim() })
      setNv({ value: '', label: '', icon: '' }); setAdding(false); refresh(); flash('✓ Added custom category ' + value)
    } catch (e) { flash('✗ ' + (e as Error).message) }
  }
  async function saveEdit(value: string) {
    try {
      await api.patch(`/device-categories/${value}`, { label: edit.label.trim(), icon: edit.icon.trim() })
      setEditRow(null); refresh(); flash('✓ Updated ' + value)
    } catch (e) { flash('✗ ' + (e as Error).message) }
  }
  async function toggle(c: DeviceCategory) {
    try { await api.patch(`/device-categories/${c.value}`, { enabled: !c.enabled }); refresh() }
    catch (e) { flash('✗ ' + (e as Error).message) }
  }
  async function remove(c: DeviceCategory) {
    if (!confirm(`Delete custom category "${c.label}"?` + (c.device_count > 0 ? `\n${c.device_count} device(s) will be reassigned to "unknown".` : ''))) return
    try { const r = await api.del(`/device-categories/${c.value}`); refresh(); flash(`✓ Deleted ${c.value}` + (c.device_count ? ` (${c.device_count} reassigned)` : '')); void r }
    catch (e) { flash('✗ ' + (e as Error).message) }
  }

  const btn = { fontSize: 12, padding: '4px 10px', border: '1px solid var(--border)', borderRadius: 6, background: 'var(--surface)', color: 'inherit', cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 4 } as const
  const inp = { fontSize: 13, padding: '5px 8px', border: '1px solid var(--border)', borderRadius: 6, background: 'var(--surface)', color: 'inherit' } as const
  const customCount = rows.filter((c) => !c.builtin).length

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12, flexWrap: 'wrap', gap: 8 }}>
        <div>
          <h1 style={{ margin: 0, display: 'flex', alignItems: 'center', gap: 8 }}><Tags size={20} /> Device Categories</h1>
          <p className="muted" style={{ margin: '4px 0 0', fontSize: 13 }}>Manage the category list used when you Edit a device. Built-in categories can be relabelled or hidden; add your own custom categories for manual classification. {rows.length} total · {customCount} custom.</p>
        </div>
        <button style={{ ...btn, fontSize: 13, padding: '6px 12px' }} onClick={() => setAdding((v) => !v)}><Plus size={14} /> Add custom category</button>
      </div>

      {msg && <div style={{ marginBottom: 10, fontSize: 13 }} className={msg.startsWith('✓') ? '' : 'muted'}>{msg}</div>}

      {adding && (
        <Panel title="New custom category" icon={Plus}>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>value (slug)
              <input style={inp} placeholder="kiosk" value={nv.value} onChange={(e) => setNv({ ...nv, value: e.target.value })} /></label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>label
              <input style={inp} placeholder="Kiosk" value={nv.label} onChange={(e) => setNv({ ...nv, label: e.target.value })} /></label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>icon (optional, lucide name)
              <input style={inp} placeholder="Monitor" value={nv.icon} onChange={(e) => setNv({ ...nv, icon: e.target.value })} /></label>
            <button style={{ ...btn, borderColor: 'var(--brand)' }} onClick={create}><Save size={14} /> Create</button>
            <button style={btn} onClick={() => setAdding(false)}><X size={14} /> Cancel</button>
          </div>
          <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Custom categories are for manual classification only — selectable in Edit Device, shown with the generic detail page, and not auto-detected by scans.</p>
        </Panel>
      )}

      <Panel title="Categories" icon={Tags} subtitle={`${rows.length}`} pad={false}>
        {cats.isLoading ? <div style={{ padding: 14 }} className="muted">Loading…</div> :
          rows.length === 0 ? <EmptyState icon={Tags} title="No categories" message="Unexpected — the built-in catalog should be seeded." /> : (
            <table className="data-table">
              <thead><tr><th>Label</th><th>Value</th><th>Type</th><th>Devices</th><th>Visible</th><th style={{ textAlign: 'right' }}>Actions</th></tr></thead>
              <tbody>{rows.map((c) => (
                <tr key={c.value} style={{ opacity: c.enabled ? 1 : 0.55 }}>
                  <td className="cell-name">
                    {editRow === c.value
                      ? <input style={{ ...inp, width: 160 }} value={edit.label} onChange={(e) => setEdit({ ...edit, label: e.target.value })} />
                      : c.label}
                  </td>
                  <td className="mono" style={{ fontSize: 12 }}>{c.value}</td>
                  <td>{c.builtin ? <span className="badge badge-unknown" title="Built-in — cannot be deleted"><Lock size={11} /> built-in</span> : <span className="badge badge-access">custom</span>}</td>
                  <td className="mono">{c.device_count || '—'}</td>
                  <td><StatusPill status={c.enabled ? 'up' : 'unknown'} label={c.enabled ? 'shown' : 'hidden'} /></td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    {editRow === c.value ? (
                      <>
                        <button style={btn} onClick={() => saveEdit(c.value)}><Save size={13} /> Save</button>{' '}
                        <button style={btn} onClick={() => setEditRow(null)}><X size={13} /></button>
                      </>
                    ) : (
                      <>
                        <button style={btn} title="Edit label" onClick={() => { setEditRow(c.value); setEdit({ label: c.label, icon: c.icon }) }}>Edit</button>{' '}
                        <button style={btn} title={c.enabled ? 'Hide from the Edit picker' : 'Show in the Edit picker'} onClick={() => toggle(c)}>{c.enabled ? <EyeOff size={13} /> : <Eye size={13} />}</button>{' '}
                        {c.builtin
                          ? <button style={{ ...btn, opacity: 0.4, cursor: 'not-allowed' }} title="Built-in categories can't be deleted — hide it instead" disabled><Trash2 size={13} /></button>
                          : <button style={{ ...btn, borderColor: 'var(--crit)' }} title="Delete custom category" onClick={() => remove(c)}><Trash2 size={13} /></button>}
                      </>
                    )}
                  </td>
                </tr>
              ))}</tbody>
            </table>
          )}
      </Panel>
    </div>
  )
}
