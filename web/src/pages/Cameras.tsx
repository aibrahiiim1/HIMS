import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Video, Wifi, ShieldCheck, TriangleAlert, Server, Pencil, Radar } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, Kpi, StatusPill, EmptyState, usePaged, Pager } from '../components/ui'
import { ManagementBadge } from '../components/StatusBadges'
import { DeleteAllToggle } from '../components/DeleteAllToggle'
import { EditDevice } from '../components/EditDevice'
import { AddVirtualButton } from '../components/AddVirtualButton'
import { AddDeviceButton } from '../components/AddDeviceButton'
import { ExportDevicesButton } from '../components/ExportDevicesButton'

const isOnline = (d: Device) => (d.status || '').toLowerCase() === 'up' || (d.reachability || '') === 'online'
const isManaged = (d: Device) => d.management === 'managed'
// How a managed camera is reached: a direct web/API method, or through its NVR's
// channel list (RTSP-only cameras recorded by a managed recorder).
function reachVia(d: Device): 'direct' | 'nvr' | null {
  if (!isManaged(d)) return null
  const m = d.managed_by ?? []
  if (m.some((x) => ['isapi', 'onvif', 'http', 'vendor_api', 'http_basic'].includes(x))) return 'direct'
  if (m.includes('nvr')) return 'nvr'
  return 'direct'
}

type StatusF = 'all' | 'online' | 'offline'
type MgmtF = 'all' | 'managed' | 'direct' | 'nvr' | 'attention'

export function Cameras() {
  const qc = useQueryClient()
  const [msg, setMsg] = useState('')
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [q, setQ] = useState('')
  const [statusF, setStatusF] = useState<StatusF>('all')
  const [mgmtF, setMgmtF] = useState<MgmtF>('all')
  const [vendorF, setVendorF] = useState('all')

  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', 'camera'],
    queryFn: () => api.get<Device[]>('/devices?category=camera'),
  })
  const del = useMutation({
    mutationFn: (ids: string[]) => api.post<{ deleted: number }>('/devices/bulk-delete', { ids }),
    onSuccess: (r) => { setMsg(`Deleted ${(r as { deleted: number }).deleted} cameras.`); qc.invalidateQueries({ queryKey: ['devices'] }) },
    onError: (e) => setMsg((e as Error).message),
  })

  const all = data ?? []
  const online = all.filter(isOnline).length
  const managed = all.filter(isManaged).length
  const direct = all.filter((d) => reachVia(d) === 'direct').length
  const viaNvr = all.filter((d) => reachVia(d) === 'nvr').length
  const attention = all.filter((d) => d.management && d.management !== 'managed').length

  // Vendor + model rollups for the breakdown + the vendor filter.
  const vendorCounts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const d of all) { const v = d.vendor || 'Unknown'; m[v] = (m[v] ?? 0) + 1 }
    return Object.entries(m).sort((a, b) => b[1] - a[1])
  }, [data])
  const modelCounts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const d of all) { if (!d.model) continue; m[d.model] = (m[d.model] ?? 0) + 1 }
    return Object.entries(m).sort((a, b) => b[1] - a[1]).slice(0, 6)
  }, [data])

  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    return all.filter((d) => {
      if (t && !(d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
        (d.vendor ?? '').toLowerCase().includes(t) || (d.model ?? '').toLowerCase().includes(t))) return false
      if (statusF === 'online' && !isOnline(d)) return false
      if (statusF === 'offline' && isOnline(d)) return false
      if (mgmtF === 'managed' && !isManaged(d)) return false
      if (mgmtF === 'direct' && reachVia(d) !== 'direct') return false
      if (mgmtF === 'nvr' && reachVia(d) !== 'nvr') return false
      if (mgmtF === 'attention' && !(d.management && d.management !== 'managed')) return false
      if (vendorF !== 'all' && (d.vendor || 'Unknown') !== vendorF) return false
      return true
    })
  }, [data, q, statusF, mgmtF, vendorF])
  const paged = usePaged(filtered, { pageSize: 25 })
  const resetPage = () => paged.setPage(0)

  const total = all.length || 1
  const seg = (n: number) => `${(n / total) * 100}%`

  return (
    <div>
      <PageHeader title="Cameras" subtitle="Surveillance cameras across the fleet — status, management & credentials" icon={Video}
        actions={
          <>
            <ExportDevicesButton devices={filtered} filename="cameras" />
            <AddDeviceButton defaultType="camera" label="Add Camera" />
            <AddVirtualButton type="camera" label="Camera" />
            <DeleteAllToggle ids={filtered.map((d) => d.id)} fullInventory={false}
              scope={(q.trim() || statusF !== 'all' || mgmtF !== 'all' || vendorF !== 'all') ? 'filtered cameras' : 'all cameras'}
              onDelete={(ids) => del.mutate(ids)} busy={del.isPending} />
          </>
        }
      />
      {msg && <div className="banner" style={{ marginBottom: 12, fontSize: 13 }}>{msg}</div>}

      <div className="kpi-grid kpi-5">
        <Kpi label="Cameras" value={all.length} icon={Video} tone="info" sub={`${vendorCounts.length} vendor${vendorCounts.length !== 1 ? 's' : ''}`} />
        <Kpi label="Online" value={online} icon={Wifi} tone="ok" sub={all.length ? `${Math.round((online / all.length) * 100)}%` : '—'} />
        <Kpi label="Managed" value={managed} icon={ShieldCheck} tone={managed === all.length ? 'ok' : 'info'}
          sub={`${direct} direct · ${viaNvr} via NVR`} />
        <Kpi label="Needs attention" value={attention} icon={TriangleAlert} tone={attention > 0 ? 'warn' : 'default'}
          sub={attention > 0 ? 'credential / access' : 'all clear'}
          onClick={attention > 0 ? () => { setMgmtF('attention'); resetPage() } : undefined} />
        <Kpi label="Vendors" value={vendorCounts.length} icon={Server} tone="default" sub="distinct" />
      </div>

      {/* Coverage + breakdown */}
      {all.length > 0 && (
        <Panel title="Coverage" subtitle="how cameras are managed" icon={ShieldCheck}>
          <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr 1fr', gap: 'var(--space-4)', alignItems: 'start' }}>
            <div>
              <div style={{ display: 'flex', height: 14, borderRadius: 7, overflow: 'hidden', background: 'var(--border)' }}>
                {direct > 0 && <div title={`Direct: ${direct}`} style={{ width: seg(direct), background: 'var(--ok)' }} />}
                {viaNvr > 0 && <div title={`Via NVR: ${viaNvr}`} style={{ width: seg(viaNvr), background: '#3b82f6' }} />}
                {attention > 0 && <div title={`Needs attention: ${attention}`} style={{ width: seg(attention), background: 'var(--warn)' }} />}
              </div>
              <div className="stat-strip" style={{ marginTop: 12 }}>
                <div className="s-item"><b style={{ color: 'var(--ok)' }}>{direct}</b><small>direct (ISAPI/ONVIF)</small></div>
                <div className="s-item"><b style={{ color: '#3b82f6' }}>{viaNvr}</b><small>via NVR channel</small></div>
                <div className="s-item" style={{ cursor: attention > 0 ? 'pointer' : undefined }} onClick={attention > 0 ? () => { setMgmtF('attention'); resetPage() } : undefined}>
                  <b style={{ color: attention > 0 ? 'var(--warn)' : undefined }}>{attention}{attention > 0 ? ' ›' : ''}</b><small>needs attention</small>
                </div>
              </div>
            </div>
            <div>
              <div className="muted" style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>Top vendors</div>
              {vendorCounts.slice(0, 5).map(([v, n]) => (
                <div key={v} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, padding: '2px 0' }}>
                  <span>{v}</span><b style={{ fontVariantNumeric: 'tabular-nums' }}>{n}</b>
                </div>
              ))}
            </div>
            <div>
              <div className="muted" style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>Top models</div>
              {modelCounts.length === 0 ? <span className="muted" style={{ fontSize: 12 }}>—</span> : modelCounts.map(([m, n]) => (
                <div key={m} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, padding: '2px 0', gap: 8 }}>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m}</span><b style={{ fontVariantNumeric: 'tabular-nums' }}>{n}</b>
                </div>
              ))}
            </div>
          </div>
        </Panel>
      )}

      <Panel title="Camera list" subtitle={`${filtered.length} of ${all.length}`} pad={false}>
        {isLoading && <div className="loading">Loading cameras…</div>}
        {error && <div style={{ padding: 'var(--space-5)' }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && data.length === 0 && (
          <EmptyState icon={Radar} title="No cameras yet" message="Run a discovery scan to populate cameras, or add one manually."
            action={<Link className="btn btn-primary btn-sm" to="/discovery">Start Discovery</Link>} />
        )}
        {data && data.length > 0 && (
          <>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', padding: '10px 12px' }}>
              <input placeholder="Search name / IP / vendor / model…" value={q} onChange={(e) => { setQ(e.target.value); resetPage() }}
                style={{ padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, width: 280, maxWidth: '100%' }} />
              <select value={statusF} onChange={(e) => { setStatusF(e.target.value as StatusF); resetPage() }} style={{ padding: '6px 8px', fontSize: 13 }}>
                <option value="all">All status</option><option value="online">Online</option><option value="offline">Offline / attention</option>
              </select>
              <select value={mgmtF} onChange={(e) => { setMgmtF(e.target.value as MgmtF); resetPage() }} style={{ padding: '6px 8px', fontSize: 13 }}>
                <option value="all">All management</option>
                <option value="managed">Managed</option>
                <option value="direct">Direct (ISAPI/ONVIF)</option>
                <option value="nvr">Via NVR channel</option>
                <option value="attention">Needs attention</option>
              </select>
              <select value={vendorF} onChange={(e) => { setVendorF(e.target.value); resetPage() }} style={{ padding: '6px 8px', fontSize: 13 }}>
                <option value="all">All vendors</option>
                {vendorCounts.map(([v, n]) => <option key={v} value={v}>{v} ({n})</option>)}
              </select>
              {(q || statusF !== 'all' || mgmtF !== 'all' || vendorF !== 'all') && (
                <button className="btn btn-ghost btn-sm" onClick={() => { setQ(''); setStatusF('all'); setMgmtF('all'); setVendorF('all'); resetPage() }}>Clear</button>
              )}
            </div>
            <table className="data-table">
              <thead>
                <tr><th>Camera</th><th>IP</th><th>Vendor · Model</th><th>Management</th><th>Status</th><th></th></tr>
              </thead>
              <tbody>
                {paged.slice.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <div className="dev-cell">
                        <span className="dev-avatar" style={{ background: '#0ea5b7' }}><Video size={13} /></span>
                        <div className="dev-meta">
                          <Link className="cell-name" to={`/cctv/${d.id}`}>{d.name}</Link>
                          {d.hostname && <small>{d.hostname}</small>}
                        </div>
                      </div>
                    </td>
                    <td className="mono">{d.primary_ip ?? '—'}</td>
                    <td>{d.vendor || '—'}{d.model ? <> · <span className="muted">{d.model}</span></> : null}</td>
                    <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                    <td><StatusPill status={d.status} /></td>
                    <td><button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit camera"><Pencil size={12} /></button></td>
                  </tr>
                ))}
                {paged.slice.length === 0 && (
                  <tr><td colSpan={6} style={{ padding: 'var(--space-5)', textAlign: 'center' }} className="muted">No cameras match the current filters.</td></tr>
                )}
              </tbody>
            </table>
            <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
          </>
        )}
      </Panel>
      {editDev && <EditDevice device={editDev} onClose={() => setEditDev(null)} />}
    </div>
  )
}
