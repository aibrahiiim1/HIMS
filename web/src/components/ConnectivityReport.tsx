import { Fragment, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Plug, ShieldCheck, ShieldOff, KeyRound, Boxes, Download, ChevronRight, ChevronDown } from 'lucide-react'
import { api, type Device, type Credential } from '../api'
import { Panel, Kpi, EmptyState, usePaged, Pager, colorFor } from './ui'
import { ManagementBadge } from './StatusBadges'
import { exportToCsv } from '../lib/exportCsv'

// One latest credential outcome per (device, credential, kind) — from
// GET /reports/credential-attempts.
interface ConnAttempt {
  device_id: string
  credential_id?: string
  credential_name: string
  kind: string
  protocol: string
  category: string
  success: boolean
  detail: string
  tested_at: string
}

const PROTO_LABEL: Record<string, string> = {
  snmp_v2c: 'SNMP v2c', snmp_v3: 'SNMP v3', ssh: 'SSH', winrm: 'WinRM', wmi: 'WMI / CIM', smb: 'SMB',
  http_basic: 'HTTP', http: 'HTTP', onvif: 'ONVIF', isapi: 'ISAPI', vmware: 'VMware', redfish: 'Redfish',
  api_token: 'API token', vendor_api: 'Vendor API', rtsp: 'RTSP', cucm_axl: 'CUCM AXL', ldap: 'LDAP', cli: 'CLI',
}
const proto = (p: string) => PROTO_LABEL[p] ?? (p || '').replace(/_/g, ' ')
const isManaged = (d: Device) => d.management === 'managed' || d.management === 'partially_managed'

// ConnectivityReport — per-device "how is it connected" + "what creds were tried
// and failed". Managed devices show the working protocol(s) + credential; every
// non-managed (or any) device lists the credentials that were tried and failed.
export function ConnectivityReport() {
  const devicesQ = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const credsQ = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const attemptsQ = useQuery({ queryKey: ['conn-attempts'], queryFn: () => api.get<ConnAttempt[]>('/reports/credential-attempts') })

  const [filter, setFilter] = useState<'all' | 'managed' | 'unmanaged'>('all')
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (id: string) => setOpen((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })

  const devices = devicesQ.data ?? []
  const credName = useMemo(() => new Map((credsQ.data ?? []).map((c) => [c.id, c.name])), [credsQ.data])
  const byDevice = useMemo(() => {
    const m = new Map<string, ConnAttempt[]>()
    for (const a of attemptsQ.data ?? []) { const l = m.get(a.device_id) ?? []; l.push(a); m.set(a.device_id, l) }
    return m
  }, [attemptsQ.data])

  const working = (d: Device) => (byDevice.get(d.id) ?? []).filter((a) => a.success)
  const failed = (d: Device) => (byDevice.get(d.id) ?? []).filter((a) => !a.success)
  const boundName = (d: Device): string => {
    const id = d.credential_id || d.cctv_credential_id
    return id ? (credName.get(id) ?? '(bound)') : ''
  }
  const viaProtocols = (d: Device): string[] => {
    const wk = [...new Set(working(d).map((a) => a.protocol || a.kind))]
    return wk.length ? wk : (d.managed_by ?? [])
  }

  const rows = useMemo(
    () => devices.filter((d) => filter === 'all' || (filter === 'managed' ? isManaged(d) : !isManaged(d)))
      .sort((a, b) => Number(isManaged(a)) - Number(isManaged(b)) || a.name.localeCompare(b.name)),
    [devices, filter],
  )
  const paged = usePaged(rows, { pageSize: 20 })

  const managedCount = devices.filter(isManaged).length
  const failedDevices = devices.filter((d) => failed(d).length > 0).length

  const exportCsv = () => {
    const headers = ['Device', 'IP', 'Category', 'Management', 'Reachability', 'Connected via', 'Working credential', 'Bound credential', 'Failed credentials (tried)']
    const data = rows.map((d) => {
      const via = viaProtocols(d).map(proto).join(' / ')
      const workingCred = [...new Set(working(d).map((a) => `${a.credential_name} (${proto(a.kind)})`))].join('; ')
      const failedStr = failed(d).map((a) => `${a.credential_name} (${proto(a.kind)}): ${a.category}${a.detail ? ` — ${a.detail}` : ''}`).join(' | ')
      return [d.name, d.primary_ip ?? '', d.category, d.management ?? '', d.reachability ?? '', via, workingCred, boundName(d), failedStr]
    })
    exportToCsv('connectivity-report', headers, data)
  }

  const loading = devicesQ.isLoading || attemptsQ.isLoading
  const err = devicesQ.error || attemptsQ.error

  return (
    <div>
      <div className="kpi-grid">
        <Kpi label="Devices" value={devices.length} icon={Boxes} tone="info" />
        <Kpi label="Managed (proven access)" value={managedCount} icon={ShieldCheck} tone="ok" sub={devices.length ? `${Math.round((managedCount / devices.length) * 100)}%` : '—'} />
        <Kpi label="Not managed" value={devices.length - managedCount} icon={ShieldOff} tone={devices.length - managedCount > 0 ? 'warn' : 'default'} />
        <Kpi label="With failed creds" value={failedDevices} icon={KeyRound} tone={failedDevices > 0 ? 'crit' : 'default'} sub="creds tried & rejected" />
      </div>

      <Panel title="Connectivity & Credentials" icon={Plug} subtitle={`${rows.length} shown — how each device is reached, and which credentials failed`} pad={false}
        actions={<button className="btn btn-sm" disabled={rows.length === 0} onClick={exportCsv}><Download size={14} /> Export CSV</button>}>
        <div className="seg" style={{ padding: '10px 12px' }}>
          <button className={'seg-chip' + (filter === 'all' ? ' active' : '')} onClick={() => { setFilter('all'); paged.setPage(0) }}>All <span className="seg-count">{devices.length}</span></button>
          <button className={'seg-chip' + (filter === 'managed' ? ' active' : '')} onClick={() => { setFilter('managed'); paged.setPage(0) }}>Managed <span className="seg-count">{managedCount}</span></button>
          <button className={'seg-chip' + (filter === 'unmanaged' ? ' active' : '')} onClick={() => { setFilter('unmanaged'); paged.setPage(0) }}>Not managed <span className="seg-count">{devices.length - managedCount}</span></button>
        </div>

        {loading && <div className="loading">Loading connectivity…</div>}
        {err && <div style={{ padding: 'var(--space-5)' }}><div className="error-msg">Failed to load: {(err as Error).message}</div></div>}
        {!loading && rows.length === 0 && <EmptyState icon={Plug} title="No devices match this filter" />}

        {rows.length > 0 && (
          <table className="data-table">
            <thead>
              <tr><th style={{ width: 24 }}></th><th>Device</th><th>IP</th><th>Category</th><th>Management</th><th>Connected via</th><th>Credential</th><th>Tried &amp; failed</th></tr>
            </thead>
            <tbody>
              {paged.slice.map((d) => {
                const wk = working(d), fl = failed(d)
                const via = viaProtocols(d)
                const isOpen = open.has(d.id)
                const expandable = fl.length > 0 || wk.length > 0
                return (
                  <Fragment key={d.id}>
                    <tr style={expandable ? { cursor: 'pointer' } : undefined} onClick={() => expandable && toggle(d.id)}>
                      <td>{expandable ? (isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : null}</td>
                      <td>
                        <div className="dev-cell">
                          <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || '?').charAt(0).toUpperCase()}</span>
                          <div className="dev-meta"><Link className="cell-name" to={`/devices/${d.id}`} onClick={(e) => e.stopPropagation()}>{d.name}</Link>{d.hostname && <small>{d.hostname}</small>}</div>
                        </div>
                      </td>
                      <td className="mono">{d.primary_ip ?? '—'}</td>
                      <td style={{ textTransform: 'capitalize' }}>{(d.category || 'unknown').replace(/_/g, ' ')}</td>
                      <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                      <td>{via.length ? via.map((p) => <span key={p} className="badge badge-up" style={{ marginRight: 4 }}>{proto(p)}</span>) : <span className="muted">—</span>}</td>
                      <td style={{ fontSize: 12 }}>{boundName(d) || <span className="muted">—</span>}</td>
                      <td>{fl.length > 0 ? <span className="badge badge-warning">{fl.length} failed</span> : <span className="muted">—</span>}</td>
                    </tr>
                    {isOpen && expandable && (
                      <tr>
                        <td></td>
                        <td colSpan={7} style={{ background: 'var(--surface-2)', padding: '8px 12px' }}>
                          {wk.length > 0 && (
                            <div style={{ marginBottom: fl.length ? 8 : 0 }}>
                              <span className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5 }}>Working access</span>
                              {wk.map((a, i) => (
                                <div key={i} style={{ fontSize: 12, marginTop: 2 }}>
                                  <span className="badge badge-up">{proto(a.protocol || a.kind)}</span> via <strong>{a.credential_name}</strong> <span className="muted">({a.kind})</span>
                                </div>
                              ))}
                            </div>
                          )}
                          {fl.length > 0 && (
                            <div>
                              <span className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5 }}>Credentials tried &amp; failed</span>
                              {fl.map((a, i) => (
                                <div key={i} style={{ fontSize: 12, marginTop: 2 }}>
                                  <span className="badge badge-down">{a.category}</span> <strong>{a.credential_name}</strong> <span className="muted">({proto(a.kind)})</span>
                                  {a.detail && <span className="muted"> — {a.detail}</span>}
                                </div>
                              ))}
                            </div>
                          )}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        )}
        {rows.length > 0 && <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />}
      </Panel>
    </div>
  )
}
