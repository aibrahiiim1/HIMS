import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Cable } from 'lucide-react'
import { api, type Interface, type PortVlan, type MacCount, type Neighbor } from '../api'
import { Panel, DefList, EmptyState, timeAgo } from './ui'

const oper = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')
const portShort = (i: Interface) => {
  const n = i.if_name || i.if_descr || `${i.if_index}`
  // Trim long vendor prefixes (GigabitEthernet1/0/3 -> Gi1/0/3) for the tile.
  return n.replace(/GigabitEthernet/i, 'Gi').replace(/TenGigabitEthernet/i, 'Te').replace(/FastEthernet/i, 'Fa').replace(/Ethernet/i, 'Eth')
}

export function SwitchPorts({ deviceId }: { deviceId: string }) {
  const ifaces = useQuery({ queryKey: ['interfaces', deviceId], queryFn: () => api.get<Interface[]>(`/devices/${deviceId}/interfaces`) })
  const pvlans = useQuery({ queryKey: ['port-vlans', deviceId], queryFn: () => api.get<PortVlan[]>(`/devices/${deviceId}/port-vlans`) })
  const macCounts = useQuery({ queryKey: ['mac-counts', deviceId], queryFn: () => api.get<MacCount[]>(`/devices/${deviceId}/mac-counts`) })
  const neighbors = useQuery({ queryKey: ['neighbors', deviceId], queryFn: () => api.get<Neighbor[]>(`/devices/${deviceId}/neighbors`) })

  const [sel, setSel] = useState<number | null>(null)

  const pvMap = useMemo(() => {
    const m = new Map<number, PortVlan[]>()
    for (const p of pvlans.data ?? []) { const a = m.get(p.if_index) ?? []; a.push(p); m.set(p.if_index, a) }
    return m
  }, [pvlans.data])
  const macMap = useMemo(() => {
    const m = new Map<number, number>()
    for (const c of macCounts.data ?? []) if (c.if_index != null) m.set(c.if_index, c.mac_count)
    return m
  }, [macCounts.data])
  const neighMap = useMemo(() => {
    const m = new Map<number, Neighbor>()
    for (const n of neighbors.data ?? []) if (n.local_if_index != null) m.set(n.local_if_index, n)
    return m
  }, [neighbors.data])

  const ports = useMemo(() => [...(ifaces.data ?? [])].sort((a, b) => a.if_index - b.if_index), [ifaces.data])
  const upCount = ports.filter((p) => p.oper_status === 1).length

  if (ifaces.isLoading) return <Panel title="Ports"><div className="loading">Loading ports…</div></Panel>
  if (ports.length === 0) return <Panel title="Ports"><EmptyState icon={Cable} title="No interfaces collected" message="Bind a working SNMP/CLI credential and collect this switch to populate ports." /></Panel>

  const selPort = sel != null ? ports.find((p) => p.if_index === sel) : undefined
  const selVlans = sel != null ? (pvMap.get(sel) ?? []) : []
  const selNeighbor = sel != null ? neighMap.get(sel) : undefined

  const portClass = (p: Interface) => {
    if (p.admin_status === 2) return 'port-tile port-admindown'
    const o = oper(p.oper_status)
    return `port-tile port-${o}` + (p.port_role === 'trunk' || p.port_role === 'uplink' ? ' port-trunk' : '')
  }

  return (
    <Panel
      title="Port Panel" icon={Cable}
      subtitle={`${ports.length} ports · ${upCount} up`}
      actions={
        <span className="port-legend">
          <span><i className="dot port-up" /> Up</span>
          <span><i className="dot port-down" /> Down</span>
          <span><i className="dot port-admindown" /> Disabled</span>
          <span><i className="dot port-trunk-key" /> Trunk/Uplink</span>
        </span>
      }
    >
      <div className="port-grid">
        {ports.map((p) => (
          <button
            key={p.if_index}
            className={portClass(p) + (sel === p.if_index ? ' selected' : '')}
            onClick={() => setSel(sel === p.if_index ? null : p.if_index)}
            title={`${p.if_name ?? p.if_index} · ${oper(p.oper_status)}${macMap.get(p.if_index) ? ` · ${macMap.get(p.if_index)} MAC` : ''}`}
          >
            <span className="port-num">{portShort(p)}</span>
            {(macMap.get(p.if_index) ?? 0) > 0 && <span className="port-mac">{macMap.get(p.if_index)}</span>}
          </button>
        ))}
      </div>

      {selPort && (
        <div className="port-detail">
          <div className="port-detail-head">
            <strong>{selPort.if_name ?? selPort.if_descr ?? `Port ${selPort.if_index}`}</strong>
            <span className={`badge badge-${oper(selPort.oper_status)}`}>{oper(selPort.oper_status)}</span>
            <span className={`badge badge-${selPort.port_role}`}>{selPort.port_role}</span>
          </div>
          <DefList items={[
            { label: 'Admin state', value: selPort.admin_status === 1 ? 'enabled' : selPort.admin_status === 2 ? 'disabled' : '—' },
            { label: 'Oper state', value: oper(selPort.oper_status) },
            { label: 'Mode', value: selPort.port_role === 'trunk' || selPort.port_role === 'uplink' ? 'trunk' : 'access' },
            { label: 'Untagged VLAN', value: selVlans.filter((v) => !v.tagged).map((v) => v.vlan_id).join(', ') || '—' },
            { label: 'Tagged VLANs', value: selVlans.filter((v) => v.tagged).map((v) => v.vlan_id).join(', ') || '—' },
            { label: 'Learned MACs', value: macMap.get(selPort.if_index) ?? 0 },
            { label: 'Connected device', value: selNeighbor ? (selNeighbor.rem_sys_name || selNeighbor.rem_chassis_id || '—') : '—' },
            { label: 'Remote port', value: selNeighbor?.rem_port_id ?? selNeighbor?.rem_port_desc ?? '—' },
            { label: 'Speed', value: selPort.speed_mbps ? `${selPort.speed_mbps} Mb/s` : '—' },
            { label: 'Port MAC', value: selPort.mac ?? '—' },
            { label: 'Last change', value: timeAgo(selPort.last_seen_at) },
          ]} />
          <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>
            Utilization, error counters and duplex are not collected by the current SNMP/CLI walk for this device class.
          </p>
        </div>
      )}
    </Panel>
  )
}
