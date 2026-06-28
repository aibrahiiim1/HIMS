import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Boxes } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, EmptyState, colorFor, usePaged, Pager } from '../components/ui'
import { ManagementBadge } from '../components/StatusBadges'
import { SummaryCards, type SummaryCard } from '../components/SummaryCards'
import { ManualClassify } from '../components/ManualClassify'
import { deviceTypeLabel } from '../inventoryGroups'

const DETAIL_BASE: Record<string, string> = {
  server: '/servers', virtual_host: '/virtual-hosts', virtual_machine: '/virtual-hosts',
  firewall: '/firewalls', endpoint: '/workstations', printer: '/printers', ups: '/ups',
  pbx: '/pbx', nvr: '/cctv', dvr: '/cctv', camera: '/cctv', wireless_controller: '/wlan',
}
function detailLink(d: Device): string {
  return `${DETAIL_BASE[d.category] || '/devices'}/${d.id}`
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
const CRED_REQUIRED = new Set(['needs_credential', 'credential_failed', 'not_authorized'])

const SOURCE_TONE: Record<string, string> = {
  manual_override: 'badge-up', fingerprint: 'badge-info', snmp: 'badge-info',
  hostname: 'badge-unknown', service: 'badge-unknown', auto: 'badge-unknown',
}

// GroupInventory is the single data-driven grid behind every "All <Group>" page and every
// per-category child page (Biometric, POS, …). It fetches the union of `categories`, renders
// a mixed-type table (Device Type + Subtype distinguish server/vhost/BMC/…), data-driven
// clickable summary cards (counts reconcile with the table), the required filters, the
// classification source, and an operator manual-classify action. Nothing is hardcoded.
export function GroupInventory({ title, subtitle, categories, allowManualClassify }: { title: string; subtitle?: string; categories: string[]; allowManualClassify?: boolean }) {
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
  const [credReq, setCredReq] = useState(false)
  const [manualOnly, setManualOnly] = useState(false)
  const [q, setQ] = useState('')
  const reset = () => { setType(''); setSubtype(''); setVendor(''); setMgmt(''); setSite(''); setProto(''); setCredReq(false); setManualOnly(false) }

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
      (!credReq || CRED_REQUIRED.has(d.management ?? '')) &&
      (!manualOnly || d.classification_source === 'manual_override') &&
      (!t || displayName(d).toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
        (d.hostname ?? '').toLowerCase().includes(t) || (d.model ?? '').toLowerCase().includes(t)),
    )
  }, [data, type, subtype, vendor, mgmt, site, proto, credReq, manualOnly, q])
  const paged = usePaged(filtered, { pageSize: 15 })

  // Data-driven, clickable summary cards. Counts come from `all` (the same source as the
  // table), so a card value equals the table count when that card's filter is applied.
  const cards: SummaryCard[] = useMemo(() => {
    const cnt = (f: (d: Device) => boolean) => all.filter(f).length
    const noFilter = !type && !subtype && !vendor && !mgmt && !site && !proto && !credReq && !manualOnly
    const list: SummaryCard[] = [
      { label: 'Total', value: all.length, tone: 'info', active: noFilter, onClick: reset },
    ]
    for (const c of opts.types) {
      list.push({ label: deviceTypeLabel(c), value: cnt((d) => d.category === c), tone: 'muted', active: type === c, onClick: () => { reset(); setType(type === c ? '' : c); paged.setPage(0) } })
    }
    list.push(
      { label: 'Managed', value: cnt((d) => d.management === 'managed'), tone: 'ok', active: mgmt === 'managed', onClick: () => { reset(); setMgmt(mgmt === 'managed' ? '' : 'managed'); paged.setPage(0) } },
      { label: 'Unmanaged', value: cnt((d) => d.management === 'unmanaged'), tone: 'muted', active: mgmt === 'unmanaged', onClick: () => { reset(); setMgmt(mgmt === 'unmanaged' ? '' : 'unmanaged'); paged.setPage(0) } },
      { label: 'Credential required', value: cnt((d) => CRED_REQUIRED.has(d.management ?? '')), tone: 'crit', active: credReq, onClick: () => { reset(); setCredReq(!credReq); paged.setPage(0) } },
    )
    const manual = cnt((d) => d.classification_source === 'manual_override')
    if (manual > 0 || allowManualClassify) {
      list.push({ label: 'Manual classified', value: manual, tone: 'info', active: manualOnly, onClick: () => { reset(); setManualOnly(!manualOnly); paged.setPage(0) } })
    }
    return list
  }, [data, type, subtype, vendor, mgmt, site, proto, credReq, manualOnly])

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
      {data && all.length > 0 && <SummaryCards cards={cards} />}
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
                  <th>Management</th><th>Primary protocol</th><th>Site</th><th>Last seen</th><th>Last collected</th><th>Confidence</th><th>Source</th>
                  {allowManualClassify && <th></th>}
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
                    <td><span className={`badge ${SOURCE_TONE[d.classification_source ?? 'auto'] ?? 'badge-unknown'}`} title="how the category was decided">{(d.classification_source ?? 'auto').replace(/_/g, ' ')}</span></td>
                    {allowManualClassify && <td><ManualClassify device={d} /></td>}
                  </tr>
                ))}
              </tbody>
            </table>
            <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
          </>
        )}
      </Panel>
    </div>
  )
}
