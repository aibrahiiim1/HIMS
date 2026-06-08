import { useMemo, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Unplug, Pencil, RefreshCw, Network, Map, Waypoints } from 'lucide-react'
import { api, type Device, type OperationalHealth } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager, colorFor } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { EditDevice } from '../components/EditDevice'

// Unmapped = a topology-capable FABRIC device (switch / router / ISP router)
// that appears in NO LLDP/CDP topology link. These are the devices dragging down
// Topology Coverage on the dashboard — the network map can't place them because
// no neighbour relationship has been collected for them yet. Distinct from
// Unmanaged (no proven access) and Missing Classification (identity gap): a
// device can be fully Managed and still be Unmapped if its neighbours haven't
// been walked. Coverage and this list share one backend predicate, so the count
// always matches the sidebar badge and the dashboard panel.
const FABRIC_LABEL: Record<string, string> = {
  switch: 'Switches',
  router: 'Routers',
  isp_router: 'ISP routers',
}

// Why a fabric device ends up unmapped — guidance mirrors the dashboard topology
// panel's remediation hint.
const REASON =
  'No LLDP/CDP neighbour collected. Bind a credential that can read neighbours (SNMP/SSH) and re-scan, or enable LLDP/CDP on the device.'

export function UnmappedDevices() {
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [cat, setCat] = useState('')
  const [q, setQ] = useState('')

  // Server returns the topology-capable fabric (switch/router/isp_router) absent
  // from every topology link — same predicate as topology coverage + the badge.
  const { data, isLoading } = useQuery({
    queryKey: ['devices', 'unmapped'],
    queryFn: () => api.get<Device[]>('/devices?topology=unmapped'),
  })
  const rescan = useMutation({
    mutationFn: (ip: string) => api.post('/discovery/scan', { mode: 'targets', targets: ip }),
  })
  // Topology coverage from the same backend computation the dashboard panel uses,
  // so "Fabric coverage" / "On the map" here always match the dashboard.
  const oph = useQuery({
    queryKey: ['operational-health'],
    queryFn: () => api.get<OperationalHealth>('/dashboard/operational-health'),
    retry: 0,
  })
  const topo = oph.data?.topology

  const counts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const d of data ?? []) c[d.category] = (c[d.category] ?? 0) + 1
    return c
  }, [data])

  const rows = useMemo(() => {
    let r = data ?? []
    if (cat) r = r.filter((d) => d.category === cat)
    const t = q.trim().toLowerCase()
    if (t) r = r.filter((d) => d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t))
    return r
  }, [data, cat, q])
  const paged = usePaged(rows, { pageSize: 15 })

  const total = (data ?? []).length

  return (
    <div>
      <PageHeader
        title="Unmapped Devices"
        subtitle="Switches & routers that appear in no LLDP/CDP topology link — the fabric the network map can't yet place. These are what hold back Topology Coverage."
        icon={Unplug}
      />
      <div className="kpi-grid">
        <Kpi label="Unmapped fabric" value={total} icon={Unplug} tone={total > 0 ? 'warn' : 'ok'} sub="no topology link" />
        <Kpi label="On the map" value={topo?.mapped_devices ?? '—'} icon={Map} tone="ok" sub="switches/routers in a link" />
        <Kpi
          label="Fabric coverage"
          value={topo?.coverage_percent != null ? `${topo.coverage_percent}%` : '—'}
          icon={Waypoints}
          tone={topo?.status === 'healthy' ? 'ok' : topo?.status === 'critical' ? 'crit' : topo?.status === 'warning' ? 'warn' : 'default'}
          sub="mapped of all fabric"
        />
        <Kpi label="Switches unmapped" value={counts['switch'] ?? 0} icon={Network} tone="default" sub={`+ ${(counts['router'] ?? 0) + (counts['isp_router'] ?? 0)} routers`} />
      </div>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', margin: '0 0 12px' }}>
        <button className={'seg-chip' + (cat === '' ? ' active' : '')} onClick={() => { setCat(''); paged.setPage(0) }}>All</button>
        {Object.keys(FABRIC_LABEL).map((c) => (
          <button key={c} className={'seg-chip' + (cat === c ? ' active' : '')} onClick={() => { setCat(c); paged.setPage(0) }}>
            {FABRIC_LABEL[c]} <span className="seg-count">{counts[c] ?? 0}</span>
          </button>
        ))}
        <Link className="seg-chip" to="/topology" style={{ marginLeft: 'auto', textDecoration: 'none' }}><Map size={13} style={{ verticalAlign: -2 }} /> Network Map</Link>
      </div>

      <Panel
        title="Unmapped fabric"
        subtitle="Coverage is measured over switches/routers only. Map these by collecting their neighbours; classification/access problems live under their own pages."
        pad={false}
      >
        <div style={{ padding: '8px 10px' }}>
          <input
            placeholder="Filter by name / IP…"
            value={q}
            onChange={(e) => { setQ(e.target.value); paged.setPage(0) }}
            style={{ padding: '6px 10px', fontSize: 13, width: 300, maxWidth: '100%' }}
          />
        </div>
        {isLoading && <div className="loading">Loading…</div>}
        {data && rows.length === 0 && (
          <EmptyState
            icon={Unplug}
            title="Every switch & router is mapped"
            message="All topology-capable devices appear in at least one LLDP/CDP link — Topology Coverage is at 100%."
          />
        )}
        {rows.length > 0 && (
          <>
            <table className="data-table">
              <thead><tr>
                <th>Device</th><th>IP</th><th>Type</th><th>Vendor</th><th>Reachability</th><th>Management</th><th>Why unmapped</th><th></th>
              </tr></thead>
              <tbody>
                {paged.slice.map((d) => (
                  <tr key={d.id}>
                    <td><div className="dev-cell"><span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || '?').charAt(0).toUpperCase()}</span>
                      <div className="dev-meta"><Link className="cell-name" to={`/devices/${d.id}`}>{d.name}</Link>{d.hostname && <small>{d.hostname}</small>}</div></div></td>
                    <td className="mono">{d.primary_ip ?? '—'}</td>
                    <td style={{ textTransform: 'capitalize' }}>{(d.category || 'unknown').replace(/_/g, ' ')}</td>
                    <td>{d.vendor || '—'}</td>
                    <td><ReachabilityBadge value={d.reachability} /></td>
                    <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                    <td className="muted" style={{ fontSize: 11 }}>{REASON}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`} title="Open device — bind credential / test / collect neighbours">Open</Link>{' '}
                      <button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit device"><Pencil size={12} /></button>{' '}
                      <button className="btn btn-ghost btn-xs" disabled={!d.primary_ip || rescan.isPending} onClick={() => d.primary_ip && rescan.mutate(d.primary_ip)} title="Re-scan this device"><RefreshCw size={12} /></button>
                    </td>
                  </tr>
                ))}
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
