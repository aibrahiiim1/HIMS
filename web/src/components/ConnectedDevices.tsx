import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Cable, ChevronDown, ChevronRight } from 'lucide-react'
import { api, type PortMapReport, type PortMapPort } from '../api'
import { Panel, Kpi, EmptyState, timeAgo } from './ui'

// ConnectedDevices — per-switch resolved port → device/VM view. Shows what is attached to each
// connected port: learned MACs resolved to inventory devices / VMs / IPs, with a per-port
// confidence. Honest: unknown MACs are counted (never invented as devices); trunk/uplink ports are
// flagged, not guessed; edge/access ports are the reliable attachment points.
const CONF: Record<string, { label: string; cls: string }> = {
  lldp_cdp: { label: 'LLDP/CDP', cls: 'badge-up' },
  access_mac: { label: 'access (edge MAC)', cls: 'badge-up' },
  arp_mac: { label: 'ARP+MAC', cls: 'badge-warn' },
  trunk_uplink: { label: 'trunk/uplink', cls: 'badge-unknown' },
  ambiguous: { label: 'ambiguous', cls: 'badge-warn' },
  none: { label: '—', cls: 'badge-unknown' },
}
const oper = (s?: number) => (s === 1 ? 'up' : s === 2 ? 'down' : '—')

export function ConnectedDevices({ deviceId }: { deviceId: string }) {
  const q = useQuery({ queryKey: ['port-map', deviceId], queryFn: () => api.get<PortMapReport>(`/devices/${deviceId}/port-map`) })
  const [open, setOpen] = useState<Set<number>>(new Set())
  const [hideTrunk, setHideTrunk] = useState(false)
  const d = q.data
  if (q.isLoading) return <Panel title="Connected Devices"><div className="loading">Resolving port → device map…</div></Panel>
  if (!d) return <Panel title="Connected Devices"><EmptyState icon={Cable} title="No data" message="Could not resolve the port map." /></Panel>
  if (d.ports.length === 0) return <Panel title="Connected Devices"><EmptyState icon={Cable} title="No connected ports" message="No learned MACs/neighbors on this switch — collect SNMP FDB/LLDP to populate." /></Panel>

  const ports = hideTrunk ? d.ports.filter((p) => p.confidence !== 'trunk_uplink') : d.ports
  const primary = (p: PortMapPort) => p.macs.find((m) => m.device_id || m.vm_id)

  return (
    <>
      <div className="kpi-grid">
        <Kpi label="Connected ports" value={`${d.connected_ports}/${d.total_ports}`} icon={Cable} tone="info" />
        <Kpi label="Resolved device ports" value={d.summary.resolved_device_ports} tone="ok" />
        <Kpi label="Trunk / uplink ports" value={d.summary.trunk_uplink_ports} />
        <Kpi label="Ambiguous ports" value={d.summary.ambiguous_ports} tone={d.summary.ambiguous_ports ? 'warn' : 'default'} />
        <Kpi label="Unknown MACs" value={d.summary.unknown_macs} tone={d.summary.unknown_macs ? 'warn' : 'default'} sub="not in inventory" />
      </div>
      <Panel title="Port → device resolution" subtitle={`${ports.length}`} pad={false}
        actions={<label style={{ fontSize: 12, display: 'flex', gap: 4, alignItems: 'center' }}><input type="checkbox" checked={hideTrunk} onChange={(e) => setHideTrunk(e.target.checked)} /> hide trunk/uplink</label>}>
        <table className="data-table">
          <thead><tr><th style={{ width: 22 }}></th><th>Port</th><th>Status</th><th>VLAN</th><th>MACs</th><th>Resolved device / VM</th><th>Confidence</th><th>Unknown</th></tr></thead>
          <tbody>
            {ports.map((p) => {
              const m = primary(p)
              const exp = open.has(p.if_index)
              return (
                <>
                  <tr key={p.if_index} style={{ cursor: p.mac_count > 0 ? 'pointer' : 'default' }} onClick={() => setOpen((s) => { const n = new Set(s); if (n.has(p.if_index)) n.delete(p.if_index); else n.add(p.if_index); return n })}>
                    <td>{p.mac_count > 1 && (exp ? <ChevronDown size={13} /> : <ChevronRight size={13} />)}</td>
                    <td className="cell-name">{p.if_name || `#${p.if_index}`}{p.speed_mbps ? <small className="muted"> · {p.speed_mbps >= 1000 ? `${p.speed_mbps / 1000}G` : `${p.speed_mbps}M`}</small> : null}</td>
                    <td className="muted">{oper(p.oper_status)}</td>
                    <td>{p.is_trunk ? <span className="badge badge-unknown">trunk{p.tagged_vlans?.length ? ` ${p.tagged_vlans.length}` : ''}</span> : (p.untagged_vlan ?? '—')}</td>
                    <td className="muted">{p.mac_count}</td>
                    <td className="cell-name">{m
                      ? (m.vm_id && !m.device_id
                        ? <Link to={m.vm_host_device_id ? `/virtual-hosts/${m.vm_host_device_id}` : '#'}>{m.vm_name} <small className="muted">(VM)</small></Link>
                        : <Link to={`/devices/${m.device_id}`}>{m.device_name || m.ip}</Link>)
                      : <span className="muted">{p.neighbor ? `${p.neighbor} (neighbor)` : '—'}</span>}
                      {m && <small className="muted"> {m.category}{m.server_role ? `/${m.server_role.replace(/_/g, ' ')}` : ''}{m.ip ? ` · ${m.ip}` : ''}</small>}
                      {p.resolved_count > 1 && <small className="muted"> +{p.resolved_count - 1} more</small>}</td>
                    <td><span className={'badge ' + (CONF[p.confidence]?.cls || 'badge-unknown')} title={`via ${m?.method ?? 'n/a'}`}>{CONF[p.confidence]?.label || p.confidence}</span></td>
                    <td className="muted">{p.unknown_count || '—'}</td>
                  </tr>
                  {exp && p.macs.map((mm, i) => (
                    <tr key={p.if_index + ':' + i} style={{ background: 'var(--surface-2)' }}>
                      <td></td>
                      <td className="mono" style={{ fontSize: 11 }}>{mm.mac}</td>
                      <td colSpan={2} className="muted" style={{ fontSize: 11 }}>{mm.ip || '—'}</td>
                      <td colSpan={2} className="cell-name">{mm.device_id
                        ? <Link to={`/devices/${mm.device_id}`}>{mm.device_name}</Link>
                        : mm.vm_id ? <span>{mm.vm_name} <small className="muted">(VM)</small></span>
                        : <span className="muted">unknown</span>}
                        {(mm.category || mm.server_role) && <small className="muted"> {mm.category}{mm.server_role ? `/${mm.server_role.replace(/_/g, ' ')}` : ''}</small>}</td>
                      <td className="muted" style={{ fontSize: 11 }}>{mm.method}</td>
                      <td className="muted" style={{ fontSize: 11 }}>{mm.last_seen ? timeAgo(mm.last_seen) : ''}</td>
                    </tr>
                  ))}
                </>
              )
            })}
          </tbody>
        </table>
      </Panel>
    </>
  )
}
