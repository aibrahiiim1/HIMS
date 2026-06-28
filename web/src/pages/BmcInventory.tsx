import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Cpu } from 'lucide-react'
import { api } from '../api'
import { PageHeader, Panel, EmptyState, usePaged, Pager } from '../components/ui'
import { ManagementBadge } from '../components/StatusBadges'
import { SummaryCards, type SummaryCard } from '../components/SummaryCards'
import { AddDeviceButton } from '../components/AddDeviceButton'

interface BmcRow {
  id: string
  ip: string
  hostname: string
  vendor: string
  model: string
  serial: string
  firmware: string
  controller_kind: string
  redfish_status: string
  bmc_status: string // collected | bmc_credential_required | bmc_auth_failed | not_collected
  ipmi_status: string
  power_state: string
  health_summary: string
  link_state: string // linked | candidate_link | unlinked_bmc
  linked_server: string
  linked_server_id?: string
  link_confidence: number
  link_evidence: string
  management: string
  managed_by?: string[]
  site: string
  last_seen?: string
  last_collected?: string
  confidence: number
  evidence: string
}

const fmt = (s?: string) => s || '—'
const CRED_REQUIRED = new Set(['needs_credential', 'credential_failed', 'not_authorized'])

// BmcInventory is the iLO / BMC / iDRAC page: out-of-band management controllers ONLY,
// never mixed with normal servers (their own 'bmc' category). Data-driven summary cards
// reconcile with the table. The physical-server link is the result of an evidence-based
// pass (serial/UUID/hostname) — linked / candidate_link / honest unlinked_bmc with reason;
// never faked.
export function BmcInventory() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['inventory-bmc'],
    queryFn: () => api.get<BmcRow[]>('/inventory/bmc'),
  })
  const all = data ?? []
  const [q, setQ] = useState('')
  const [vendor, setVendor] = useState('')
  const [filterKey, setFilterKey] = useState('') // card-driven filter
  const vendors = useMemo(() => Array.from(new Set(all.map((r) => r.vendor).filter(Boolean))).sort(), [data])

  const cardMatch = (r: BmcRow): boolean => {
    switch (filterKey) {
      case 'managed': return r.management === 'managed'
      case 'credreq': return CRED_REQUIRED.has(r.management)
      case 'linked': return r.link_state === 'linked'
      case 'candidate': return r.link_state === 'candidate_link'
      case 'unlinked': return r.link_state === 'unlinked_bmc'
      case 'redfish_ok': return r.redfish_status === 'collected'
      case 'redfish_no': return r.redfish_status !== 'collected'
      case 'cred_req': return r.bmc_status === 'bmc_credential_required'
      case 'health_bad': return !!r.health_summary && !/ok/i.test(r.health_summary)
      default: return true
    }
  }
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    return all.filter((r) => (!vendor || r.vendor === vendor) && cardMatch(r) &&
      (!t || r.ip.includes(t) || (r.hostname || '').toLowerCase().includes(t) || (r.model || '').toLowerCase().includes(t)))
  }, [data, q, vendor, filterKey])
  const paged = usePaged(filtered, { pageSize: 15 })

  const cards: SummaryCard[] = useMemo(() => {
    const c = (f: (r: BmcRow) => boolean) => all.filter(f).length
    const card = (key: string, label: string, f: (r: BmcRow) => boolean, tone: SummaryCard['tone']): SummaryCard =>
      ({ label, value: c(f), tone, active: filterKey === key, onClick: () => { setFilterKey(filterKey === key ? '' : key); paged.setPage(0) } })
    return [
      { label: 'Total BMC / iLO', value: all.length, tone: 'info', active: filterKey === '', onClick: () => { setFilterKey(''); paged.setPage(0) } },
      card('managed', 'Managed', (r) => r.management === 'managed', 'ok'),
      card('credreq', 'Credential required', (r) => CRED_REQUIRED.has(r.management), 'crit'),
      card('linked', 'Linked to server', (r) => r.link_state === 'linked', 'ok'),
      card('candidate', 'Candidate link', (r) => r.link_state === 'candidate_link', 'warn'),
      card('unlinked', 'Unlinked BMC', (r) => r.link_state === 'unlinked_bmc', 'muted'),
      card('redfish_ok', 'Redfish OK', (r) => r.redfish_status === 'collected', 'ok'),
      card('cred_req', 'BMC credential required', (r) => r.bmc_status === 'bmc_credential_required', 'warn'),
      card('health_bad', 'Health warning/critical', (r) => !!r.health_summary && !/ok/i.test(r.health_summary), 'crit'),
    ]
  }, [data, filterKey])

  const linkBadge = (r: BmcRow) => {
    if (r.link_state === 'linked') return <Link className="cell-name" to={r.linked_server_id ? `/devices/${r.linked_server_id}` : '#'} title={r.link_evidence}>{r.linked_server} <span className="muted">({r.link_confidence}%)</span></Link>
    if (r.link_state === 'candidate_link') return <Link className="badge badge-warning" to={r.linked_server_id ? `/devices/${r.linked_server_id}` : '#'} title={r.link_evidence} style={{ textDecoration: 'none' }}>candidate: {r.linked_server} ({r.link_confidence}%)</Link>
    return <span className="badge badge-unknown" title={r.link_evidence}>unlinked_bmc</span>
  }

  return (
    <div>
      <PageHeader title="iLO / BMC / iDRAC" subtitle="Out-of-band management controllers (HPE iLO, Dell iDRAC, Lenovo XClarity/IMM, Huawei iBMC, Supermicro IPMI, Redfish)" icon={Cpu}
        actions={<AddDeviceButton defaultType="bmc" label="Add iLO / BMC" />} />
      {data && all.length > 0 && <SummaryCards cards={cards} />}
      <Panel title="Out-of-band controllers" subtitle={`${filtered.length} of ${all.length} controller(s)`} pad={false}>
        {isLoading && <div className="loading">Loading…</div>}
        {error && <div style={{ padding: 16 }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && all.length === 0 && (
          <EmptyState icon={Cpu} title="No BMC / iLO controllers yet"
            message="Out-of-band controllers appear here once discovery classifies a device as a BMC (iLO/iDRAC/XClarity/IPMI/Redfish identity). None are hardcoded." />
        )}
        {data && all.length > 0 && (
          <>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, padding: 10 }}>
              <input placeholder="Search IP / hostname / model…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }}
                style={{ padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, width: 260 }} />
              <select value={vendor} onChange={(e) => { setVendor(e.target.value); paged.setPage(0) }}
                style={{ padding: '6px 8px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, background: 'var(--surface)', color: 'inherit' }}>
                <option value="">Vendor: all</option>
                {vendors.map((v) => <option key={v} value={v}>{v}</option>)}
              </select>
            </div>
            <table className="data-table">
              <thead>
                <tr>
                  <th>IP</th><th>Hostname</th><th>Vendor</th><th>Model</th><th>Serial</th><th>Firmware</th>
                  <th>Redfish</th><th>IPMI</th><th>Linked server</th><th>Link evidence</th><th>Health</th><th>Management</th><th>Last collected</th><th>Evidence</th>
                </tr>
              </thead>
              <tbody>
                {paged.slice.map((r) => (
                  <tr key={r.id}>
                    <td className="mono"><Link className="cell-name" to={`/devices/${r.id}`}>{r.ip}</Link></td>
                    <td>{fmt(r.hostname)}</td>
                    <td>{fmt(r.vendor)}</td>
                    <td>{fmt(r.model)}</td>
                    <td>{fmt(r.serial)}</td>
                    <td>{fmt(r.firmware)}</td>
                    <td><span className={`badge badge-${r.bmc_status === 'collected' ? 'up' : r.bmc_status === 'bmc_auth_failed' ? 'down' : 'warning'}`} title="BMC/Redfish collection status">{r.bmc_status || r.redfish_status}</span></td>
                    <td><span className="badge badge-unknown">{r.ipmi_status}</span></td>
                    <td>{linkBadge(r)}</td>
                    <td className="muted" style={{ fontSize: 12 }} title={r.link_evidence}>{fmt(r.link_evidence)}</td>
                    <td>{r.health_summary ? <span className={`badge badge-${/ok/i.test(r.health_summary) ? 'up' : 'warning'}`}>{r.health_summary}{r.power_state ? ` · ${r.power_state}` : ''}</span> : <span className="muted">—</span>}</td>
                    <td><ManagementBadge value={r.management} managedBy={r.managed_by} /></td>
                    <td className="mono">{fmt(r.last_collected)}</td>
                    <td className="muted" style={{ fontSize: 12 }}>{fmt(r.evidence)}</td>
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
