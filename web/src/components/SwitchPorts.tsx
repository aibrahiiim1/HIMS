import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Cable, Download, Link2, Info, Network, AlertTriangle, Layers, Cpu } from 'lucide-react'
import { api, type Interface, type PortVlan, type MacCount, type MacEntry, type Neighbor } from '../api'
import { Panel, DefList, EmptyState, usePaged, Pager, timeAgo } from './ui'

// Switch Port Mapping Pro — a rack-style faceplate over the real interface,
// port-VLAN, FDB and neighbor data. Physical ports and logical interfaces (SVIs,
// port-channels, loopbacks, tunnels…) are shown in separate racks. Each port
// opens a detail card with its VLAN membership (vendor-neutral tagged/untagged)
// and the MAC addresses learned on it (searchable + paginated). Data that a
// device's SNMP/CLI walk did not return is labelled honestly, never fabricated.

const SPP_CSS = `
.spp-toolbar { display:flex; flex-wrap:wrap; align-items:center; gap:10px; margin-bottom:14px; }
.spp-toolbar .sp { flex:1; }
.spp-stat { display:inline-flex; align-items:baseline; gap:6px; font-size:13px; color:var(--text-muted); }
.spp-stat b { font-size:15px; color:var(--text); }
.spp-racktitle { display:flex; align-items:center; gap:8px; font-size:12px; font-weight:700; text-transform:uppercase; letter-spacing:.4px; color:var(--text-muted); margin:14px 0 6px; }
.spp-rack { display:grid; grid-auto-flow:column; grid-template-rows:repeat(2,minmax(0,1fr)); gap:5px; overflow-x:auto; padding:10px; background:var(--surface-2); border:1px solid var(--border); border-radius:10px; }
.spp-rack.logical { grid-template-rows:repeat(1,minmax(0,1fr)); }
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

// isLogical separates virtual interfaces (VLAN SVIs, port-channels/LAGs, loopback,
// tunnel, null, mgmt/CPU/stack) from physical front-panel ports — by IANAifType
// when present, else by name. Aruba switches that expose no ifType (0) and a bare
// numeric name fall through to physical, which is correct for numbered ports.
function isLogical(p: Interface): boolean {
  const t = p.if_type ?? 0
  if ([24, 53, 131, 135, 136, 142, 161, 162].includes(t)) return true // loopback/propVirtual/tunnel/l2vlan/l3vlan/lag
  if (t === 6) return false // ethernetCsmacd → physical
  const n = (p.if_name || p.if_descr || '').toLowerCase()
  return /vlan|port-?channel|^po\d|loopback|^lo\d+$|tunnel|^tu\d|^null|bridge-agg|aggregat|^bagg|^trk|mgmt|management|^cpu|stack|^vl\d/.test(n)
}

type StatusF = 'all' | 'up' | 'down' | 'disabled'
type ModeF = 'all' | 'access' | 'trunk'
type OccF = 'all' | 'connected' | 'empty'

export function SwitchPorts({ deviceId }: { deviceId: string }) {
  const ifaces = useQuery({ queryKey: ['interfaces', deviceId], queryFn: () => api.get<Interface[]>(`/devices/${deviceId}/interfaces`) })
  const pvlans = useQuery({ queryKey: ['port-vlans', deviceId], queryFn: () => api.get<PortVlan[]>(`/devices/${deviceId}/port-vlans`) })
  const macCounts = useQuery({ queryKey: ['mac-counts', deviceId], queryFn: () => api.get<MacCount[]>(`/devices/${deviceId}/mac-counts`) })
  const macs = useQuery({ queryKey: ['mac', deviceId], queryFn: () => api.get<MacEntry[]>(`/devices/${deviceId}/mac`) })
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
  const macByPort = useMemo(() => {
    const m = new Map<number, MacEntry[]>()
    for (const e of macs.data ?? []) if (e.if_index != null) { const a = m.get(e.if_index) ?? []; a.push(e); m.set(e.if_index, a) }
    return m
  }, [macs.data])
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
  // FDB count fallback: prefer mac-counts, else derive from the full MAC table.
  const macCountFor = (idx: number) => macMap.get(idx) ?? (macByPort.get(idx)?.length ?? 0)

  const occupied = (p: Interface) => macCountFor(p.if_index) > 0 || neighMap.has(p.if_index)
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
  const visible = useMemo(() => new Set(ports.filter(matches).map((p) => p.if_index)), [ports, statusF, modeF, occF, vlanF, text, macMap, macByPort, neighMap, pvMap]) // eslint-disable-line react-hooks/exhaustive-deps

  const physical = useMemo(() => ports.filter((p) => !isLogical(p)), [ports])
  const logical = useMemo(() => ports.filter(isLogical), [ports])

  if (ifaces.isLoading) return <Panel title="Ports"><div className="loading">Loading ports…</div></Panel>
  if (ports.length === 0) return <Panel title="Ports"><EmptyState icon={Cable} title="No interfaces collected" message="Bind a working SNMP/CLI credential and collect this switch to populate ports." /></Panel>

  const upCount = ports.filter((p) => p.oper_status === 1).length
  const downCount = ports.filter((p) => oper(p.oper_status) === 'down' && p.admin_status !== 2).length
  const disabledCount = ports.filter((p) => p.admin_status === 2).length
  const trunkCount = ports.filter(isTrunk).length
  const connectedCount = ports.filter(occupied).length

  // Honesty signals: IF-MIB not returned (no oper/admin status at all) and VLAN
  // membership not collected. These explain "grey" ports rather than a UI bug.
  const ifMibMissing = ports.every((p) => (p.oper_status ?? 0) === 0 && (p.admin_status ?? 0) === 0)
  const vlanMissing = (pvlans.data?.length ?? 0) === 0

  const selPort = sel != null ? ports.find((p) => p.if_index === sel) : undefined
  const selVlans = sel != null ? (pvMap.get(sel) ?? []) : []
  const selNeighbor = sel != null ? neighMap.get(sel) : undefined
  const selMacs = sel != null ? (macByPort.get(sel) ?? []) : []
  const untagged = selVlans.filter((v) => !v.tagged).map((v) => v.vlan_id)
  const tagged = selVlans.filter((v) => v.tagged).map((v) => v.vlan_id)

  const portClass = (p: Interface) => {
    let c = p.admin_status === 2 ? 'port-tile port-admindown' : `port-tile port-${oper(p.oper_status)}`
    if (isTrunk(p)) c += ' port-trunk'
    if (!visible.has(p.if_index)) c += ' spp-dim'
    return c
  }

  const exportCsv = () => {
    const head = ['port', 'if_index', 'kind', 'admin', 'oper', 'role', 'mode', 'untagged_vlan', 'tagged_vlans', 'learned_macs', 'connected_device', 'remote_port', 'speed_mbps', 'mac', 'description', 'last_seen']
    const rows = ports.map((p) => {
      const vs = pvMap.get(p.if_index) ?? []
      const n = neighMap.get(p.if_index)
      return [
        p.if_name ?? p.if_index, p.if_index, isLogical(p) ? 'logical' : 'physical',
        p.admin_status === 1 ? 'enabled' : p.admin_status === 2 ? 'disabled' : '',
        oper(p.oper_status), p.port_role, isTrunk(p) ? 'trunk' : 'access',
        vs.filter((v) => !v.tagged).map((v) => v.vlan_id).join(' '),
        vs.filter((v) => v.tagged).map((v) => v.vlan_id).join(' '),
        macCountFor(p.if_index),
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

  const renderRack = (list: Interface[], logicalRack: boolean) => (
    <div className={'spp-rack' + (logicalRack ? ' logical' : '')}>
      {list.map((p) => (
        <button
          key={p.if_index}
          className={portClass(p) + (sel === p.if_index ? ' selected' : '')}
          onClick={() => setSel(sel === p.if_index ? null : p.if_index)}
          title={`${p.if_name ?? p.if_index} · ${oper(p.oper_status)}${macCountFor(p.if_index) ? ` · ${macCountFor(p.if_index)} MAC` : ''}${neighMap.has(p.if_index) ? ` · ${neighMap.get(p.if_index)!.rem_sys_name || 'neighbor'}` : ''}`}
        >
          <span className="port-num">{portShort(p)}</span>
          {macCountFor(p.if_index) > 0 && <span className="port-mac">{macCountFor(p.if_index)}</span>}
          {neighMap.has(p.if_index) && <span className="neigh" />}
        </button>
      ))}
    </div>
  )

  return (
    <Panel
      title="Port Panel" icon={Cable}
      subtitle={`${physical.length} physical · ${logical.length} logical · ${upCount} up`}
      actions={<button className="btn btn-xs" onClick={exportCsv}><Download size={13} /> Export map</button>}
    >
      <style>{SPP_CSS}</style>

      {ifMibMissing && (
        <div className="enc-banner warn" style={{ marginBottom: 12 }}>
          <AlertTriangle size={14} style={{ verticalAlign: -2 }} /> This switch returned no IF-MIB operational data — port up/down, names, type and speed weren't collected, so tiles show grey (status unknown) rather than green/red. Bind or verify an SNMP credential under <strong>Operations</strong>, then <strong>Re-scan this device</strong>. Port roles below are still inferred from the MAC/topology tables.
        </div>
      )}
      {vlanMissing && !ifMibMissing && (
        <div className="enc-banner info" style={{ marginBottom: 12 }}>
          <Info size={14} style={{ verticalAlign: -2 }} /> VLAN membership (tagged/untagged per port) wasn't collected for this device, so the per-port VLAN fields read “not collected”.
        </div>
      )}

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
          <span><i className="dot port-unknown" /> Unknown</span>
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

      <div className="spp-racktitle"><Cpu size={13} /> Physical ports <span className="badge badge-unknown">{physical.length}</span></div>
      {physical.length === 0 ? <EmptyState icon={Cable} title="No physical ports" /> : renderRack(physical, false)}

      {logical.length > 0 && (
        <>
          <div className="spp-racktitle"><Layers size={13} /> Logical interfaces <span className="badge badge-unknown">{logical.length}</span> <span className="muted" style={{ textTransform: 'none', fontWeight: 400 }}>VLAN SVIs · port-channels · loopback · tunnel</span></div>
          {renderRack(logical, true)}
        </>
      )}

      {selPort && (
        <div className="port-detail">
          <div className="port-detail-head">
            <strong>{selPort.if_name ?? selPort.if_descr ?? `Port ${selPort.if_index}`}</strong>
            <span className="badge badge-unknown">{isLogical(selPort) ? 'logical' : 'physical'}</span>
            <span className={`badge badge-${oper(selPort.oper_status)}`}>{oper(selPort.oper_status)}</span>
            <span className={`badge badge-${selPort.port_role}`}>{selPort.port_role}</span>
            {selNeighbor && <span className="badge"><Link2 size={11} /> {selNeighbor.rem_sys_name || 'neighbor'}</span>}
          </div>
          <DefList items={[
            { label: 'ifIndex', value: selPort.if_index },
            { label: 'Admin state', value: selPort.admin_status === 1 ? 'enabled' : selPort.admin_status === 2 ? 'disabled' : <span className="muted">not collected</span> },
            { label: 'Oper state', value: (selPort.oper_status ?? 0) === 0 ? <span className="muted">not collected</span> : oper(selPort.oper_status) },
            { label: 'Mode', value: isTrunk(selPort) ? 'Trunk (802.1Q tagged)' : 'Access (untagged)' },
            { label: 'Description / alias', value: selPort.if_alias || selPort.if_descr || <span className="muted">—</span> },
            {
              label: isTrunk(selPort) ? 'Native / untagged VLAN' : 'Access (untagged) VLAN',
              value: selVlans.length === 0 ? <span className="muted">not collected</span> : (untagged.length ? untagged.join(', ') : <span className="muted">none</span>),
            },
            {
              label: 'Tagged VLANs (trunk)',
              value: selVlans.length === 0 ? <span className="muted">not collected</span> : (tagged.length ? tagged.join(', ') : <span className="muted">none</span>),
            },
            { label: 'Learned MACs', value: macCountFor(selPort.if_index) },
            { label: 'Connected device', value: selNeighbor ? (selNeighbor.rem_sys_name || selNeighbor.rem_chassis_id || '—') : <span className="muted">—</span> },
            { label: 'Remote port', value: selNeighbor?.rem_port_id ?? selNeighbor?.rem_port_desc ?? <span className="muted">—</span> },
            { label: 'Remote mgmt IP', value: selNeighbor?.rem_mgmt_ip ?? <span className="muted">—</span> },
            { label: 'Speed', value: speedLabel(selPort.speed_mbps) },
            { label: 'Port MAC', value: selPort.mac ?? <span className="muted">—</span> },
            { label: 'Last change', value: timeAgo(selPort.last_seen_at) },
          ]} />

          {/* Learned MAC addresses on this port — searchable + paginated past 5. */}
          <PortMacs entries={selMacs} fdbMissing={(macs.data?.length ?? 0) === 0} isUplink={isTrunk(selPort)} />

          <p className="muted" style={{ fontSize: 12, marginTop: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
            <Info size={13} /> Utilization, error/discard/CRC counters, duplex and PoE are not collected by the current SNMP/CLI walk for this device class.
          </p>
        </div>
      )}
    </Panel>
  )
}

// PortMacs renders the FDB entries learned on one port. ≤5 entries show plainly;
// past 5 it gains a search box + pagination. Honest about empty / uncollected.
function PortMacs({ entries, fdbMissing, isUplink }: { entries: MacEntry[]; fdbMissing: boolean; isUplink: boolean }) {
  const [term, setTerm] = useState('')
  const filtered = useMemo(() => {
    if (!term.trim()) return entries
    const t = term.toLowerCase()
    return entries.filter((m) =>
      m.mac.toLowerCase().includes(t) || (m.owner_name ?? '').toLowerCase().includes(t) ||
      (m.owner_vendor ?? '').toLowerCase().includes(t) || String(m.vlan_id).includes(t))
  }, [entries, term])
  const paged = usePaged(filtered, { pageSize: 5 })

  return (
    <div className="card" style={{ margin: '12px 0 0' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <strong style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
          <Network size={14} /> Learned MAC addresses <span className="badge badge-unknown">{entries.length}</span>
        </strong>
        {entries.length > 5 && (
          <input className="spp-field" style={{ maxWidth: 240 }} placeholder="Search MAC / device / vendor / VLAN…" value={term} onChange={(e) => { setTerm(e.target.value); paged.setPage(0) }} />
        )}
      </div>
      {entries.length === 0 ? (
        <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
          {fdbMissing
            ? 'The bridge MAC table (FDB) was not collected for this switch — bind/verify an SNMP credential and re-scan.'
            : isUplink
              ? 'No MACs attributed to this port. On an uplink/trunk the FDB usually attributes learned MACs to the access port where the host actually lives, not the uplink.'
              : 'No MAC addresses learned on this port — nothing is currently bridging here, or the host is silent.'}
        </p>
      ) : (
        <>
          <table className="data-table" style={{ marginTop: 10 }}>
            <thead><tr><th>MAC</th><th>VLAN</th><th>Owner device</th><th>Vendor</th><th>Source</th><th>Last seen</th></tr></thead>
            <tbody>
              {paged.slice.map((m) => (
                <tr key={m.id}>
                  <td className="mono">{m.mac}</td>
                  <td>{m.vlan_id}</td>
                  <td>{m.owner_name ?? '—'}</td>
                  <td>{m.owner_vendor ?? '—'}</td>
                  <td><span className="badge badge-unknown">{m.collection_source}</span></td>
                  <td className="muted">{timeAgo(m.last_seen_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {entries.length > 5 && <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />}
        </>
      )}
    </div>
  )
}
