import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, ShieldCheck, Search, Check, X, Layers } from 'lucide-react'
import { api, type Device, type Credential, type CredentialGroup } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

// Device Access — one place to see every device's management access and give a
// credential to many devices at once.
//
// The assign action TESTS before it binds: the server authenticates each selected
// device and binds only where that succeeded. A binding is HIMS claiming it can
// manage the device, so it is never written on assertion alone — the per-device
// outcome below reports exactly which ones took it and why the rest did not.

interface AssignResult {
  device_id: string
  device_name: string
  ip: string
  protocol: string
  category: string
  detail: string
  success: boolean
  bound: boolean
}
interface AssignResponse {
  results: AssignResult[]
  devices: number
  authenticated: number
  bound: number
  failed: number
}

export function DeviceAccess() {
  const qc = useQueryClient()
  const devicesQ = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const credsQ = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const groupsQ = useQuery({ queryKey: ['credential-groups'], queryFn: () => api.get<CredentialGroup[]>('/credential-groups'), retry: 0 })

  const [filter, setFilter] = useState('')
  const [onlyUnbound, setOnlyUnbound] = useState(false)
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [credId, setCredId] = useState('')
  const [report, setReport] = useState<AssignResponse | null>(null)

  const devices = useMemo(() => devicesQ.data ?? [], [devicesQ.data])
  const creds = credsQ.data ?? []
  const credName = (id?: string | null) => creds.find((c) => c.id === id)?.name ?? null

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return devices
      .filter((d) => (onlyUnbound ? !d.credential_id : true))
      .filter((d) => !q || d.name.toLowerCase().includes(q) || (d.primary_ip ?? '').includes(q) || d.category.toLowerCase().includes(q))
      .sort((a, b) => (a.primary_ip ?? a.name).localeCompare(b.primary_ip ?? b.name, undefined, { numeric: true }))
  }, [devices, filter, onlyUnbound])

  const bound = devices.filter((d) => d.credential_id).length
  const allShownSelected = rows.length > 0 && rows.every((d) => sel.has(d.id))

  const toggle = (id: string) => {
    const n = new Set(sel)
    if (n.has(id)) n.delete(id); else n.add(id)
    setSel(n)
  }
  const toggleAllShown = () => {
    const n = new Set(sel)
    if (allShownSelected) rows.forEach((d) => n.delete(d.id))
    else rows.forEach((d) => n.add(d.id))
    setSel(n)
  }

  const assign = useMutation({
    mutationFn: () => api.post<AssignResponse>('/devices/credential-assign', { credential_id: credId, device_ids: [...sel] }),
    onSuccess: (r) => {
      setReport(r)
      qc.invalidateQueries({ queryKey: ['devices', 'all'] })
    },
  })

  return (
    <div>
      <PageHeader title="Device Access" icon={ShieldCheck}
        subtitle="Management access per device — assign a credential to many devices at once. Bound only where authentication succeeds." />

      <div className="kpi-row">
        <Kpi label="Devices" value={devices.length} icon={Layers} />
        <Kpi label="With a bound credential" value={bound} tone={bound === devices.length ? 'ok' : 'default'} icon={KeyRound} />
        <Kpi label="No credential" value={devices.length - bound} tone={devices.length - bound > 0 ? 'warn' : 'ok'} />
        <Kpi label="Selected" value={sel.size} />
      </div>

      {groupsQ.data && groupsQ.data.length > 0 && (
        <Panel title="Defaults applied before a scan" icon={Layers}
          subtitle="Credential groups the scan already tries automatically — assign here only for devices those did not cover">
          <div className="row" style={{ flexWrap: 'wrap', gap: 6 }}>
            {groupsQ.data.map((g) => <span key={g.id} className="seg-chip">{g.name}</span>)}
          </div>
        </Panel>
      )}

      <Panel title="Assign a credential to the selection" icon={KeyRound}>
        <div className="row" style={{ flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <select className="field" style={{ minWidth: 260 }} value={credId} onChange={(e) => setCredId(e.target.value)}>
            <option value="">Choose a credential…</option>
            {creds.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
          </select>
          <button className="btn btn-primary" disabled={!credId || sel.size === 0 || assign.isPending}
            onClick={() => { setReport(null); assign.mutate() }}>
            {assign.isPending ? `Testing ${sel.size}…` : `Test & assign to ${sel.size} device${sel.size === 1 ? '' : 's'}`}
          </button>
          {sel.size > 0 && <button className="btn btn-sm" onClick={() => setSel(new Set())}>Clear selection</button>}
        </div>
        <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
          Each selected device is authenticated first. The credential is bound only where that succeeded — devices that
          fail are listed with the reason and are left unchanged.
        </p>
        {assign.isError && <div style={{ color: 'var(--crit)', fontSize: 12, marginTop: 6 }}>{(assign.error as Error).message}</div>}
      </Panel>

      {report && (
        <Panel title="Assignment result" icon={Check}
          subtitle={`${report.bound} bound · ${report.authenticated} authenticated · ${report.failed} failed of ${report.devices}`} pad={false}>
          <table className="data-table">
            <thead><tr><th>Device</th><th>IP</th><th>Protocol</th><th>Outcome</th><th>Detail</th></tr></thead>
            <tbody>
              {report.results.map((r) => (
                <tr key={r.device_id}>
                  <td className="cell-name">{r.device_name}</td>
                  <td className="mono">{r.ip || '—'}</td>
                  <td>{r.protocol || '—'}</td>
                  <td>{r.bound
                    ? <span className="badge badge-up">bound</span>
                    : r.success ? <span className="badge badge-warning">authenticated, not saved</span>
                      : <span className="badge badge-down">{r.category || 'failed'}</span>}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{r.detail || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}

      <Panel title="Devices" icon={Layers} subtitle={`${rows.length} shown · ${devices.length} total`} pad={false}
        actions={
          <div className="row" style={{ gap: 8, alignItems: 'center' }}>
            <label className="muted" style={{ fontSize: 12, display: 'flex', gap: 6, alignItems: 'center' }}>
              <input type="checkbox" checked={onlyUnbound} onChange={(e) => setOnlyUnbound(e.target.checked)} /> only without a credential
            </label>
            <span className="row" style={{ gap: 4, alignItems: 'center' }}>
              <Search size={14} />
              <input className="field" style={{ width: 200 }} placeholder="name, IP, category" value={filter} onChange={(e) => setFilter(e.target.value)} />
            </span>
          </div>
        }>
        {devicesQ.isLoading && <div className="loading">Loading…</div>}
        {!devicesQ.isLoading && rows.length === 0 && (
          <EmptyState icon={Layers} title="No devices match" message="Clear the filter, or run a discovery scan to populate inventory." />
        )}
        {rows.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 32 }}><input type="checkbox" checked={allShownSelected} onChange={toggleAllShown} title="Select all shown" /></th>
                <th>IP</th><th>Name</th><th>Category</th><th>Status</th><th>Bound credential</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => (
                <tr key={d.id} style={sel.has(d.id) ? { background: 'var(--surface-2)' } : undefined}>
                  <td><input type="checkbox" checked={sel.has(d.id)} onChange={() => toggle(d.id)} /></td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  <td className="cell-name">{d.name}</td>
                  <td>{d.category}</td>
                  <td>{d.status === 'up' ? <span className="badge badge-up">up</span> : <span className="badge badge-down">{d.status}</span>}</td>
                  <td>{d.credential_id
                    ? <span className="badge badge-up"><Check size={11} /> {credName(d.credential_id) ?? 'bound'}</span>
                    : <span className="muted" style={{ fontSize: 12 }}><X size={11} /> none</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
