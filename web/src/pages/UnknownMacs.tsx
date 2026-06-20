import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { HelpCircle, Search } from 'lucide-react'
import { api, type UnknownMacsReport } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'

// Unknown MACs — MACs learned on switch ports that map to NO inventory device (not a known NIC,
// and no ARP-resolved IP that belongs to an inventory device). Pure read-only visibility into
// unmapped L2 endpoints — no fake devices are created. Each row suggests a next action.
export function UnknownMacs() {
  const q = useQuery({ queryKey: ['unknown-macs'], queryFn: () => api.get<UnknownMacsReport>('/topology/unknown-macs') })
  const [search, setSearch] = useState('')
  const [onlyIp, setOnlyIp] = useState(false)
  const d = q.data

  const rows = useMemo(() => (d?.macs ?? []).filter((m) => {
    if (onlyIp && !m.possible_ip) return false
    if (search) { const s = search.toLowerCase(); if (!`${m.mac} ${m.possible_ip} ${m.switch_name} ${m.if_name ?? ''}`.toLowerCase().includes(s)) return false }
    return true
  }), [d, search, onlyIp])
  const withIp = (d?.macs ?? []).filter((m) => m.possible_ip).length

  const fieldStyle = { padding: '5px 8px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 12 }
  return (
    <div>
      <PageHeader title="Unknown MACs" icon={HelpCircle} subtitle="MACs seen on switch ports but not linked to any inventory device — unmapped L2 endpoints (read-only; no devices are created)" />
      {q.isLoading && <div className="loading">Loading FDB…</div>}
      {d && (
        <>
          <div className="kpi-grid">
            <Kpi label="Unknown MACs (total)" value={d.total} icon={HelpCircle} tone={d.total ? 'warn' : 'default'} />
            <Kpi label="With ARP-resolved IP" value={withIp} icon={Search} tone={withIp ? 'info' : 'default'} sub="candidate to discover" />
            <Kpi label="No IP (pure L2)" value={(d.macs.length - withIp)} sub="investigate at switch port" />
            <Kpi label="Showing" value={`${rows.length} / ${d.shown}`} sub={d.shown < d.total ? `capped from ${d.total}` : undefined} />
          </div>
          <Panel title="Unmapped MACs" subtitle={`${rows.length}`} pad={false}
            actions={
              <div style={{ display: 'flex', gap: 8 }}>
                <input style={{ ...fieldStyle, width: 220 }} placeholder="Search MAC / IP / switch / port" value={search} onChange={(e) => setSearch(e.target.value)} />
                <label style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 4 }}><input type="checkbox" checked={onlyIp} onChange={(e) => setOnlyIp(e.target.checked)} /> only with IP</label>
              </div>
            }>
            {rows.length === 0 && <EmptyState icon={HelpCircle} title="Nothing here" message="No unmapped MACs match — every learned MAC resolves to an inventory device." />}
            {rows.length > 0 && (
              <table className="data-table">
                <thead><tr><th>MAC</th><th>Switch</th><th>Port</th><th>VLAN</th><th>Port MACs</th><th>Possible IP</th><th>Last seen</th><th>Suggested action</th></tr></thead>
                <tbody>
                  {rows.map((m) => (
                    <tr key={m.mac + m.switch_id + (m.if_index ?? '')}>
                      <td className="mono">{m.mac}</td>
                      <td className="cell-name"><Link to={`/devices/${m.switch_id}`}>{m.switch_name}</Link></td>
                      <td>{m.if_name || (m.if_index != null ? `#${m.if_index}` : '—')}{m.if_alias && <small className="muted"> · {m.if_alias}</small>}</td>
                      <td>{m.vlan_id}</td>
                      <td className="muted">{m.port_macs}{m.port_macs <= 4 && <small className="muted"> (edge)</small>}</td>
                      <td className="mono">{m.possible_ip || <span className="muted">—</span>}</td>
                      <td className="muted">{timeAgo(m.last_seen_at)}</td>
                      <td className="muted" style={{ fontSize: 12 }}>{m.possible_ip
                        ? <Link to={`/discovery?ip=${encodeURIComponent(m.possible_ip)}`}>Discover {m.possible_ip}</Link>
                        : m.suggested}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </>
      )}
    </div>
  )
}
