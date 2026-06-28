import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Boxes } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, EmptyState, colorFor, usePaged, Pager } from '../components/ui'
import { ManagementBadge, ReachabilityBadge } from '../components/StatusBadges'
import { deviceTypeLabel } from '../inventoryGroups'

// detailBase maps a device's category to the right detail route so the name links to a
// working page. Falls back to the generic /devices/:id dispatcher (router-by-category).
const DETAIL_BASE: Record<string, string> = {
  server: '/servers', virtual_host: '/virtual-hosts', virtual_machine: '/virtual-hosts',
  firewall: '/firewalls', endpoint: '/workstations', printer: '/printers', ups: '/ups',
  pbx: '/pbx', nvr: '/cctv', dvr: '/cctv', camera: '/cctv', wireless_controller: '/wlan',
}
function detailLink(d: Device): string {
  const base = DETAIL_BASE[d.category] || '/devices'
  return `${base}/${d.id}`
}
function displayName(d: Device): string {
  const ip = d.primary_ip ?? ''
  const real = (v?: string | null) => !!v && v !== ip && !/^localhost(\.|$)/i.test(v)
  if (real(d.name)) return d.name
  if (real(d.hostname)) return d.hostname!
  return ip || d.name || '—'
}
const primaryProtocol = (d: Device) => (d.managed_by && d.managed_by.length ? d.managed_by[0] : '')
const fmtDate = (s?: string | null) => (s ? s.slice(0, 16).replace('T', ' ') : '—')

// GroupInventory is the single data-driven grid behind every "All <Group>" page and every
// per-category child page. It fetches the union of `categories` from the same /devices
// endpoint the rest of the app uses, then renders a mixed-type table (Device Type + Subtype
// columns distinguish server vs virtual host vs BMC vs …) with the required filters. Nothing
// is hardcoded — every row, type, vendor, and count comes from the live classification model.
export function GroupInventory({ title, subtitle, categories }: { title: string; subtitle?: string; categories: string[] }) {
  const catParam = categories.join(',')
  const { data, isLoading, error } = useQuery({
    queryKey: ['group-inventory', catParam],
    queryFn: () => api.get<Device[]>(`/devices?category=${catParam}`),
  })
  const all = data ?? []

  const [type, setType] = useState('')
  const [subtype, setSubtype] = useState('')
  const [vendor, setVendor] = useState('')
  const [mgmt, setMgmt] = useState('')
  const [site, setSite] = useState('')
  const [proto, setProto] = useState('')
  const [q, setQ] = useState('')

  const opts = useMemo(() => {
    const uniq = (xs: (string | undefined | null)[]) => Array.from(new Set(xs.filter((x): x is string => !!x))).sort()
    return {
      types: uniq(all.map((d) => d.category)),
      subtypes: uniq(all.map((d) => d.subtype)),
      vendors: uniq(all.map((d) => d.vendor)),
      mgmts: uniq(all.map((d) => d.management)),
      sites: uniq(all.map((d) => d.location)),
      protos: uniq(all.flatMap((d) => d.managed_by ?? [])),
    }
  }, [data])

  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    return all.filter((d) =>
      (!type || d.category === type) &&
      (!subtype || d.subtype === subtype) &&
      (!vendor || d.vendor === vendor) &&
      (!mgmt || d.management === mgmt) &&
      (!site || d.location === site) &&
      (!proto || (d.managed_by ?? []).includes(proto)) &&
      (!t || displayName(d).toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
        (d.hostname ?? '').toLowerCase().includes(t) || (d.model ?? '').toLowerCase().includes(t)),
    )
  }, [data, type, subtype, vendor, mgmt, site, proto, q])
  const paged = usePaged(filtered, { pageSize: 15 })

  const sel = (val: string, set: (v: string) => void, label: string, options: string[], fmt?: (v: string) => string) => (
    <select value={val} onChange={(e) => { set(e.target.value); paged.setPage(0) }}
      style={{ padding: '6px 8px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, background: 'var(--surface)', color: 'inherit' }}>
      <option value="">{label}: all</option>
      {options.map((o) => <option key={o} value={o}>{fmt ? fmt(o) : o}</option>)}
    </select>
  )

  return (
    <div>
      <PageHeader title={title} subtitle={subtitle ?? 'Data-driven view of the inventory classification model'} icon={Boxes} />
      <Panel title={title} subtitle={`${filtered.length} of ${all.length} device(s)`} pad={false}>
        {isLoading && <div className="loading">Loading…</div>}
        {error && <div style={{ padding: 16 }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && all.length === 0 && (
          <EmptyState icon={Boxes} title="No devices in this group yet"
            message="Devices appear here automatically as discovery classifies them into these categories. No device is hardcoded." />
        )}
        {data && all.length > 0 && (
          <>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, padding: '10px' }}>
              <input placeholder="Search IP / hostname / model…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }}
                style={{ padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, width: 260, maxWidth: '100%' }} />
              {sel(type, setType, 'Type', opts.types, deviceTypeLabel)}
              {opts.subtypes.length > 0 && sel(subtype, setSubtype, 'Subtype', opts.subtypes)}
              {sel(vendor, setVendor, 'Vendor', opts.vendors)}
              {sel(mgmt, setMgmt, 'Management', opts.mgmts, (v) => v.replace(/_/g, ' '))}
              {opts.sites.length > 0 && sel(site, setSite, 'Site', opts.sites)}
              {opts.protos.length > 0 && sel(proto, setProto, 'Protocol', opts.protos, (v) => v.toUpperCase())}
            </div>
            <table className="data-table">
              <thead>
                <tr>
                  <th>Device</th><th>IP</th><th>Device Type</th><th>Subtype</th><th>Vendor</th><th>Model</th>
                  <th>Management</th><th>Primary protocol</th><th>Site</th><th>Last seen</th><th>Last collected</th><th>Confidence</th>
                </tr>
              </thead>
              <tbody>
                {paged.slice.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <div className="dev-cell">
                        <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(displayName(d) || d.category).charAt(0).toUpperCase()}</span>
                        <Link className="cell-name" to={detailLink(d)}>{displayName(d)}</Link>
                      </div>
                    </td>
                    <td className="mono">{d.primary_ip ?? '—'}</td>
                    <td><span className="badge" style={{ background: colorFor(d.category), color: '#fff' }}>{deviceTypeLabel(d.category)}</span></td>
                    <td>{d.subtype || <span className="muted">—</span>}</td>
                    <td>{d.vendor ?? '—'}</td>
                    <td>{d.model ?? '—'}</td>
                    <td><ManagementBadge value={d.management} managedBy={d.managed_by} reason={d.management_reason} /></td>
                    <td>{primaryProtocol(d) ? <span className="mono">{primaryProtocol(d).toUpperCase()}</span> : <span className="muted">—</span>}</td>
                    <td>{d.location || <span className="muted">—</span>}</td>
                    <td className="mono" title="last reachability check">{fmtDate(d.last_monitoring_at)}</td>
                    <td className="mono" title="last discovery/collection">{fmtDate(d.last_discovery_at)}</td>
                    <td>{typeof d.confidence_score === 'number' ? `${d.confidence_score}%` : <span className="muted">—</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
            <div className="muted" style={{ fontSize: 11, padding: '6px 10px' }}>
              <ReachabilityBadge value="online" /> reachability and management are separate signals — see each device for details.
            </div>
          </>
        )}
      </Panel>
    </div>
  )
}
