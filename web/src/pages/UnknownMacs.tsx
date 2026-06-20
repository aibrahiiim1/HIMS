import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { HelpCircle, Search } from 'lucide-react'
import { api, type UnknownMacsReport } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, TabBar, timeAgo } from '../components/ui'

// Unknown MACs — MACs learned on switch ports that map to NO inventory device. Split by the port
// they were learned on: EDGE (real access port, few MACs → actionable), TRANSIT (trunk/uplink/
// neighbor → MAC noise, not a direct endpoint), AMBIGUOUS (uncertain → review). Defaults to EDGE so
// the operator isn't drowned in thousands of transit MACs. Pure read-only; no devices are created.
const CLASS_LABEL: Record<string, string> = { edge: 'Edge unknowns', transit: 'Transit / uplink', ambiguous: 'Ambiguous', all: 'All' }

export function UnknownMacs() {
  const q = useQuery({ queryKey: ['unknown-macs'], queryFn: () => api.get<UnknownMacsReport>('/topology/unknown-macs') })
  const [tab, setTab] = useState('edge')
  const [search, setSearch] = useState('')
  const d = q.data
  const c = d?.counts

  const rows = useMemo(() => (d?.macs ?? []).filter((m) => {
    if (tab !== 'all' && m.class !== tab) return false
    if (search) { const s = search.toLowerCase(); if (!`${m.mac} ${m.possible_ip} ${m.switch_name} ${m.if_name ?? ''}`.toLowerCase().includes(s)) return false }
    return true
  }), [d, tab, search])

  const tabs = [
    { key: 'edge', label: 'Edge unknowns', icon: HelpCircle, count: c?.edge },
    { key: 'transit', label: 'Transit / uplink', icon: Search, count: c?.transit },
    { key: 'ambiguous', label: 'Ambiguous', icon: HelpCircle, count: c?.ambiguous },
    { key: 'all', label: 'All', icon: HelpCircle, count: c?.total },
  ]
  const fieldStyle = { padding: '5px 8px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 12 }
  return (
    <div>
      <PageHeader title="Unknown MACs" icon={HelpCircle} subtitle="MACs seen on switch ports but not in inventory — split into actionable edge vs transit/uplink noise (read-only; no devices created)" />
      {q.isLoading && <div className="loading">Loading FDB…</div>}
      {d && (
        <>
          <div className="kpi-grid">
            <Kpi label="Edge unknowns" value={c?.edge ?? 0} icon={HelpCircle} tone={(c?.edge ?? 0) ? 'warn' : 'ok'} sub="actionable" />
            <Kpi label="Transit / uplink MACs" value={c?.transit ?? 0} icon={Search} sub="not actionable" />
            <Kpi label="Ambiguous" value={c?.ambiguous ?? 0} tone={(c?.ambiguous ?? 0) ? 'warn' : 'default'} sub="review" />
            <Kpi label="Total unmapped" value={c?.total ?? 0} sub={d.shown < d.total ? `showing ${d.shown}` : undefined} />
          </div>
          <TabBar tabs={tabs} active={tab} onChange={setTab} />
          <Panel title={CLASS_LABEL[tab]} subtitle={`${rows.length}`} pad={false}
            actions={<input style={{ ...fieldStyle, width: 240 }} placeholder="Search MAC / IP / switch / port" value={search} onChange={(e) => setSearch(e.target.value)} />}>
            {tab === 'transit' && rows.length > 0 && (
              <div className="muted" style={{ padding: '8px 12px', fontSize: 12 }}>These MACs pass through trunk/uplink/neighbor ports — they are not directly attached here. Do not create devices from them.</div>
            )}
            {rows.length === 0 && <EmptyState icon={HelpCircle} title="Nothing here" message={tab === 'edge' ? 'No actionable edge unknowns — every edge port resolves.' : 'No MACs in this class.'} />}
            {rows.length > 0 && (
              <table className="data-table">
                <thead><tr><th>MAC</th><th>Switch</th><th>Port</th><th>VLAN</th><th>Port MACs</th><th>Possible IP</th><th>Last seen</th><th>{tab === 'edge' ? 'Action' : 'Note'}</th></tr></thead>
                <tbody>
                  {rows.map((m) => (
                    <tr key={m.mac + m.switch_id + (m.if_index ?? '')}>
                      <td className="mono">{m.mac}</td>
                      <td className="cell-name"><Link to={`/devices/${m.switch_id}`}>{m.switch_name}</Link></td>
                      <td>{m.if_name || (m.if_index != null ? `#${m.if_index}` : '—')}{m.if_alias && <small className="muted"> · {m.if_alias}</small>}</td>
                      <td>{m.vlan_id}</td>
                      <td className="muted">{m.port_macs}{m.class === 'edge' && <small className="muted"> (edge)</small>}</td>
                      <td className="mono">{m.possible_ip || <span className="muted">—</span>}</td>
                      <td className="muted">{timeAgo(m.last_seen_at)}</td>
                      <td className="muted" style={{ fontSize: 12 }}>{m.class === 'edge' && m.possible_ip
                        ? <Link to={`/discovery?ip=${encodeURIComponent(m.possible_ip)}`}>Discover {m.possible_ip}</Link>
                        : (m.reason || m.suggested)}</td>
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
