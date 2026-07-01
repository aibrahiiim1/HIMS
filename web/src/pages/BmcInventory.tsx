import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Cpu } from 'lucide-react'
import { api, type Credential } from '../api'
import { PageHeader, Panel, EmptyState, usePaged, Pager } from '../components/ui'
import { ManagementBadge, ReachabilityBadge } from '../components/StatusBadges'
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
  reachability: string // online | offline | warning | unknown
  redfish_status: string
  bmc_status: string // collected | bmc_credential_required | bmc_auth_failed | not_collected
  ipmi_status: string
  power_state: string
  health_summary: string // hardware health: Redfish when collected, else SNMP-derived
  snmp_health: string // HPE iLO overall condition over SNMP (separate from Redfish)
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

// redfishTitle explains the Redfish/BMC collection state honestly — most importantly
// WHY full hardware inventory is missing (a missing Redfish credential), so the iLO/
// iDRAC gap is self-explanatory in the table without opening each device.
function redfishTitle(r: BmcRow): string {
  if (r.redfish_status === 'collected') return 'Full hardware inventory collected over authenticated Redfish (model, CPU, memory, disks, sensors).'
  if (r.redfish_status === 'credential_failed')
    return 'A Redfish credential was REJECTED by this controller — it needs its own valid Redfish login. Use Collect Redfish with the correct credential.'
  if (r.redfish_status === 'credential_required')
    return 'Redfish service is reachable but no valid credential is bound. Use Collect Redfish (Test then Collect) with an http_basic/Redfish login to gather full hardware inventory. Identity/health shown is from the unauthenticated ServiceRoot / SNMP only.'
  return 'No Redfish service detected. Reachability + any SNMP health are collected separately.'
}

// redfishBadge maps redfish_status to a badge tone: collected=ok, credential_failed=down,
// credential_required=warning, not_collected=muted.
function redfishBadge(s: string): { cls: string; label: string } {
  switch (s) {
    case 'collected': return { cls: 'up', label: 'collected' }
    case 'credential_failed': return { cls: 'down', label: 'credential_failed' }
    case 'credential_required': return { cls: 'warning', label: 'credential_required' }
    default: return { cls: 'unknown', label: s || 'not_collected' }
  }
}

// RedfishCollect is the operator-driven, single-credential Redfish path for one BMC:
// Test Connection (read-only auth check) then Collect Now (authenticated full inventory).
// It NEVER sprays — the operator picks exactly one http_basic/vendor_api login. On a
// successful collect it refreshes the table (redfish_status → collected). This is the UI
// that closes the credential_required / credential_failed gate.
function RedfishCollect({ row }: { row: BmcRow }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [cred, setCred] = useState('')
  const [msg, setMsg] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials'), enabled: open })
  const loginCreds = (creds.data ?? []).filter((c) => c.kind === 'http_basic' || c.kind === 'vendor_api')
  async function run(kind: 'test-redfish' | 'collect-bmc-redfish') {
    if (!cred) { setMsg('Pick a credential first'); return }
    setBusy(true); setMsg(null)
    try {
      const res = await api.post<{ ok: boolean; state: string; detail: string }>(`/devices/${row.id}/${kind}`, { credential_id: cred })
      setMsg((res.ok ? '✓ ' : '✗ ') + (res.detail || res.state))
      if (res.ok && kind === 'collect-bmc-redfish') qc.invalidateQueries({ queryKey: ['inventory-bmc'] })
    } catch (e) { setMsg('✗ ' + (e as Error).message) } finally { setBusy(false) }
  }
  const btn = { fontSize: 11, padding: '2px 8px', border: '1px solid var(--border)', borderRadius: 5, background: 'var(--surface)', color: 'inherit', cursor: 'pointer' } as const
  if (!open) {
    return <button style={btn} onClick={() => setOpen(true)} title="Bind a Redfish/http_basic credential and collect full hardware inventory (one credential, no spray).">
      {row.redfish_status === 'collected' ? 'Re-collect' : 'Collect Redfish…'}</button>
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 180 }}>
      <select value={cred} onChange={(e) => setCred(e.target.value)} style={{ fontSize: 11, maxWidth: 200 }}>
        <option value="">— pick credential —</option>
        {loginCreds.map((c) => <option key={c.id} value={c.id}>{c.name} · {c.kind}</option>)}
      </select>
      <div style={{ display: 'flex', gap: 4 }}>
        <button style={btn} disabled={busy} onClick={() => run('test-redfish')}>Test</button>
        <button style={btn} disabled={busy} onClick={() => run('collect-bmc-redfish')}>Collect</button>
        <button style={btn} onClick={() => { setOpen(false); setMsg(null) }}>✕</button>
      </div>
      {msg && <span className="muted" style={{ fontSize: 10, maxWidth: 220, whiteSpace: 'normal' }}>{msg}</span>}
    </div>
  )
}

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
      case 'offline': return r.reachability === 'offline'
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
      card('offline', 'Offline', (r) => r.reachability === 'offline', 'crit'),
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
                  <th title="Live reachability (separate from any collection state)">Reachability</th>
                  <th>IP</th><th>Hostname</th><th>Vendor</th><th>Model</th><th>Serial</th><th>Firmware</th>
                  <th title="HPE iLO overall condition read over SNMP (cpqHeMibCondition) — separate from Redfish">SNMP health</th>
                  <th title="Authenticated Redfish inventory health (model/CPU/mem/disks/sensors)">HW health</th>
                  <th title="Redfish credential + full-inventory collection state">Redfish inventory</th>
                  <th>Collect</th>
                  <th>Linked server</th><th>Management</th><th>Last collected</th><th>Evidence</th>
                </tr>
              </thead>
              <tbody>
                {paged.slice.map((r) => (
                  <tr key={r.id}>
                    <td><ReachabilityBadge value={r.reachability} /></td>
                    <td className="mono"><Link className="cell-name" to={`/devices/${r.id}`}>{r.ip}</Link></td>
                    <td>{fmt(r.hostname)}</td>
                    <td>{fmt(r.vendor)}</td>
                    <td>{fmt(r.model)}</td>
                    <td>{fmt(r.serial)}</td>
                    <td>{fmt(r.firmware)}</td>
                    <td title="SNMP overall condition (HPE iLO) — independent of Redfish">{r.snmp_health ? <span className={`badge badge-${/ok/i.test(r.snmp_health) ? 'up' : 'warning'}`}>{r.snmp_health}</span> : <span className="muted">—</span>}</td>
                    <td title="Authenticated Redfish hardware health">{r.redfish_status === 'collected' && r.health_summary ? <span className={`badge badge-${/ok/i.test(r.health_summary) ? 'up' : 'warning'}`}>{r.health_summary}{r.power_state ? ` · ${r.power_state}` : ''}</span> : <span className="muted">—</span>}</td>
                    <td>{(() => { const b = redfishBadge(r.redfish_status); return <span className={`badge badge-${b.cls}`} title={redfishTitle(r)}>{b.label}</span> })()}</td>
                    <td><RedfishCollect row={r} /></td>
                    <td>{linkBadge(r)}</td>
                    <td><ManagementBadge value={r.management} managedBy={r.managed_by} /></td>
                    <td className="mono">{fmt(r.last_collected)}</td>
                    <td className="muted" style={{ fontSize: 12 }} title={r.evidence}>{fmt(r.evidence)}</td>
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
