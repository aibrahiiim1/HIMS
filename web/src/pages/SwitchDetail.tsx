import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type Interface, type VLAN, type Neighbor, type TopologyLink } from '../api'

type Tab = 'interfaces' | 'vlans' | 'neighbors' | 'topology'

const operLabel = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')

export function SwitchDetail() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState<Tab>('interfaces')

  return (
    <div>
      <div className="card">
        <h2>Switch detail</h2>
        <div className="tabs">
          {(['interfaces', 'vlans', 'neighbors', 'topology'] as Tab[]).map((t) => (
            <div key={t} className={`tab ${tab === t ? 'active' : ''}`} onClick={() => setTab(t)}>
              {t[0].toUpperCase() + t.slice(1)}
            </div>
          ))}
        </div>
        {tab === 'interfaces' && <Interfaces id={id!} />}
        {tab === 'vlans' && <Vlans id={id!} />}
        {tab === 'neighbors' && <Neighbors id={id!} />}
        {tab === 'topology' && <DeviceTopology id={id!} />}
      </div>
    </div>
  )
}

function Interfaces({ id }: { id: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['interfaces', id],
    queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`),
  })
  if (isLoading) return <div className="loading">Loading interfaces…</div>
  if (error) return <div className="error-msg">{(error as Error).message}</div>
  if (!data?.length) return <div className="muted">No interfaces collected.</div>
  return (
    <table>
      <thead>
        <tr><th>Port</th><th>Name</th><th>Alias</th><th>Speed</th><th>Admin</th><th>Oper</th><th>Role</th><th>MAC</th></tr>
      </thead>
      <tbody>
        {data.map((i) => (
          <tr key={i.id}>
            <td>{i.if_index}</td>
            <td>{i.if_name ?? i.if_descr ?? '—'}</td>
            <td>{i.if_alias ?? '—'}</td>
            <td>{i.speed_mbps ? `${i.speed_mbps} Mb` : '—'}</td>
            <td>{operLabel(i.admin_status)}</td>
            <td><span className={`badge badge-${operLabel(i.oper_status)}`}>{operLabel(i.oper_status)}</span></td>
            <td><span className={`badge badge-${i.port_role}`}>{i.port_role}</span></td>
            <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{i.mac ?? '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Vlans({ id }: { id: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['vlans', id],
    queryFn: () => api.get<VLAN[]>(`/devices/${id}/vlans`),
  })
  if (isLoading) return <div className="loading">Loading VLANs…</div>
  if (error) return <div className="error-msg">{(error as Error).message}</div>
  if (!data?.length) return <div className="muted">No VLANs collected.</div>
  return (
    <table>
      <thead><tr><th>VLAN ID</th><th>Name</th></tr></thead>
      <tbody>{data.map((v) => <tr key={v.id}><td>{v.vlan_id}</td><td>{v.name ?? '—'}</td></tr>)}</tbody>
    </table>
  )
}

function Neighbors({ id }: { id: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['neighbors', id],
    queryFn: () => api.get<Neighbor[]>(`/devices/${id}/neighbors`),
  })
  if (isLoading) return <div className="loading">Loading neighbors…</div>
  if (error) return <div className="error-msg">{(error as Error).message}</div>
  if (!data?.length) return <div className="muted">No LLDP/CDP neighbors. (Empty is normal in mixed-vendor segments.)</div>
  return (
    <table>
      <thead><tr><th>Local port</th><th>Neighbor</th><th>Remote port</th><th>Mgmt IP</th><th>Protocol</th></tr></thead>
      <tbody>
        {data.map((n) => (
          <tr key={n.id}>
            <td>{n.local_if_name ?? n.local_if_index ?? '—'}</td>
            <td>{n.rem_sys_name ?? n.rem_chassis_id ?? '—'}</td>
            <td>{n.rem_port_id ?? n.rem_port_desc ?? '—'}</td>
            <td>{n.rem_mgmt_ip ?? '—'}</td>
            <td><span className={`badge badge-${n.protocol}`}>{n.protocol}</span></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function DeviceTopology({ id }: { id: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['device-topology', id],
    queryFn: () => api.get<TopologyLink[]>(`/devices/${id}/topology`),
  })
  if (isLoading) return <div className="loading">Loading links…</div>
  if (!data?.length) return <div className="muted">No topology links computed for this device yet.</div>
  return (
    <table>
      <thead><tr><th>Local port</th><th>Connects to</th><th>Remote IP</th><th>Source</th></tr></thead>
      <tbody>
        {data.map((l, i) => (
          <tr key={i}>
            <td>{l.local_if_name ?? l.local_if_index ?? '—'}</td>
            <td>{l.remote_device_name ?? l.remote_sys_name ?? '—'}</td>
            <td>{l.remote_ip ?? '—'}</td>
            <td><span className={`badge badge-${l.link_source}`}>{l.link_source}</span></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
