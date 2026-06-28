import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Cpu } from 'lucide-react'
import { api } from '../api'
import { PageHeader, Panel, EmptyState, usePaged, Pager } from '../components/ui'
import { ManagementBadge } from '../components/StatusBadges'

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
  ipmi_status: string
  power_state: string
  health_summary: string
  linked_server: string
  linked_server_id?: string
  management: string
  managed_by?: string[]
  site: string
  last_seen?: string
  last_collected?: string
  confidence: number
  evidence: string
}

const fmt = (s?: string) => s || '—'

// BmcInventory is the iLO / BMC / iDRAC page: out-of-band management controllers ONLY,
// never mixed with normal servers (they are their own 'bmc' category). The linked physical
// server is shown when known, otherwise an honest "unlinked_bmc" — links are never faked.
export function BmcInventory() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['inventory-bmc'],
    queryFn: () => api.get<BmcRow[]>('/inventory/bmc'),
  })
  const all = data ?? []
  const [q, setQ] = useState('')
  const [vendor, setVendor] = useState('')
  const vendors = useMemo(() => Array.from(new Set(all.map((r) => r.vendor).filter(Boolean))).sort(), [data])
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    return all.filter((r) => (!vendor || r.vendor === vendor) &&
      (!t || r.ip.includes(t) || (r.hostname || '').toLowerCase().includes(t) || (r.model || '').toLowerCase().includes(t)))
  }, [data, q, vendor])
  const paged = usePaged(filtered, { pageSize: 15 })

  return (
    <div>
      <PageHeader title="iLO / BMC / iDRAC" subtitle="Out-of-band management controllers (HPE iLO, Dell iDRAC, Lenovo XClarity/IMM, Supermicro IPMI, Redfish)" icon={Cpu} />
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
                  <th>Redfish</th><th>IPMI</th><th>Linked server</th><th>Health</th><th>Management</th><th>Last collected</th><th>Evidence</th>
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
                    <td><span className={`badge badge-${r.redfish_status === 'collected' ? 'up' : 'unknown'}`}>{r.redfish_status}</span></td>
                    <td><span className="badge badge-unknown">{r.ipmi_status}</span></td>
                    <td>{r.linked_server
                      ? <Link className="cell-name" to={r.linked_server_id ? `/devices/${r.linked_server_id}` : '#'}>{r.linked_server}</Link>
                      : <span className="badge badge-warning" title="No BMC→server link established; not faked">unlinked_bmc</span>}</td>
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
