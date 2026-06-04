import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Cable, Download, Link2, Info } from 'lucide-react'
import { api, type Interface, type PortVlan, type MacCount, type Neighbor } from '../api'
import { Panel, DefList, EmptyState, timeAgo } from './ui'

// Switch Port Mapping Pro — a rack-style faceplate over the real interface,
// port-VLAN, FDB and neighbor data, with status / mode / VLAN / occupancy
// filters and CSV export. Live counters (utilization, errors, duplex, PoE) are
// not collected for these device classes, so they are labelled as such rather
// than fabricated.

const SPP_CSS = `
.spp-toolbar { display:flex; flex-wrap:wrap; align-items:center; gap:10px; margin-bottom:14px; }
.spp-toolbar .sp { flex:1; }
.spp-stat { display:inline-flex; align-items:baseline; gap:6px; font-size:13px; color:var(--text-muted); }
.spp-stat b { font-size:15px; color:var(--text); }
.spp-rack { display:grid; grid-auto-flow:column; grid-template-rows:repeat(2,minmax(0,1fr)); gap:5px; overflow-x:auto; padding:10px; background:var(--surface-2); border:1px solid var(--border); border-radius:10px; }
.spp-rack .port-tile { position:relative; }
.spp-rack .port-tile .neigh { position:absolute; bottom:2px; left:50%; transform:translateX(-50%); width:5px; height:5px; border-radius:50%; background:var(--brand); }
.spp-field { background:var(--surface); border:1px solid var(--border); border-radius:8px; padding:6px 9px; font-size:13px; color:var(--text); }
.spp-dim { opacity:.28; }
`

const oper = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')
const isTrunk = (p: Interface) => p.port_role === 'trunk' || p.port_role === 'uplink'
const portShort = (i: Interface) => {
  const n = i.if_name || i.if_descr || `${i.if_index}`
  return n.replace(/GigabitEthernet/i, 'Gi').replace(/TenGigabitEthernet/i, 'Te').replace(/FastEthernet/i, 'Fa').replace(/Ethernet/i, 'Eth')
}
const speedLabel = (mbps?: number | null) => {
  if (!mbps) return '—'
  if (mbps >= 1000) return `${mbps / 1000} Gb/s`
  return `${mbps} Mb/s`
}

type StatusF = 'all' | 'up' | 'down' | 'disabled'
type ModeF = 'all' | 'access' | 'trunk'
type OccF = 'all' | 'connected' | 'empty'

export function SwitchPorts({ deviceId }: { deviceId: string }) {
  const ifaces = useQuery({ queryKey: ['interfaces', deviceId], queryFn: () => api.get<Interface[]>(`/devices/${deviceId}/interfaces`) })
  const pvlans = useQuery({ queryKey: ['port-vlans', deviceId], queryFn: () => api.get<PortVlan[]>(`/devices/${deviceId}/port-vlans`) })
  const macCounts = useQuery({ queryKey: ['mac-counts', deviceId], queryFn: () => api.get<MacCount[]>(`/devices/${deviceId}/mac-counts`) })
  const neighbors = useQuery({ queryKey: ['neighbors', deviceId], queryFn: () => api.get<Neighbor[]>(`/devices/${deviceId}/neighbors`) })

  const [sel, setSel] = useState<number | null>(null)
  const [statusF, setStatusF] = useState<StatusF>('all')
  const [modeF, setModeF] = useState<ModeF>('all')
  const [occF, setOccF] = useState<OccF>('all')
  const [vlanF, setVlanF] = useState<string>('all')
  const [text, setText] = useState('')

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

  const allVlans = useMemo(() => {
    const s = new Set<number>()
    for (const p of pvlans.data ?? []) s.add(p.vlan_id)
    return [...s].sort((a, b) => a - b)
  }, [pvlans.data])

  const ports = useMemo(() => [...(ifaces.data ?? [])].sort((a, b) => a.if_index - b.if_index), [ifaces.data])

  const occupied = (p: Interface) => (macMap.get(p.if_index) ?? 0) > 0 || neighMap.has(p.if_index)
  const matches = (p: Interface) => {
    if (statusF === 'disabled' && p.admin_status !== 2) return false
    if (statusF === 'up' && oper(p.oper_status) !== 'up') return false
    if (statusF === 'down' && !(oper(p.oper_status) === 'down' && p.admin_status !== 2)) return false
    if (modeF === 'trunk' && !isTrunk(p)) return false
    if (modeF === 'access' && isTrunk(p)) return false
    if (occF === 'connected' && !occupied(p)) return false
    if (occF === 'empty' && occupied(p)) return false
    if (vlanF !== 'all') {
      const vs = pvMap.get(p.if_index) ?? []
      if (!vs.some((v) => String(v.vlan_id) === vlanF)) return false
    }
    if (text.trim()) {
      const t = text.trim().toLowerCase()
      const hay = `${p.if_name ?? ''} ${p.if_descr ?? ''} ${p.if_alias ?? ''}`.toLowerCase()
      if (!hay.includes(t)) return false
    }
    return true
  }
  const visible = useMemo(() => new Set(ports.filter(matches).map((p) => p.if_index)), [ports, statusF, modeF, occF, vlanF, text, macMap, neighMap, pvMap]) // eslint-disable-line react-hooks/exhaustive-deps

  if (ifaces.isLoading) return <Panel title="Ports"><div className="loading">Loading ports…</div></Panel>
  if (ports.length === 0) return <Panel title="Ports"><EmptyState icon={Cable} title="No interfaces collected" message="Bind a working SNMP/CLI credential and collect this switch to populate ports." /></Panel>

  const upCount = ports.filter((p) => p.oper_status === 1).length
  const downCount = ports.filter((p) => oper(p.oper_status) === 'down' && p.admin_status !== 2).length
  const disabledCount = ports.filter((p) => p.admin_status === 2).length
  const trunkCount = ports.filter(isTrunk).length
  const connectedCount = ports.filter(occupied).length

  const selPort = sel != null ? ports.find((p) => p.if_index === sel) : undefined
  const selVlans = sel != null ? (pvMap.get(sel) ?? []) : []
  const selNeighbor = sel != null ? neighMap.get(sel) : undefined

  const portClass = (p: Interface) => {
    let c = p.admin_status === 2 ? 'port-tile port-admindown' : `port-tile port-${oper(p.oper_status)}`
    if (isTrunk(p)) c += ' port-trunk'
    if (!visible.has(p.if_index)) c += ' spp-dim'
    return c
  }

  const exportCsv = () => {
    const head = ['port', 'if_index', 'admin', 'oper', 'role', 'mode', 'native_vlan', 'tagged_vlans', 'learned_macs', 'connected_device', 'remote_port', 'speed_mbps', 'mac', 'description', 'last_seen']
    const rows = ports.map((p) => {
      const vs = pvMap.get(p.if_index) ?? []
      const n = neighMap.get(p.if_index)
      return [
        p.if_name ?? p.if_index, p.if_index,
        p.admin_status === 1 ? 'enabled' : p.admin_status === 2 ? 'disabled' : '',
        oper(p.oper_status), p.port_role, isTrunk(p) ? 'trunk' : 'access',
        vs.filter((v) => !v.tagged).map((v) => v.vlan_id).join(' '),
        vs.filter((v) => v.tagged).map((v) => v.vlan_id).join(' '),
        macMap.get(p.if_index) ?? 0,
        n ? (n.rem_sys_name || n.rem_chassis_id || '') : '',
        n ? (n.rem_port_id || n.rem_port_desc || '') : '',
        p.speed_mbps ?? '', p.mac ?? '', (p.if_alias || p.if_descr || '').replace(/[\r\n,]/g, ' '), p.last_seen_at,
      ].join(',')
    })
    const blob = new Blob([[head.join(','), ...rows].join('\n')], { type: 'text/csv' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `port-map-${deviceId}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <Panel
      title="Port Panel" icon={Cable}
      subtitle={`${ports.length} ports · ${upCount} up`}
      actions={<button className="btn btn-xs" onClick={exportCsv}><Download size={13} /> Export map</button>}
    >
      <style>{SPP_CSS}</style>

      {/* Summary */}
      <div className="spp-toolbar">
        <span className="spp-stat"><b>{upCount}</b> up</span>
        <span className="spp-stat"><b>{downCount}</b> down</span>
        <span className="spp-stat"><b>{disabledCount}</b> disabled</span>
        <span className="spp-stat"><b>{trunkCount}</b> trunk/uplink</span>
        <span className="spp-stat"><b>{connectedCount}</b> connected</span>
        <span className="sp" />
        <span className="port-legend">
          <span><i className="dot port-up" /> Up</span>
          <span><i className="dot port-down" /> Down</span>
          <span><i className="dot port-admindown" /> Disabled</span>
          <span><i className="dot port-trunk-key" /> Trunk</span>
        </span>
      </div>

      {/* Filters */}
      <div className="spp-toolbar">
        <select className="spp-field" value={statusF} onChange={(e) => setStatusF(e.target.value as StatusF)}>
          <option value="all">Status: all</option><option value="up">Up</option><option value="down">Down</option><option value="disabled">Disabled</option>
        </select>
        <select className="spp-field" value={modeF} onChange={(e) => setModeF(e.target.value as ModeF)}>
          <option value="all">Mode: all</option><option value="access">Access</option><option value="trunk">Trunk/Uplink</option>
        </select>
        <select className="spp-field" value={occF} onChange={(e) => setOccF(e.target.value as OccF)}>
          <option value="all">Occupancy: all</option><option value="connected">Connected</option><option value="empty">Empty</option>
        </select>
        <select className="spp-field" value={vlanF} onChange={(e) => setVlanF(e.target.value)}>
          <option value="all">VLAN: all</option>
          {allVlans.map((v) => <option key={v} value={String(v)}>VLAN {v}</option>)}
        </select>
        <input className="spp-field sp" value={text} onChange={(e) => setText(e.target.value)} placeholder="Filter by port name / description…" />
        <span className="muted" style={{ fontSize: 12 }}>{visible.size}/{ports.length}</span>
      </div>

      <div className="spp-rack">
        {ports.map((p) => (
          <button
            key={p.if_index}
            className={portClass(p) + (sel === p.if_index ? ' selected' : '')}
            onClick={() => setSel(sel === p.if_index ? null : p.if_index)}
            title={`${p.if_name ?? p.if_index} · ${oper(p.oper_status)}${macMap.get(p.if_index) ? ` · ${macMap.get(p.if_index)} MAC` : ''}${neighMap.has(p.if_index) ? ` · ${neighMap.get(p.if_index)!.rem_sys_name || 'neighbor'}` : ''}`}
          >
            <span className="port-num">{portShort(p)}</span>
            {(macMap.get(p.if_index) ?? 0) > 0 && <span className="port-mac">{macMap.get(p.if_index)}</span>}
            {neighMap.has(p.if_index) && <span className="neigh" />}
          </button>
        ))}
      </div>

      {selPort && (
        <div className="port-detail">
          <div className="port-detail-head">
            <strong>{selPort.if_name ?? selPort.if_descr ?? `Port ${selPort.if_index}`}</strong>
            <span className={`badge badge-${oper(selPort.oper_status)}`}>{oper(selPort.oper_status)}</span>
            <span className={`badge badge-${selPort.port_role}`}>{selPort.port_role}</span>
            {selNeighbor && <span className="badge"><Link2 size={11} /> {selNeighbor.rem_sys_name || 'neighbor'}</span>}
          </div>
          <DefList items={[
            { label: 'Admin state', value: selPort.admin_status === 1 ? 'enabled' : selPort.admin_status === 2 ? 'disabled' : '—' },
            { label: 'Oper state', value: oper(selPort.oper_status) },
            { label: 'Mode', value: isTrunk(selPort) ? 'trunk' : 'access' },
            { label: 'Description', value: selPort.if_alias || selPort.if_descr || '—' },
            { label: 'Native (untagged) VLAN', value: selVlans.filter((v) => !v.tagged).map((v) => v.vlan_id).join(', ') || '—' },
            { label: 'Tagged VLANs', value: selVlans.filter((v) => v.tagged).map((v) => v.vlan_id).join(', ') || '—' },
            { label: 'Learned MACs', value: macMap.get(selPort.if_index) ?? 0 },
            { label: 'Connected device', value: selNeighbor ? (selNeighbor.rem_sys_name || selNeighbor.rem_chassis_id || '—') : '—' },
            { label: 'Remote port', value: selNeighbor?.rem_port_id ?? selNeighbor?.rem_port_desc ?? '—' },
            { label: 'Speed', value: speedLabel(selPort.speed_mbps) },
            { label: 'Port MAC', value: selPort.mac ?? '—' },
            { label: 'Source', value: (selPort as Interface & { collection_source?: string }).collection_source ?? 'snmp' },
            { label: 'Last change', value: timeAgo(selPort.last_seen_at) },
          ]} />
          <p className="muted" style={{ fontSize: 12, marginTop: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
            <Info size={13} /> Utilization, error/discard/CRC counters, duplex and PoE are not collected by the current SNMP/CLI walk for this device class.
          </p>
        </div>
      )}
    </Panel>
  )
}
