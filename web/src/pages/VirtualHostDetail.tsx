import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { HardDrive, Cpu, MemoryStick, Server, Database, Network, Activity, Boxes } from 'lucide-react'
import { api, type VHDetail } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill, TabBar } from '../components/ui'

function fmtBytes(n?: number | null): string {
  if (!n) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i <= 1 ? 0 : 1)} ${u[i]}`
}
const powerBadge = (s: string) => (s === 'on' ? 'up' : s === 'off' ? 'down' : 'unknown')

// Virtual Host detail — ESXi or Hyper-V — with tabs for VMs, storage, networks,
// hardware and collection health. All data from /devices/{id}/virtualization.
export function VirtualHostDetail() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState('overview')
  const [vmq, setVmq] = useState('')
  const { data, isLoading } = useQuery({ queryKey: ['virt', id], queryFn: () => api.get<VHDetail>(`/devices/${id}/virtualization`) })
  const o = data?.overview ?? {}
  const isHyperV = o['hypervisor_type'] === 'hyperv'
  const num = (k: string) => { const v = Number(o[k]); return Number.isFinite(v) && o[k] != null ? v : null }
  const memTotal = num('memory.total_bytes'), memUsed = num('memory.used_bytes')
  const vms = data?.vms ?? []
  const filteredVMs = useMemo(() => {
    const t = vmq.trim().toLowerCase()
    if (!t) return vms
    return vms.filter((v) => v.name.toLowerCase().includes(t) || (v.ip ?? '').includes(t) || (v.guest_os ?? '').toLowerCase().includes(t) || (v.mac ?? '').toLowerCase().includes(t))
  }, [vms, vmq])

  const tabs = [
    { key: 'overview', label: 'Overview', icon: Server },
    { key: 'vms', label: 'VMs', icon: Boxes, count: vms.length },
    { key: 'storage', label: isHyperV ? 'Storage' : 'Datastores', icon: Database, count: isHyperV ? undefined : data?.datastores.length },
    { key: 'networks', label: 'Networks', icon: Network, count: data?.networks.length },
    { key: 'hardware', label: 'Hardware / NICs', icon: Cpu },
    { key: 'health', label: 'Collection Health', icon: Activity, count: data?.health.length },
    { key: 'ops', label: 'Operations', icon: HardDrive },
  ]

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={HardDrive} />
      {isLoading && <div className="loading">Loading…</div>}
      {data && (
        <>
        <div className="kpi-grid">
          <Kpi label="Type" value={isHyperV ? 'Hyper-V' : 'ESXi'} icon={Server} tone="info" />
          <Kpi label="VMs" value={`${o['running'] ?? 0}▶ / ${o['vm_count'] ?? 0}`} icon={Boxes} />
          <Kpi label="Memory" value={memTotal ? fmtBytes(memTotal) : '—'} icon={MemoryStick} sub={memUsed ? `${fmtBytes(memUsed)} used` : undefined} />
          <Kpi label={isHyperV ? 'vSwitches' : 'Datastores'} value={isHyperV ? (data.networks.length || '—') : (data.datastores.length || '—')} icon={Database} />
        </div>

        <TabBar tabs={tabs} active={tab} onChange={setTab} />

        {tab === 'overview' && (
          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="Host" icon={Server}>
              <DefList items={[
                { label: 'Type', value: isHyperV ? 'Hyper-V' : 'ESXi' },
                { label: 'Management IP', value: String(o['ip'] || '—') },
                { label: 'Hypervisor / OS version', value: String(o['version'] || '—') },
                { label: 'Vendor', value: String(o['vendor'] || '—') },
                { label: 'Model', value: String(o['model'] || '—') },
                { label: 'Management', value: String(o['management'] || '—') },
              ]} />
            </Panel>
            <Panel title="Capacity" icon={Cpu}>
              <DefList items={[
                { label: 'CPU', value: String(o['hardware.cpu_model'] || (o['cpu_cores'] ? `${o['cpu_cores']} cores` : '—')) },
                { label: 'CPU cores', value: String(o['hardware.cpu_cores'] || '—') },
                { label: 'Memory total', value: fmtBytes(memTotal) },
                { label: 'Memory used', value: fmtBytes(memUsed) },
                { label: 'VMs', value: `${o['running'] ?? 0} on / ${o['stopped'] ?? 0} off / ${o['vm_count'] ?? 0} total` },
                { label: isHyperV ? 'Virtual switches' : 'Datastores', value: isHyperV ? data.networks.length : `${data.datastores.length} · ${fmtBytes(data.datastores.reduce((a, d) => a + (d.capacity_bytes ?? 0), 0))}` },
              ]} />
            </Panel>
          </div>
        )}

        {tab === 'vms' && (
          <Panel title="Virtual Machines" subtitle={`${vms.length}`} pad={false}>
            <div style={{ padding: '8px 10px' }}>
              <input placeholder="Filter VMs by name / IP / OS / MAC…" value={vmq} onChange={(e) => setVmq(e.target.value)} style={{ padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13, width: 320 }} />
            </div>
            {vms.length === 0 && <EmptyState icon={Boxes} title="No VMs" message="This host reported no virtual machines." />}
            {vms.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Name</th><th>Power</th><th>Guest OS</th><th>IP</th><th>MAC</th><th>vCPU</th><th>Mem</th><th>Disks</th><th>NICs</th><th>Linked device</th></tr></thead>
                <tbody>
                  {filteredVMs.map((v) => (
                    <tr key={v.id}>
                      <td className="cell-name">{v.name}{v.generation && <small className="muted"> gen{v.generation}</small>}</td>
                      <td><StatusPill status={powerBadge(v.power_state)} label={v.power_state} /></td>
                      <td className="muted" style={{ fontSize: 12 }}>{v.guest_os || '—'}</td>
                      <td className="mono">{v.ip || '—'}</td>
                      <td className="mono" style={{ fontSize: 11 }}>{v.mac || '—'}</td>
                      <td>{v.vcpu || '—'}</td>
                      <td>{v.mem_mb ? `${(v.mem_mb / 1024).toFixed(0)} GB` : '—'}</td>
                      <td title={fmtBytes(v.disk_total_bytes)}>{v.disk_count || '—'}{v.disk_total_bytes ? <small className="muted"> {fmtBytes(v.disk_total_bytes)}</small> : null}</td>
                      <td>{v.nic_count || '—'}</td>
                      <td>{v.linked_device_id
                        ? <Link to={`/devices/${v.linked_device_id}`} className="cell-name">{v.linked_ip || v.linked_name}</Link>
                        : <span className="muted" style={{ fontSize: 11 }}>not discovered</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        )}

        {tab === 'storage' && (
          <Panel title={isHyperV ? 'VM disks (VHD/VHDX)' : 'Datastores'} pad={false}>
            {!isHyperV && data.datastores.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Datastore</th><th>Type</th><th>Capacity</th><th>Free</th><th>Used</th></tr></thead>
                <tbody>
                  {data.datastores.map((d) => (
                    <tr key={d.id}>
                      <td className="cell-name">{d.name}</td>
                      <td className="muted">{d.type || '—'}</td>
                      <td className="mono">{fmtBytes(d.capacity_bytes)}</td>
                      <td className="mono">{fmtBytes(d.free_bytes)}</td>
                      <td className="mono">{fmtBytes((d.capacity_bytes ?? 0) - (d.free_bytes ?? 0))}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            {isHyperV && (
              <table className="data-table">
                <thead><tr><th>VM</th><th>Disk path</th><th>Capacity</th><th>Used</th></tr></thead>
                <tbody>
                  {vms.flatMap((v) => v.disks.map((dk, i) => (
                    <tr key={v.id + i}>
                      <td className="cell-name">{i === 0 ? v.name : ''}</td>
                      <td className="mono" style={{ fontSize: 11 }}>{dk.path || dk.label}</td>
                      <td className="mono">{fmtBytes(dk.capacity_bytes)}</td>
                      <td className="mono">{fmtBytes(dk.used_bytes)}</td>
                    </tr>
                  )))}
                </tbody>
              </table>
            )}
            {!isHyperV && data.datastores.length === 0 && <EmptyState icon={Database} title="No datastores" message="No datastores collected for this host." />}
          </Panel>
        )}

        {tab === 'networks' && (
          <Panel title="Virtual networks" subtitle={`${data.networks.length}`} pad={false}>
            {data.networks.length === 0 && <EmptyState icon={Network} title="No networks" message="No virtual switches / port groups collected." />}
            {data.networks.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Name</th><th>Kind</th><th>VLAN</th><th>vSwitch / uplinks</th></tr></thead>
                <tbody>
                  {data.networks.map((n) => (
                    <tr key={n.id}>
                      <td className="cell-name">{n.name}</td>
                      <td className="muted">{n.kind}</td>
                      <td>{n.vlan ?? '—'}</td>
                      <td className="muted" style={{ fontSize: 12 }}>{n.switch_name || n.uplinks || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        )}

        {tab === 'hardware' && (
          <Panel title={isHyperV ? 'Host NICs (OS inventory)' : 'Host NICs (vmnics)'} pad={false}>
            {(() => {
              const nics = isHyperV
                ? data.os_nics.map((n) => ({ id: n.id, name: n.name, mac: n.mac, speed: undefined as number | undefined }))
                : data.host_nics.map((n) => ({ id: n.id, name: n.name, mac: n.mac, speed: n.link_speed_mbps }))
              if (nics.length === 0) return <EmptyState icon={Cpu} title="No host NICs" message="No physical NICs collected for this host." />
              return (
                <table className="data-table">
                  <thead><tr><th>NIC</th><th>MAC</th><th>Speed</th></tr></thead>
                  <tbody>{nics.map((n) => (
                    <tr key={n.id}><td className="cell-name">{n.name}</td><td className="mono">{n.mac || '—'}</td><td className="muted">{n.speed ? `${n.speed} Mb` : '—'}</td></tr>
                  ))}</tbody>
                </table>
              )
            })()}
          </Panel>
        )}

        {tab === 'health' && (
          <Panel title="Collection health" pad={false}>
            {data.health.length === 0 && <EmptyState icon={Activity} title="No collection recorded" message="No virtualization collection has run for this host yet." />}
            {data.health.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Collector</th><th>Status</th><th>VMs</th><th>Detail</th><th>Last collected</th></tr></thead>
                <tbody>
                  {data.health.map((h, i) => (
                    <tr key={i}>
                      <td className="cell-name">{h.collector}</td>
                      <td><span className={'badge ' + (h.status === 'ok' ? 'badge-up' : h.status === 'failed' ? 'badge-crit' : 'badge-warn')}>{h.status}</span></td>
                      <td>{h.vm_count ?? '—'}</td>
                      <td className="muted" style={{ fontSize: 12 }}>{h.detail || '—'}</td>
                      <td className="muted" style={{ fontSize: 11 }}>{h.collected_at ? new Date(h.collected_at).toLocaleString() : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        )}

        {tab === 'ops' && <DeviceOps deviceId={id ?? ''} />}
        </>
      )}
    </div>
  )
}
