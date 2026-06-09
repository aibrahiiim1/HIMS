import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Ghost, Plus, Trash2, Download, Upload, Save } from 'lucide-react'
import { api, saveBlob, locationPaths, type Location, type Device, type VirtualDeviceReq, type VirtualPort, type VirtualVlan, type VirtualNeighbor, type VirtualMac } from '../api'
import { PageHeader, Panel } from '../components/ui'

// Categories mirror the backend devices.category CHECK + manual-add list.
const CATEGORIES = [
  'switch', 'router', 'firewall', 'access_point', 'wireless_controller', 'server',
  'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer', 'ip_phone',
  'pbx', 'voice_gateway', 'database', 'directory', 'dns', 'dhcp', 'endpoint', 'ups',
  'isp_router', 'application', 'unknown',
]
const STATUSES = ['up', 'down', 'warning', 'unknown']
const ROLES = ['access', 'trunk', 'uplink', 'unknown']

const blankPort = (ifIndex: number): VirtualPort => ({ if_index: ifIndex, name: '', up: true, admin_down: false, vlan: 0, role: 'access' })

export function VirtualDeviceForm() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = locationPaths(locs.data ?? [])

  const [d, setD] = useState<VirtualDeviceReq>({ name: '', category: 'switch', status: 'up' })
  const [ports, setPorts] = useState<VirtualPort[]>([])
  const [vlans, setVlans] = useState<VirtualVlan[]>([])
  const [neighbors, setNeighbors] = useState<VirtualNeighbor[]>([])
  const [macs, setMacs] = useState<VirtualMac[]>([])
  const [msg, setMsg] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const set = (k: keyof VirtualDeviceReq, v: string) => setD((p) => ({ ...p, [k]: v }))

  const save = useMutation({
    mutationFn: () => api.post<Device>('/devices/virtual', {
      ...d,
      location_id: d.location_id || undefined,
      ports, vlans, neighbors, macs,
    }),
    onSuccess: (dev) => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      nav(`/devices/${dev.id}`)
    },
    onError: (e) => setErr((e as Error).message),
  })

  const importXlsx = useMutation({
    mutationFn: async (file: File) => {
      const fd = new FormData()
      fd.append('file', file)
      return api.postForm<{ device: Device; counts: Record<string, number> }>('/devices/virtual/import', fd)
    },
    onSuccess: (res) => { qc.invalidateQueries({ queryKey: ['devices'] }); nav(`/devices/${res.device.id}`) },
    onError: (e) => setErr((e as Error).message),
  })

  const downloadTemplate = async () => {
    try {
      const blob = await api.getBlob('/devices/virtual/template.xlsx')
      saveBlob(blob, 'virtual-device-template.xlsx')
    } catch (e) { setErr((e as Error).message) }
  }

  const addPorts = (n: number) => {
    const start = ports.reduce((m, p) => Math.max(m, p.if_index), 0) + 1
    setPorts((p) => [...p, ...Array.from({ length: n }, (_, i) => blankPort(start + i))])
  }
  const submit = () => {
    setErr(null); setMsg(null)
    if (!d.name.trim()) { setErr('Name is required'); return }
    save.mutate()
  }

  return (
    <div>
      <PageHeader title="Add Virtual Device" icon={Ghost}
        subtitle="A manually-entered placeholder for gear HIMS can't integrate with — it counts in inventory and renders everywhere, marked Virtual."
        actions={
          <>
            <button className="btn btn-ghost btn-sm" onClick={downloadTemplate}><Download size={14} /> Excel template</button>
            <label className="btn btn-ghost btn-sm" style={{ cursor: 'pointer' }}>
              <Upload size={14} /> Import Excel
              <input type="file" accept=".xlsx" style={{ display: 'none' }}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) importXlsx.mutate(f) }} />
            </label>
          </>
        }
      />

      {err && <div className="enc-banner crit" style={{ marginBottom: 12 }}>{err}</div>}
      {msg && <div className="enc-banner" style={{ marginBottom: 12 }}>{msg}</div>}
      {importXlsx.isPending && <div className="loading">Importing…</div>}

      <Panel title="Identity" subtitle="What this device is">
        <div className="form-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: 12 }}>
          <Field label="Name *"><input className="field" value={d.name} onChange={(e) => set('name', e.target.value)} placeholder="Core-SW-04" /></Field>
          <Field label="Category">
            <select className="field" value={d.category} onChange={(e) => set('category', e.target.value)}>
              {CATEGORIES.map((c) => <option key={c} value={c}>{c.replace(/_/g, ' ')}</option>)}
            </select>
          </Field>
          <Field label="Status">
            <select className="field" value={d.status} onChange={(e) => set('status', e.target.value)}>
              {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </Field>
          <Field label="Management IP"><input className="field" value={d.primary_ip ?? ''} onChange={(e) => set('primary_ip', e.target.value)} placeholder="172.21.96.9 (optional)" /></Field>
          <Field label="Vendor"><input className="field" value={d.vendor ?? ''} onChange={(e) => set('vendor', e.target.value)} /></Field>
          <Field label="Model"><input className="field" value={d.model ?? ''} onChange={(e) => set('model', e.target.value)} /></Field>
          <Field label="Serial"><input className="field" value={d.serial ?? ''} onChange={(e) => set('serial', e.target.value)} /></Field>
          <Field label="OS / Firmware"><input className="field" value={d.os_version ?? ''} onChange={(e) => set('os_version', e.target.value)} /></Field>
          <Field label="VLAN (mgmt)"><input className="field" value={d.vlan ?? ''} onChange={(e) => set('vlan', e.target.value)} /></Field>
          <Field label="Class"><input className="field" value={d.class ?? ''} onChange={(e) => set('class', e.target.value)} placeholder="core_switch / access_switch / …" /></Field>
          <Field label="Location">
            <select className="field" value={d.location_id ?? ''} onChange={(e) => set('location_id', e.target.value)}>
              <option value="">— none —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id] ?? l.name}</option>)}
            </select>
          </Field>
        </div>
      </Panel>

      <EditorPanel title="Ports / Interfaces" count={ports.length}
        head={<button className="btn btn-ghost btn-xs" onClick={() => setPorts((p) => [...p, blankPort((p.reduce((m, x) => Math.max(m, x.if_index), 0)) + 1)])}><Plus size={12} /> Add port</button>}
        extra={<><button className="btn btn-ghost btn-xs" onClick={() => addPorts(24)}>+24</button><button className="btn btn-ghost btn-xs" onClick={() => addPorts(48)}>+48</button></>}>
        {ports.length > 0 && (
          <table className="data-table"><thead><tr><th>#</th><th>Name</th><th>Alias</th><th>State</th><th>Admin</th><th>Speed</th><th>VLAN</th><th>Role</th><th>MAC</th><th /></tr></thead>
            <tbody>{ports.map((p, i) => (
              <tr key={i}>
                <td style={{ width: 56 }}><input className="field" type="number" value={p.if_index} onChange={(e) => upd(setPorts, i, { if_index: +e.target.value })} /></td>
                <td><input className="field" value={p.name ?? ''} onChange={(e) => upd(setPorts, i, { name: e.target.value })} placeholder="Gi1/0/1" /></td>
                <td><input className="field" value={p.alias ?? ''} onChange={(e) => upd(setPorts, i, { alias: e.target.value })} /></td>
                <td><select className="field" value={p.up ? 'up' : 'down'} onChange={(e) => upd(setPorts, i, { up: e.target.value === 'up' })}><option value="up">up</option><option value="down">down</option></select></td>
                <td><label style={{ fontSize: 12 }}><input type="checkbox" checked={!!p.admin_down} onChange={(e) => upd(setPorts, i, { admin_down: e.target.checked })} /> shut</label></td>
                <td style={{ width: 80 }}><input className="field" type="number" value={p.speed_mbps ?? 0} onChange={(e) => upd(setPorts, i, { speed_mbps: +e.target.value })} /></td>
                <td style={{ width: 70 }}><input className="field" type="number" value={p.vlan ?? 0} onChange={(e) => upd(setPorts, i, { vlan: +e.target.value })} /></td>
                <td><select className="field" value={p.role ?? 'access'} onChange={(e) => upd(setPorts, i, { role: e.target.value })}>{ROLES.map((r) => <option key={r} value={r}>{r}</option>)}</select></td>
                <td><input className="field mono" value={p.mac ?? ''} onChange={(e) => upd(setPorts, i, { mac: e.target.value })} /></td>
                <td><button className="btn btn-ghost btn-xs" onClick={() => rm(setPorts, i)}><Trash2 size={12} /></button></td>
              </tr>))}</tbody>
          </table>
        )}
      </EditorPanel>

      <EditorPanel title="VLANs" count={vlans.length} head={<button className="btn btn-ghost btn-xs" onClick={() => setVlans((v) => [...v, { id: 0, name: '' }])}><Plus size={12} /> Add VLAN</button>}>
        {vlans.length > 0 && (
          <table className="data-table"><thead><tr><th>VLAN ID</th><th>Name</th><th /></tr></thead>
            <tbody>{vlans.map((v, i) => (
              <tr key={i}>
                <td style={{ width: 100 }}><input className="field" type="number" value={v.id} onChange={(e) => upd(setVlans, i, { id: +e.target.value })} /></td>
                <td><input className="field" value={v.name ?? ''} onChange={(e) => upd(setVlans, i, { name: e.target.value })} /></td>
                <td><button className="btn btn-ghost btn-xs" onClick={() => rm(setVlans, i)}><Trash2 size={12} /></button></td>
              </tr>))}</tbody>
          </table>
        )}
      </EditorPanel>

      <EditorPanel title="Neighbors (LLDP/CDP)" count={neighbors.length} head={<button className="btn btn-ghost btn-xs" onClick={() => setNeighbors((n) => [...n, { protocol: 'manual' }])}><Plus size={12} /> Add neighbor</button>}>
        {neighbors.length > 0 && (
          <table className="data-table"><thead><tr><th>Local port</th><th>Remote device</th><th>Remote port</th><th>Remote mgmt IP</th><th>Protocol</th><th /></tr></thead>
            <tbody>{neighbors.map((n, i) => (
              <tr key={i}>
                <td><input className="field" value={n.local_port ?? ''} onChange={(e) => upd(setNeighbors, i, { local_port: e.target.value })} /></td>
                <td><input className="field" value={n.remote_name ?? ''} onChange={(e) => upd(setNeighbors, i, { remote_name: e.target.value })} /></td>
                <td><input className="field" value={n.remote_port ?? ''} onChange={(e) => upd(setNeighbors, i, { remote_port: e.target.value })} /></td>
                <td><input className="field mono" value={n.remote_mgmt_ip ?? ''} onChange={(e) => upd(setNeighbors, i, { remote_mgmt_ip: e.target.value })} /></td>
                <td><select className="field" value={n.protocol ?? 'manual'} onChange={(e) => upd(setNeighbors, i, { protocol: e.target.value })}><option>manual</option><option>lldp</option><option>cdp</option></select></td>
                <td><button className="btn btn-ghost btn-xs" onClick={() => rm(setNeighbors, i)}><Trash2 size={12} /></button></td>
              </tr>))}</tbody>
          </table>
        )}
      </EditorPanel>

      <EditorPanel title="Learned MACs (FDB)" count={macs.length} head={<button className="btn btn-ghost btn-xs" onClick={() => setMacs((m) => [...m, { mac: '', vlan: 0, if_index: 0 }])}><Plus size={12} /> Add MAC</button>}>
        {macs.length > 0 && (
          <table className="data-table"><thead><tr><th>MAC</th><th>VLAN</th><th>Port (ifIndex)</th><th /></tr></thead>
            <tbody>{macs.map((m, i) => (
              <tr key={i}>
                <td><input className="field mono" value={m.mac} onChange={(e) => upd(setMacs, i, { mac: e.target.value })} placeholder="aa:bb:cc:dd:ee:ff" /></td>
                <td style={{ width: 80 }}><input className="field" type="number" value={m.vlan ?? 0} onChange={(e) => upd(setMacs, i, { vlan: +e.target.value })} /></td>
                <td style={{ width: 110 }}><input className="field" type="number" value={m.if_index ?? 0} onChange={(e) => upd(setMacs, i, { if_index: +e.target.value })} /></td>
                <td><button className="btn btn-ghost btn-xs" onClick={() => rm(setMacs, i)}><Trash2 size={12} /></button></td>
              </tr>))}</tbody>
          </table>
        )}
      </EditorPanel>

      <div className="row" style={{ gap: 10, marginTop: 16, justifyContent: 'flex-end' }}>
        <button className="btn btn-ghost" onClick={() => nav('/inventory')}>Cancel</button>
        <button className="btn btn-primary" disabled={save.isPending} onClick={submit}><Save size={15} /> {save.isPending ? 'Creating…' : 'Create virtual device'}</button>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13 }}><span className="muted">{label}</span>{children}</label>
}

function EditorPanel({ title, count, head, extra, children }: { title: string; count: number; head: React.ReactNode; extra?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Panel title={title} subtitle={count ? `${count}` : undefined} actions={<div className="row" style={{ gap: 6 }}>{extra}{head}</div>} pad={count === 0}>
      {count === 0 ? <div className="muted" style={{ fontSize: 13 }}>None added yet.</div> : children}
    </Panel>
  )
}

// upd/rm: immutable row helpers for the editors.
function upd<T>(setter: React.Dispatch<React.SetStateAction<T[]>>, i: number, patch: Partial<T>) {
  setter((arr) => arr.map((x, j) => (j === i ? { ...x, ...patch } : x)))
}
function rm<T>(setter: React.Dispatch<React.SetStateAction<T[]>>, i: number) {
  setter((arr) => arr.filter((_, j) => j !== i))
}
