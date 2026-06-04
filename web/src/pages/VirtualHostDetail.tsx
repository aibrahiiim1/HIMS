import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { HardDrive, Cpu, MemoryStick, Server, Database } from 'lucide-react'
import { api, type DeviceFact, type ServerStorage, type VirtualMachine } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill, Meter } from '../components/ui'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
function fmtUptime(centisec?: number | null): string {
  if (!centisec) return '—'
  const s = Math.floor(centisec / 100)
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}
const powerBadge = (s: string) => (s === 'on' ? 'up' : s === 'off' ? 'down' : 'unknown')

// Server / Virtualization Intelligence (#15): host CPU/memory/disk + uptime,
// datastores with usage, and the VM inventory. Host metrics are SNMP/HOST-
// RESOURCES; VM detail comes from the vSphere/Hyper-V transport when collected.
export function VirtualHostDetail() {
  const { id } = useParams<{ id: string }>()
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const storage = useQuery({ queryKey: ['storage', id], queryFn: () => api.get<ServerStorage[]>(`/devices/${id}/storage`) })
  const vms = useQuery({ queryKey: ['vms', id], queryFn: () => api.get<VirtualMachine[]>(`/devices/${id}/vms`) })
  const f = new Map((facts.data ?? []).map((x) => [x.key, x.value ?? '']))
  const num = (k: string) => { const v = Number(f.get(k)); return Number.isFinite(v) && f.has(k) ? v : null }

  const cpu = num('cpu.load_pct')
  const memTotal = num('memory.total_bytes'), memUsed = num('memory.used_bytes')
  const memPct = num('memory.used_pct') ?? (memTotal && memUsed ? Math.round((memUsed / memTotal) * 100) : null)
  const diskPct = num('disk.used_pct')
  const ds = storage.data ?? []
  const dsTotal = ds.reduce((a, s) => a + (s.total_bytes ?? 0), 0)
  const dsUsed = ds.reduce((a, s) => a + (s.used_bytes ?? 0), 0)
  const vmList = vms.data ?? []
  const vmsOn = vmList.filter((v) => v.power_state === 'on').length

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={HardDrive} />

      <div className="kpi-grid">
        <Kpi label="Hypervisor" value={f.get('hypervisor.type') || '—'} icon={Server} tone="info" />
        <Kpi label="CPU load" value={cpu != null ? `${cpu}%` : '—'} icon={Cpu} tone={cpu != null && cpu >= 85 ? 'crit' : cpu != null && cpu >= 65 ? 'warn' : 'default'} />
        <Kpi label="Memory used" value={memPct != null ? `${memPct}%` : '—'} icon={MemoryStick} sub={memTotal ? fmtBytes(memTotal) : undefined} />
        <Kpi label="Datastores" value={ds.length || '—'} icon={Database} sub={dsTotal ? `${fmtBytes(dsUsed)} / ${fmtBytes(dsTotal)}` : undefined} />
      </div>

      <div className="grid-2" style={{ alignItems: 'start' }}>
        <Panel title="Host" icon={Server}>
          <DefList items={[
            { label: 'Hypervisor', value: f.get('hypervisor.type') || '—' },
            { label: 'Uptime', value: fmtUptime(num('hardware.uptime_centisec')) },
            { label: 'Memory total', value: fmtBytes(memTotal) },
            { label: 'Memory used', value: fmtBytes(memUsed) },
            { label: 'VMs', value: vmList.length ? `${vmsOn} on / ${vmList.length}` : '—' },
          ]} />
        </Panel>
        <Panel title="Resources" icon={Cpu}>
          {(cpu == null && memPct == null && diskPct == null)
            ? <div className="muted">CPU/memory/disk not collected for this host.</div>
            : (
              <div className="stack" style={{ gap: 12 }}>
                {cpu != null && <Meter label="CPU" value={cpu} />}
                {memPct != null && <Meter label="Memory" value={memPct} />}
                {diskPct != null && <Meter label="Disk" value={diskPct} />}
              </div>
            )}
        </Panel>
      </div>

      <Panel title="Datastores" icon={Database} subtitle={ds.length ? `${ds.length}` : undefined} pad={false}>
        {storage.data && ds.length === 0 && <EmptyState icon={Database} title="No datastores collected" message="Datastore capacity is collected via SNMP HOST-RESOURCES or the vSphere API." />}
        {ds.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Datastore</th><th>Type</th><th>Usage</th><th>Used / Total</th></tr></thead>
            <tbody>
              {ds.map((s) => {
                const pct = s.total_bytes && s.used_bytes != null ? Math.round((s.used_bytes / s.total_bytes) * 100) : null
                return (
                  <tr key={s.id}>
                    <td className="cell-name">{s.descr ?? '—'}</td>
                    <td className="muted">{s.storage_type ?? '—'}</td>
                    <td style={{ minWidth: 160 }}>{pct != null ? <Meter value={pct} /> : '—'}</td>
                    <td className="mono">{fmtBytes(s.used_bytes)} / {fmtBytes(s.total_bytes)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Virtual Machines" icon={HardDrive} subtitle={vmList.length ? `${vmsOn} on / ${vmList.length}` : undefined} pad={false}>
        {vms.data && vmList.length === 0 && (
          <EmptyState icon={HardDrive} title="No VM inventory yet" message="Per-VM enumeration (power state, vCPU, guest OS) is collected via the vSphere / Hyper-V API transport. Host-level resources above are collected via SNMP." />
        )}
        {vmList.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Power</th><th>vCPU</th><th>Memory</th><th>Guest OS</th><th>IP</th></tr></thead>
            <tbody>
              {vmList.map((v) => (
                <tr key={v.id}>
                  <td className="cell-name">{v.name}</td>
                  <td><StatusPill status={powerBadge(v.power_state)} label={v.power_state} /></td>
                  <td>{v.vcpu ?? '—'}</td>
                  <td>{v.mem_mb ? `${v.mem_mb} MB` : '—'}</td>
                  <td>{v.guest_os ?? '—'}</td>
                  <td className="mono">{v.primary_ip ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <DeviceOps deviceId={id ?? ''} />
    </div>
  )
}
