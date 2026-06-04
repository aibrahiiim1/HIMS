import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Network, Cable, Layers, Share2, Activity, Settings, Gauge } from 'lucide-react'
import { api, type Interface, type VLAN, type Neighbor, type TopologyLink, type MonitoringCheck, type MonitoringSample } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { DeviceOps } from '../components/DeviceOps'
import { Panel, TabBar, Kpi, StatusPill, EmptyState, Sparkline, timeAgo } from '../components/ui'

type Tab = 'overview' | 'interfaces' | 'vlans' | 'neighbors' | 'topology' | 'monitoring' | 'operations'
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
    { key: 'interfaces', label: 'Interfaces', icon: Cable, count: ifList.length || undefined },
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
  const checks = useQuery({ queryKey: ['dev-checks', id], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${id}/monitoring/checks`) })
  const samples = useQuery({ queryKey: ['dev-samples', id], queryFn: () => api.get<MonitoringSample[]>(`/devices/${id}/monitoring/samples`) })
  const list = checks.data ?? []
  const latencyPoints = [...(samples.data ?? [])]
    .sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime())
    .map((s) => s.latency_ms ?? 0).filter((n) => n > 0).slice(-40)

  return (
    <div className="stack">
      <Panel title="Availability Trend" icon={Gauge}>
        {latencyPoints.length > 1
          ? <Sparkline points={latencyPoints} width={640} height={80} />
          : <EmptyState icon={Gauge} title="No samples yet" message="Latency history appears after a few monitoring sweeps." />}
      </Panel>
      <Panel title="Checks" pad={false}>
        {list.length === 0 ? <EmptyState icon={Activity} title="No checks for this device" message="Add a monitor under the Operations tab." />
          : (
            <table className="data-table">
              <thead><tr><th>Kind</th><th>Port</th><th>Status</th><th>Latency</th><th>Interval</th><th>Last run</th></tr></thead>
              <tbody>{list.map((c) => (
                <tr key={c.id}><td style={{ textTransform: 'uppercase', fontWeight: 600 }}>{c.kind}</td><td>{c.target_port ?? '—'}</td><td><StatusPill status={c.last_status} /></td><td className="mono">{c.last_latency_ms != null ? `${c.last_latency_ms.toFixed(1)} ms` : '—'}</td><td>{c.interval_seconds}s</td><td className="muted">{timeAgo(c.last_run_at)}</td></tr>
              ))}</tbody>
            </table>
          )}
      </Panel>
    </div>
  )
}
