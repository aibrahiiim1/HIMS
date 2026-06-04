import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Network, Cable, Layers, Share2, Activity, Settings, Gauge, LayoutGrid, Table, Router } from 'lucide-react'
import { api, type Interface, type VLAN, type Neighbor, type TopologyLink, type MonitoringCheck, type MonitoringSample, type MacEntry, type ArpEntry } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { ClassificationCard } from '../components/ClassificationCard'
import { DeviceOps } from '../components/DeviceOps'
import { SwitchPorts } from '../components/SwitchPorts'
import { Panel, TabBar, Kpi, StatusPill, EmptyState, Sparkline, timeAgo } from '../components/ui'

type Tab = 'overview' | 'ports' | 'mac' | 'arp' | 'interfaces' | 'vlans' | 'neighbors' | 'topology' | 'monitoring' | 'operations'
const operLabel = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')

export function SwitchDetail() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState<Tab>('overview')

  const ifaces = useQuery({ queryKey: ['interfaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })
  const vlans = useQuery({ queryKey: ['vlans', id], queryFn: () => api.get<VLAN[]>(`/devices/${id}/vlans`) })
  const neighbors = useQuery({ queryKey: ['neighbors', id], queryFn: () => api.get<Neighbor[]>(`/devices/${id}/neighbors`) })
  const topo = useQuery({ queryKey: ['device-topology', id], queryFn: () => api.get<TopologyLink[]>(`/devices/${id}/topology`) })

  const ifList = ifaces.data ?? []
  const ifUp = ifList.filter((i) => i.oper_status === 1).length

  const tabs = [
    { key: 'overview', label: 'Overview', icon: Activity },
    { key: 'ports', label: 'Ports', icon: LayoutGrid, count: ifList.length || undefined },
    { key: 'mac', label: 'MAC Table', icon: Table },
    { key: 'arp', label: 'ARP Table', icon: Router },
    { key: 'vlans', label: 'VLANs', icon: Layers, count: vlans.data?.length || undefined },
    { key: 'neighbors', label: 'Neighbors', icon: Share2, count: neighbors.data?.length || undefined },
    { key: 'topology', label: 'Topology', icon: Network, count: topo.data?.length || undefined },
    { key: 'monitoring', label: 'Monitoring', icon: Gauge },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Network} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {tab === 'overview' && (
        <div>
          <div className="kpi-grid">
            <Kpi label="Interfaces" value={ifList.length} icon={Cable} tone="info" sub={`${ifUp} operationally up`} />
            <Kpi label="VLANs" value={vlans.data?.length ?? 0} icon={Layers} tone="default" />
            <Kpi label="Neighbors" value={neighbors.data?.length ?? 0} icon={Share2} tone="default" sub="LLDP / CDP" />
            <Kpi label="Topology Links" value={topo.data?.length ?? 0} icon={Network} tone="default" />
          </div>
          <div style={{ marginBottom: 16 }}><ClassificationCard deviceId={id!} /></div>
          <div className="grid-2">
            <Panel title="Top Interfaces" icon={Cable} pad={false}>
              {ifList.length === 0 ? <EmptyState icon={Cable} title="No interfaces collected" />
                : <InterfaceTable data={ifList.slice(0, 8)} />}
            </Panel>
            <Panel title="Neighbors" icon={Share2} pad={false}>
              {(neighbors.data?.length ?? 0) === 0 ? <EmptyState icon={Share2} title="No LLDP/CDP neighbors" message="Empty is normal in mixed-vendor segments." />
                : <NeighborTable data={neighbors.data!.slice(0, 8)} />}
            </Panel>
          </div>
        </div>
      )}

      {tab === 'ports' && <SwitchPorts deviceId={id!} />}
      {tab === 'mac' && <MacTable id={id!} />}
      {tab === 'arp' && <ArpTable id={id!} />}

      {tab === 'interfaces' && (
        <Panel title="Interfaces" subtitle={`${ifList.length}`} pad={false}>
          {ifaces.isLoading && <div className="loading">Loading interfaces…</div>}
          {ifaces.error && <div style={{ padding: 20 }}><div className="error-msg">{(ifaces.error as Error).message}</div></div>}
          {ifaces.data && ifList.length === 0 && <EmptyState icon={Cable} title="No interfaces collected" />}
          {ifList.length > 0 && <InterfaceTable data={ifList} full />}
        </Panel>
      )}

      {tab === 'vlans' && (
        <Panel title="VLANs" subtitle={`${vlans.data?.length ?? 0}`} pad={false}>
          {vlans.isLoading && <div className="loading">Loading VLANs…</div>}
          {vlans.data && vlans.data.length === 0 && <EmptyState icon={Layers} title="No VLANs collected" />}
          {(vlans.data?.length ?? 0) > 0 && (
            <table className="data-table"><thead><tr><th>VLAN ID</th><th>Name</th></tr></thead>
              <tbody>{vlans.data!.map((v) => <tr key={v.id}><td className="cell-name">{v.vlan_id}</td><td>{v.name ?? '—'}</td></tr>)}</tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'neighbors' && (
        <Panel title="LLDP / CDP Neighbors" subtitle={`${neighbors.data?.length ?? 0}`} pad={false}>
          {neighbors.isLoading && <div className="loading">Loading neighbors…</div>}
          {neighbors.data && neighbors.data.length === 0 && <EmptyState icon={Share2} title="No neighbors discovered" message="Empty is normal in mixed-vendor segments." />}
          {(neighbors.data?.length ?? 0) > 0 && <NeighborTable data={neighbors.data!} full />}
        </Panel>
      )}

      {tab === 'topology' && (
        <Panel title="Topology Links" subtitle={`${topo.data?.length ?? 0}`} pad={false}>
          {topo.isLoading && <div className="loading">Loading links…</div>}
          {topo.data && topo.data.length === 0 && <EmptyState icon={Network} title="No topology links computed" message="Links are derived from LLDP/CDP and ARP/MAC correlation." />}
          {(topo.data?.length ?? 0) > 0 && (
            <table className="data-table"><thead><tr><th>Local port</th><th>Connects to</th><th>Remote IP</th><th>Source</th></tr></thead>
              <tbody>{topo.data!.map((l, i) => (
                <tr key={i}><td>{l.local_if_name ?? l.local_if_index ?? '—'}</td><td className="cell-name">{l.remote_device_name ?? l.remote_sys_name ?? '—'}</td><td className="mono">{l.remote_ip ?? '—'}</td><td><span className={`badge badge-${l.link_source}`}>{l.link_source}</span></td></tr>
              ))}</tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'monitoring' && <MonitoringTab id={id!} />}
      {tab === 'operations' && <DeviceOps deviceId={id!} />}
    </div>
  )
}

function InterfaceTable({ data, full }: { data: Interface[]; full?: boolean }) {
  return (
    <table className="data-table">
      <thead><tr><th>Port</th><th>Name</th>{full && <th>Alias</th>}<th>Speed</th><th>Oper</th><th>Role</th>{full && <th>MAC</th>}</tr></thead>
      <tbody>
        {data.map((i) => (
          <tr key={i.id}>
            <td>{i.if_index}</td>
            <td className="cell-name">{i.if_name ?? i.if_descr ?? '—'}</td>
            {full && <td>{i.if_alias ?? '—'}</td>}
            <td>{i.speed_mbps ? `${i.speed_mbps} Mb` : '—'}</td>
            <td><StatusPill status={operLabel(i.oper_status)} label={operLabel(i.oper_status)} /></td>
            <td><span className={`badge badge-${i.port_role}`}>{i.port_role}</span></td>
            {full && <td className="mono">{i.mac ?? '—'}</td>}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function NeighborTable({ data, full }: { data: Neighbor[]; full?: boolean }) {
  return (
    <table className="data-table">
      <thead><tr><th>Local port</th><th>Neighbor</th><th>Remote port</th>{full && <th>Mgmt IP</th>}<th>Proto</th></tr></thead>
      <tbody>
        {data.map((n) => (
          <tr key={n.id}>
            <td>{n.local_if_name ?? n.local_if_index ?? '—'}</td>
            <td className="cell-name">{n.rem_sys_name ?? n.rem_chassis_id ?? '—'}</td>
            <td>{n.rem_port_id ?? n.rem_port_desc ?? '—'}</td>
            {full && <td className="mono">{n.rem_mgmt_ip ?? '—'}</td>}
            <td><span className={`badge badge-${n.protocol}`}>{n.protocol}</span></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function MonitoringTab({ id }: { id: string }) {
  const checks = useQuery({ queryKey: ['dev-checks', id], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${id}/monitoring/checks`), refetchInterval: 30_000 })
  const samples = useQuery({ queryKey: ['dev-samples', id], queryFn: () => api.get<MonitoringSample[]>(`/devices/${id}/monitoring/samples?limit=200`), refetchInterval: 30_000 })
  const list = checks.data ?? []
  const asc = [...(samples.data ?? [])].sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime())

  const latencyPoints = asc.map((s) => s.latency_ms ?? 0).filter((n) => n > 0).slice(-60)
  const valuePoints = asc.filter((s) => s.value_num != null).map((s) => s.value_num as number).slice(-60)
  const strip = asc.slice(-60)
  const total = asc.length
  const ups = asc.filter((s) => s.status === 'up').length
  const uptime = total ? Math.round((ups / total) * 100) : 0
  const lat = latencyPoints.length ? {
    avg: latencyPoints.reduce((a, b) => a + b, 0) / latencyPoints.length,
    min: Math.min(...latencyPoints), max: Math.max(...latencyPoints),
  } : null
  const sColor = (s: string) => (s === 'up' ? 'var(--ok)' : s === 'warning' ? 'var(--warn)' : s === 'down' ? 'var(--crit)' : 'var(--surface-3)')

  return (
    <div className="stack">
      <Panel title="Performance & Availability" icon={Gauge} subtitle={`${total} samples`}>
        {total === 0 && <EmptyState icon={Gauge} title="No samples yet" message="History appears after a few monitoring sweeps (every 30s)." />}
        {total > 0 && (
          <>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(140px,1fr))', gap: 14, marginBottom: 16 }}>
              <Stat label="Uptime (window)" value={`${uptime}%`} tone={uptime >= 99 ? 'ok' : uptime >= 90 ? 'warn' : 'crit'} />
              <Stat label="Avg latency" value={lat ? `${lat.avg.toFixed(1)} ms` : '—'} />
              <Stat label="Min / Max" value={lat ? `${lat.min.toFixed(0)} / ${lat.max.toFixed(0)} ms` : '—'} />
              <Stat label="Samples" value={String(total)} />
            </div>
            {/* status timeline strip */}
            <div style={{ display: 'flex', gap: 1, height: 22, borderRadius: 4, overflow: 'hidden', marginBottom: 16 }} title="status over time (oldest → newest)">
              {strip.map((s, i) => <div key={i} style={{ flex: 1, background: sColor(s.status) }} title={`${new Date(s.time).toLocaleTimeString()} · ${s.status}`} />)}
            </div>
            {latencyPoints.length > 1 && (<>
              <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Latency (ms)</div>
              <Sparkline points={latencyPoints} width={680} height={70} />
            </>)}
            {valuePoints.length > 1 && (<>
              <div className="muted" style={{ fontSize: 12, margin: '14px 0 4px' }}>SNMP metric (value)</div>
              <Sparkline points={valuePoints} width={680} height={70} color="var(--brand)" />
            </>)}
          </>
        )}
      </Panel>
      <Panel title="Checks" pad={false}>
        {list.length === 0 ? <EmptyState icon={Activity} title="No checks for this device" message="Add a monitor under the Operations tab." />
          : (
            <table className="data-table">
              <thead><tr><th>Kind</th><th>Port / OID</th><th>Status</th><th>Latency</th><th>Interval</th><th>Fails</th><th>Last run</th></tr></thead>
              <tbody>{list.map((c) => (
                <tr key={c.id}><td style={{ textTransform: 'uppercase', fontWeight: 600 }}>{c.kind}</td><td className="mono">{c.kind === 'snmp' ? (c.oid ?? 'sysUpTime') : (c.target_port ?? '—')}</td><td><StatusPill status={c.last_status} /></td><td className="mono">{c.last_latency_ms != null ? `${c.last_latency_ms.toFixed(1)} ms` : '—'}</td><td>{c.interval_seconds}s</td><td>{c.consecutive_failures}</td><td className="muted">{timeAgo(c.last_run_at)}</td></tr>
              ))}</tbody>
            </table>
          )}
      </Panel>
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'ok' | 'warn' | 'crit' }) {
  const color = tone === 'ok' ? 'var(--ok)' : tone === 'warn' ? 'var(--warn)' : tone === 'crit' ? 'var(--crit)' : 'var(--text)'
  return (
    <div className="card" style={{ margin: 0, padding: '10px 14px' }}>
      <div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '.04em' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 800, color, marginTop: 2 }}>{value}</div>
    </div>
  )
}

function MacTable({ id }: { id: string }) {
  const q = useQuery({ queryKey: ['mac', id], queryFn: () => api.get<MacEntry[]>(`/devices/${id}/mac`) })
  const [term, setTerm] = useState('')
  const [vlan, setVlan] = useState('all')
  const rows = q.data ?? []
  const vlanOpts = useMemo(() => [...new Set(rows.map((r) => r.vlan_id))].sort((a, b) => a - b), [q.data])
  const filtered = rows.filter((r) => {
    if (vlan !== 'all' && String(r.vlan_id) !== vlan) return false
    if (!term.trim()) return true
    const t = term.toLowerCase()
    return r.mac.toLowerCase().includes(t) || (r.if_name ?? '').toLowerCase().includes(t) || (r.owner_name ?? '').toLowerCase().includes(t) || (r.owner_vendor ?? '').toLowerCase().includes(t)
  })
  return (
    <Panel title="MAC Address Table" icon={Table} subtitle={`${filtered.length} of ${rows.length}`} pad={false}>
      <div className="row" style={{ padding: 'var(--space-4) var(--space-5)', borderBottom: '1px solid var(--border)' }}>
        <input className="field" style={{ width: 260 }} placeholder="Search MAC / port / device / vendor…" value={term} onChange={(e) => setTerm(e.target.value)} />
        <select className="field" value={vlan} onChange={(e) => setVlan(e.target.value)}>
          <option value="all">All VLANs</option>
          {vlanOpts.map((v) => <option key={v} value={String(v)}>VLAN {v}</option>)}
        </select>
      </div>
      {q.isLoading && <div className="loading">Loading MAC table…</div>}
      {q.data && rows.length === 0 && <EmptyState icon={Table} title="No MAC entries collected" message="The forwarding table is populated by SNMP/CLI collection of this switch." />}
      {filtered.length > 0 && (
        <table className="data-table">
          <thead><tr><th>MAC</th><th>VLAN</th><th>Port</th><th>Owner device</th><th>Vendor</th><th>Source</th><th>Last seen</th></tr></thead>
          <tbody>
            {filtered.map((m) => (
              <tr key={m.id}>
                <td className="mono">{m.mac}</td>
                <td>{m.vlan_id}</td>
                <td>{m.if_name ?? (m.if_index != null ? `if ${m.if_index}` : '—')}</td>
                <td>{m.owner_name ?? '—'}</td>
                <td>{m.owner_vendor ?? '—'}</td>
                <td><span className="badge badge-unknown">{m.collection_source}</span></td>
                <td className="muted">{timeAgo(m.last_seen_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

function ArpTable({ id }: { id: string }) {
  const q = useQuery({ queryKey: ['arp', id], queryFn: () => api.get<ArpEntry[]>(`/devices/${id}/arp`) })
  const [term, setTerm] = useState('')
  const rows = q.data ?? []
  const filtered = rows.filter((r) => {
    if (!term.trim()) return true
    const t = term.toLowerCase()
    return r.ip_address.toLowerCase().includes(t) || r.mac.toLowerCase().includes(t) || (r.if_name ?? '').toLowerCase().includes(t) || (r.owner_name ?? '').toLowerCase().includes(t)
  })
  return (
    <Panel title="ARP Table" icon={Router} subtitle={`${filtered.length} of ${rows.length}`} pad={false}>
      <div className="row" style={{ padding: 'var(--space-4) var(--space-5)', borderBottom: '1px solid var(--border)' }}>
        <input className="field" style={{ width: 260 }} placeholder="Search IP / MAC / port / device…" value={term} onChange={(e) => setTerm(e.target.value)} />
      </div>
      {q.isLoading && <div className="loading">Loading ARP table…</div>}
      {q.data && rows.length === 0 && <EmptyState icon={Router} title="No ARP entries collected" message="ARP is collected from L3 devices (routers, firewalls, L3 switches)." />}
      {filtered.length > 0 && (
        <table className="data-table">
          <thead><tr><th>IP address</th><th>MAC</th><th>Interface</th><th>Resolved device</th><th>Source</th><th>Last seen</th></tr></thead>
          <tbody>
            {filtered.map((a) => (
              <tr key={a.id}>
                <td className="mono">{a.ip_address}</td>
                <td className="mono">{a.mac}</td>
                <td>{a.if_name ?? (a.if_index != null ? `if ${a.if_index}` : '—')}</td>
                <td>{a.owner_name ?? '—'}</td>
                <td><span className="badge badge-unknown">{a.collection_source}</span></td>
                <td className="muted">{timeAgo(a.last_seen_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
