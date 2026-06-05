import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { Search as SearchIcon, Boxes, Building2, ClipboardList, Wrench, Network, Route as RouteIcon, Clock, X } from 'lucide-react'
import { api, type Device, type WorkOrder, type SystemLicense, type Location, type SearchResult, locationPaths } from '../api'
import { PageHeader, Panel, StatusPill, EmptyState, colorFor } from '../components/ui'

const RECENT_KEY = 'hims-recent-search'
function loadRecent(): string[] {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]') } catch { return [] }
}

const detailBase: Record<string, string> = { switch: '/devices', server: '/servers', firewall: '/firewalls', camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx', virtual_host: '/virtual-hosts' }
const looksNetworky = (s: string) => /^[0-9a-f]{2}([:-][0-9a-f]{2}){5}$/i.test(s) || /^\d{1,3}(\.\d{1,3}){3}$/.test(s)

export function SearchPage() {
  const [params, setParams] = useSearchParams()
  const initial = params.get('q') ?? ''
  const [term, setTerm] = useState(initial)
  const [q, setQ] = useState(initial)
  const [recent, setRecent] = useState<string[]>(loadRecent)

  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const workOrders = useQuery({ queryKey: ['work-orders'], queryFn: () => api.get<WorkOrder[]>('/work-orders') })
  const systems = useQuery({ queryKey: ['systems'], queryFn: () => api.get<SystemLicense[]>('/systems') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  // Network-location resolve (IP/MAC → switch+port+path) only when the query looks like one.
  const net = useQuery({
    queryKey: ['search-net', q],
    queryFn: () => api.get<SearchResult | SearchResult[]>(`/search?q=${encodeURIComponent(q)}`),
    enabled: q.length > 0 && looksNetworky(q),
    retry: 0,
  })

  const t = q.trim().toLowerCase()
  const devHits = (devices.data ?? []).filter((d) =>
    t && (d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) || (d.vendor ?? '').toLowerCase().includes(t)
      || (d.serial ?? '').toLowerCase().includes(t) || (d.hostname ?? '').toLowerCase().includes(t) || d.category.includes(t)
      || (d.vlan ?? '').toLowerCase().includes(t) || (d.device_class ?? '').toLowerCase().includes(t))).slice(0, 25)
  const locHits = (locs.data ?? []).filter((l) => t && (locPath[l.id] ?? l.name).toLowerCase().includes(t)).slice(0, 15)
  const woHits = (workOrders.data ?? []).filter((w) => t && (w.title.toLowerCase().includes(t) || (w.assigned_to ?? '').toLowerCase().includes(t) || w.problem_type.includes(t))).slice(0, 15)
  const sysHits = (systems.data ?? []).filter((s) => t && (s.name.toLowerCase().includes(t) || (s.vendor ?? '').toLowerCase().includes(t))).slice(0, 15)
  const netList: SearchResult[] = net.data == null ? [] : Array.isArray(net.data) ? net.data : [net.data]
  const totalHits = devHits.length + locHits.length + woHits.length + sysHits.length

  const pushRecent = (v: string) => {
    const next = [v, ...recent.filter((x) => x !== v)].slice(0, 8)
    setRecent(next)
    localStorage.setItem(RECENT_KEY, JSON.stringify(next))
  }
  const run = (override?: string) => {
    const v = (override ?? term).trim()
    setTerm(v)
    setQ(v)
    setParams(v ? { q: v } : {})
    if (v) pushRecent(v)
  }
  const clearRecent = () => { setRecent([]); localStorage.removeItem(RECENT_KEY) }

  return (
    <div>
      <PageHeader title="Global Search" icon={SearchIcon} subtitle="Devices, IPs, MACs, serials, vendors, locations, work orders and systems" />
      <div className="card">
        <div className="search-box" style={{ marginBottom: 0 }}>
          <input autoFocus value={term} onChange={(e) => setTerm(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()}
            placeholder="Search anything — 172.21.15.44 · aa:bb:cc:dd:ee:ff · SW-CORE-01 · Aruba · VLAN 95 · Hotel A" />
          <button onClick={() => run()}>Search</button>
        </div>
      </div>

      {!t && (
        <>
          {recent.length > 0 && (
            <Panel title="Recent searches" icon={Clock} actions={<button className="btn btn-ghost btn-xs" onClick={clearRecent}><X size={13} /> Clear</button>}>
              <div className="row" style={{ flexWrap: 'wrap', gap: 8 }}>
                {recent.map((r) => (
                  <button key={r} className="chip" onClick={() => run(r)} style={{ cursor: 'pointer' }}>{r}</button>
                ))}
              </div>
            </Panel>
          )}
          <EmptyState icon={SearchIcon} title="Search the whole platform" message="Find devices by name/IP/serial/vendor/VLAN, locations, work orders, systems, and resolve a MAC/IP to its switch port." />
        </>
      )}

      {t && (
        <>
          {looksNetworky(q) && (
            <Panel title="Network Location" icon={Network} subtitle="switch · port · path"
              actions={<Link className="btn btn-primary btn-xs" to={`/path-finder?q=${encodeURIComponent(q)}`}><RouteIcon size={13} /> Open in Path Finder</Link>}>
              {net.isLoading && <div className="loading">Resolving…</div>}
              {netList.length === 0 && !net.isLoading && <div className="muted">No switch port found for this IP/MAC in the collected forwarding tables.</div>}
              {netList.map((res, i) => (
                <div key={i} style={{ marginBottom: 12 }}>
                  {res.device_name && <div className="muted" style={{ marginBottom: 6 }}>Matched: <strong>{res.device_name}</strong>{res.mac && <span className="mono"> · {res.mac}</span>}</div>}
                  {res.switch_port.length > 0 && (
                    <table className="data-table"><thead><tr><th>Switch</th><th>IP</th><th>Port</th><th>VLAN</th><th>Role</th></tr></thead>
                      <tbody>{res.switch_port.map((sp, j) => (
                        <tr key={j}><td className="cell-name">{sp.switch_name}</td><td className="mono">{sp.switch_ip ?? '—'}</td><td>{sp.if_name ?? sp.if_index}</td><td>{sp.vlan_id}</td><td>{sp.port_role ? <span className={`badge badge-${sp.port_role}`}>{sp.port_role}</span> : '—'}</td></tr>
                      ))}</tbody>
                    </table>
                  )}
                </div>
              ))}
            </Panel>
          )}

          {totalHits === 0 && !looksNetworky(q) && <EmptyState icon={SearchIcon} title="No matches" message={`Nothing matched "${q}".`} />}

          {devHits.length > 0 && (
            <Panel title="Devices" icon={Boxes} subtitle={`${devHits.length}`} pad={false}>
              <table className="data-table"><thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Vendor</th><th>Status</th></tr></thead>
                <tbody>{devHits.map((d) => {
                  const base = detailBase[d.category] ?? '/devices' // unmapped (unknown/endpoint) → dispatcher
                  return <tr key={d.id}>
                    <td><div className="dev-cell"><span className="dev-avatar" style={{ background: colorFor(d.category) }}>{d.category.charAt(0).toUpperCase()}</span>
                      <div className="dev-meta">{base ? <Link className="cell-name" to={`${base}/${d.id}`}>{d.name}</Link> : <span className="cell-name">{d.name}</span>}{d.serial && <small>SN {d.serial}</small>}</div></div></td>
                    <td className="mono">{d.primary_ip ?? '—'}</td><td>{d.category.replace(/_/g, ' ')}</td><td>{d.vendor ?? '—'}</td><td><StatusPill status={d.status} /></td>
                  </tr>
                })}</tbody>
              </table>
            </Panel>
          )}

          {locHits.length > 0 && (
            <Panel title="Locations" icon={Building2} subtitle={`${locHits.length}`} pad={false}>
              <table className="data-table"><thead><tr><th>Location</th><th>Kind</th></tr></thead>
                <tbody>{locHits.map((l) => <tr key={l.id}><td><Link className="cell-name" to="/locations">{locPath[l.id] ?? l.name}</Link></td><td>{l.kind}</td></tr>)}</tbody>
              </table>
            </Panel>
          )}

          {woHits.length > 0 && (
            <Panel title="Work Orders" icon={ClipboardList} subtitle={`${woHits.length}`} pad={false}>
              <table className="data-table"><thead><tr><th>Title</th><th>Type</th><th>Priority</th><th>Status</th></tr></thead>
                <tbody>{woHits.map((w) => <tr key={w.id}><td><Link className="cell-name" to="/work-orders">{w.title}</Link></td><td>{w.problem_type}</td><td>{w.priority}</td><td>{w.status}</td></tr>)}</tbody>
              </table>
            </Panel>
          )}

          {sysHits.length > 0 && (
            <Panel title="Systems" icon={Wrench} subtitle={`${sysHits.length}`} pad={false}>
              <table className="data-table"><thead><tr><th>System</th><th>Vendor</th><th>Status</th></tr></thead>
                <tbody>{sysHits.map((s) => <tr key={s.id}><td><Link className="cell-name" to="/systems">{s.name}</Link></td><td>{s.vendor ?? '—'}</td><td>{s.overall_status}</td></tr>)}</tbody>
              </table>
            </Panel>
          )}
        </>
      )}
    </div>
  )
}
