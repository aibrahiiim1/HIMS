import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Device } from '../api'

const DETAIL_BASE: Record<string, string> = {
  switch: '/devices', server: '/servers', virtual_host: '/virtual-hosts', firewall: '/firewalls',
  camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx',
}
const CATEGORIES = [
  'unknown', 'switch', 'router', 'firewall', 'access_point', 'wireless_controller', 'server',
  'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer', 'ip_phone', 'pbx',
  'voice_gateway', 'database', 'directory', 'dns', 'dhcp', 'fingerprint', 'endpoint', 'ups',
  'isp_router', 'application',
]

const input: React.CSSProperties = { padding: '6px 8px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13 }
const btn: React.CSSProperties = { padding: '6px 12px', background: '#1565c0', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 13, fontWeight: 600 }
const danger: React.CSSProperties = { ...btn, background: '#c62828' }
const ghost: React.CSSProperties = { padding: '3px 8px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }

export function Inventory() {
  const qc = useQueryClient()
  const [cat, setCat] = useState('all')
  const [classF, setClassF] = useState('all')
  const [locF, setLocF] = useState('all')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [editing, setEditing] = useState<Device | null>(null)
  const [msg, setMsg] = useState('')
  // bulk-assign inputs (applied to the current selection)
  const [asg, setAsg] = useState({ vlan: '', class: '', location: '' })

  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', 'all'],
    queryFn: () => api.get<Device[]>('/devices?category=all'),
  })

  const counts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const d of data ?? []) m[d.category] = (m[d.category] ?? 0) + 1
    return m
  }, [data])
  const cats = useMemo(() => Object.keys(counts).sort(), [counts])
  const classes = useMemo(() => [...new Set((data ?? []).map((d) => d.device_class).filter(Boolean) as string[])].sort(), [data])
  const locations = useMemo(() => [...new Set((data ?? []).map((d) => d.location).filter(Boolean) as string[])].sort(), [data])

  const rows = useMemo(() => {
    let r = data ?? []
    if (cat !== 'all') r = r.filter((d) => d.category === cat)
    if (classF !== 'all') r = r.filter((d) => (d.device_class ?? '') === classF)
    if (locF !== 'all') r = r.filter((d) => (d.location ?? '') === locF)
    if (q.trim()) {
      const t = q.toLowerCase()
      r = r.filter((d) =>
        d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
        (d.vendor ?? '').toLowerCase().includes(t) || (d.vlan ?? '').toLowerCase().includes(t) ||
        (d.device_class ?? '').toLowerCase().includes(t) || (d.location ?? '').toLowerCase().includes(t))
    }
    return r
  }, [data, cat, classF, locF, q])

  const refresh = () => qc.invalidateQueries({ queryKey: ['devices'] })
  const toggle = (id: string) => setSel((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n })
  const allShownSelected = rows.length > 0 && rows.every((d) => sel.has(d.id))
  const toggleAll = () => setSel((s) => {
    const n = new Set(s)
    if (allShownSelected) rows.forEach((d) => n.delete(d.id))
    else rows.forEach((d) => n.add(d.id))
    return n
  })
  const selRows = useMemo(() => (data ?? []).filter((d) => sel.has(d.id)), [data, sel])

  const del = useMutation({
    mutationFn: (ids: string[]) => api.post<{ deleted: number }>('/devices/bulk-delete', { ids }),
    onSuccess: (r) => { setMsg(`Deleted ${(r as { deleted: number }).deleted} device(s).`); setSel(new Set()); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })

  const rescan = useMutation({
    mutationFn: (targets: string) => api.post('/discovery/scan', { mode: 'targets', targets }),
    onSuccess: () => setMsg('Re-scan launched for selected targets — watch Discovery → Scan jobs.'),
    onError: (e) => setMsg((e as Error).message),
  })

  const save = useMutation({
    mutationFn: (d: Device) => api.patch(`/devices/${d.id}`, {
      name: d.name, category: d.category, vendor: d.vendor ?? '', model: d.model ?? '',
      serial: d.serial ?? '', os_version: d.os_version ?? '', hostname: d.hostname ?? '',
    }),
    onSuccess: () => { setEditing(null); setMsg('Saved.'); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })

  const assign = useMutation({
    mutationFn: (body: { ids: string[]; vlan?: string; class?: string; location?: string }) => api.post<{ updated: number }>('/devices/bulk-assign', body),
    onSuccess: (r) => { setMsg(`Updated ${(r as { updated: number }).updated} device(s).`); setAsg({ vlan: '', class: '', location: '' }); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const doAssign = () => {
    if (sel.size === 0) return
    const body: { ids: string[]; vlan?: string; class?: string; location?: string } = { ids: [...sel] }
    if (asg.vlan.trim()) body.vlan = asg.vlan.trim()
    if (asg.class.trim()) body.class = asg.class.trim()
    if (asg.location.trim()) body.location = asg.location.trim()
    if (body.vlan === undefined && body.class === undefined && body.location === undefined) { setMsg('Enter a VLAN, Class, or Location to assign.'); return }
    assign.mutate(body)
  }

  const doDeleteSelected = () => {
    if (sel.size === 0) return
    if (confirm(`Delete ${sel.size} device(s)? This also removes their collected inventory and cannot be undone.`)) del.mutate([...sel])
  }
  const doRescanSelected = () => {
    const ips = selRows.map((d) => d.primary_ip).filter(Boolean) as string[]
    if (ips.length === 0) { setMsg('None of the selected devices have an IP to re-scan.'); return }
    rescan.mutate(ips.join(','))
  }

  return (
    <div>
      <div className="card">
        <h2>Inventory <span className="muted" style={{ fontSize: 13, fontWeight: 400 }}>— every device, all categories</span></h2>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
          <select style={{ ...input, width: 170 }} value={cat} onChange={(e) => setCat(e.target.value)}>
            <option value="all">All categories ({(data ?? []).length})</option>
            {cats.map((c) => <option key={c} value={c}>{c} ({counts[c]})</option>)}
          </select>
          <select style={{ ...input, width: 150 }} value={classF} onChange={(e) => setClassF(e.target.value)}>
            <option value="all">All classes</option>
            {classes.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
          <select style={{ ...input, width: 170 }} value={locF} onChange={(e) => setLocF(e.target.value)}>
            <option value="all">All locations</option>
            {locations.map((l) => <option key={l} value={l}>{l}</option>)}
          </select>
          <input style={{ ...input, width: 200 }} placeholder="search name / IP / vlan / class / loc" value={q} onChange={(e) => setQ(e.target.value)} />
          <div style={{ flex: 1 }} />
          <button style={btn} disabled={sel.size === 0 || rescan.isPending} onClick={doRescanSelected}>Re-scan selected ({sel.size})</button>
          <button style={danger} disabled={sel.size === 0 || del.isPending} onClick={doDeleteSelected}>Delete selected ({sel.size})</button>
        </div>

        {/* Bulk-assign bar — appears when rows are selected */}
        {sel.size > 0 && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', padding: '8px 0', borderTop: '1px solid #2a2a2a' }}>
            <span className="muted" style={{ fontSize: 12 }}>Assign to {sel.size} selected:</span>
            <input style={{ ...input, width: 90 }} placeholder="vlan" value={asg.vlan} onChange={(e) => setAsg({ ...asg, vlan: e.target.value })} />
            <input style={{ ...input, width: 130 }} placeholder="class" value={asg.class} onChange={(e) => setAsg({ ...asg, class: e.target.value })} />
            <input style={{ ...input, width: 160 }} placeholder="location" value={asg.location} onChange={(e) => setAsg({ ...asg, location: e.target.value })} />
            <button style={btn} disabled={assign.isPending} onClick={doAssign}>Assign</button>
            <span className="muted" style={{ fontSize: 11 }}>(blank fields are left unchanged)</span>
          </div>
        )}
        {msg && <div className="muted" style={{ fontSize: 12 }}>{msg}</div>}
      </div>

      <div className="card">
        {isLoading && <div className="loading">Loading inventory…</div>}
        {error && <div className="error-msg">Failed to load: {(error as Error).message}</div>}
        {data && rows.length === 0 && <div className="muted">No devices match.</div>}
        {rows.length > 0 && (
          <table>
            <thead>
              <tr>
                <th style={{ width: 28 }}><input type="checkbox" checked={allShownSelected} onChange={toggleAll} /></th>
                <th>Name</th><th>IP</th><th>Category</th><th>VLAN</th><th>Class</th><th>Location</th><th>Vendor</th><th>Status</th><th></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => {
                const base = DETAIL_BASE[d.category]
                const isEd = editing?.id === d.id
                if (isEd && editing) {
                  return (
                    <tr key={d.id} style={{ background: '#1a2733' }}>
                      <td></td>
                      <td><input style={{ ...input, width: 130 }} value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} /></td>
                      <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{d.primary_ip ?? '—'}</td>
                      <td>
                        <select style={{ ...input, width: 130 }} value={editing.category} onChange={(e) => setEditing({ ...editing, category: e.target.value })}>
                          {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
                        </select>
                      </td>
                      <td><input style={{ ...input, width: 70 }} value={editing.vlan ?? ''} onChange={(e) => setEditing({ ...editing, vlan: e.target.value })} /></td>
                      <td><input style={{ ...input, width: 90 }} value={editing.device_class ?? ''} onChange={(e) => setEditing({ ...editing, device_class: e.target.value })} /></td>
                      <td><input style={{ ...input, width: 110 }} value={editing.location ?? ''} onChange={(e) => setEditing({ ...editing, location: e.target.value })} /></td>
                      <td><input style={{ ...input, width: 90 }} value={editing.vendor ?? ''} onChange={(e) => setEditing({ ...editing, vendor: e.target.value })} /></td>
                      <td>{d.status}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>
                        <button style={btn} disabled={save.isPending} onClick={() => save.mutate(editing)}>Save</button>{' '}
                        <button style={ghost} onClick={() => setEditing(null)}>Cancel</button>
                      </td>
                    </tr>
                  )
                }
                return (
                  <tr key={d.id}>
                    <td><input type="checkbox" checked={sel.has(d.id)} onChange={() => toggle(d.id)} /></td>
                    <td>{base ? <Link to={`${base}/${d.id}`}>{d.name}</Link> : d.name}</td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{d.primary_ip ?? '—'}</td>
                    <td>{d.category}</td>
                    <td>{d.vlan ?? '—'}</td>
                    <td>{d.device_class ?? '—'}</td>
                    <td>{d.location ?? '—'}</td>
                    <td>{d.vendor ?? '—'}</td>
                    <td><span className={`badge badge-${d.status}`}>{d.status}</span></td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button style={ghost} onClick={() => setEditing(d)}>Edit</button>{' '}
                      <button style={{ ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }} onClick={() => { if (confirm(`Delete ${d.name}?`)) del.mutate([d.id]) }}>Delete</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
