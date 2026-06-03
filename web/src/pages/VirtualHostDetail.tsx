import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type DeviceFact, type ServerStorage, type VirtualMachine } from '../api'
import { DeviceOps } from '../components/DeviceOps'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

const powerBadge = (s: string) => (s === 'on' ? 'up' : s === 'off' ? 'down' : 'unknown')

// The virtual_host template: hypervisor host resources (via SNMP) + the VM
// inventory section. Per-VM detail is populated by the vSphere/Hyper-V API
// transport (deferred); until then the section explains that.
export function VirtualHostDetail() {
  const { id } = useParams<{ id: string }>()
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const storage = useQuery({ queryKey: ['storage', id], queryFn: () => api.get<ServerStorage[]>(`/devices/${id}/storage`) })
  const vms = useQuery({ queryKey: ['vms', id], queryFn: () => api.get<VirtualMachine[]>(`/devices/${id}/vms`) })
  const factMap = new Map((facts.data ?? []).map((f) => [f.key, f.value ?? '']))

  return (
    <div>
      <div className="card">
        <h2>Virtualization host</h2>
        <dl className="kv">
          <div><dt>Hypervisor</dt><dd>{factMap.get('hypervisor.type') ?? '—'}</dd></div>
          <div><dt>CPU load</dt><dd>{factMap.has('cpu.load_pct') ? `${factMap.get('cpu.load_pct')}%` : '—'}</dd></div>
          <div><dt>Memory total</dt><dd>{fmtBytes(Number(factMap.get('memory.total_bytes')) || null)}</dd></div>
          <div><dt>Memory used</dt><dd>{fmtBytes(Number(factMap.get('memory.used_bytes')) || null)}</dd></div>
        </dl>
      </div>

      <div className="card">
        <h2>Datastores</h2>
        {storage.data && storage.data.length === 0 && <div className="muted">No datastores collected.</div>}
        {storage.data && storage.data.length > 0 && (
          <table>
            <thead><tr><th>Datastore</th><th>Total</th><th>Used</th></tr></thead>
            <tbody>
              {storage.data.map((s) => (
                <tr key={s.id}><td>{s.descr ?? '—'}</td><td>{fmtBytes(s.total_bytes)}</td><td>{fmtBytes(s.used_bytes)}</td></tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h2>Virtual machines</h2>
        {vms.data && vms.data.length === 0 && (
          <div className="muted">
            No VM inventory yet. Per-VM enumeration (power state, vCPU, guest OS) requires the
            vSphere / Hyper-V API transport — deferred to a follow-up. Host-level resources above
            are collected via SNMP today.
          </div>
        )}
        {vms.data && vms.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Power</th><th>vCPU</th><th>Memory</th><th>Guest OS</th><th>IP</th></tr></thead>
            <tbody>
              {vms.data.map((v) => (
                <tr key={v.id}>
                  <td><strong>{v.name}</strong></td>
                  <td><span className={`badge badge-${powerBadge(v.power_state)}`}>{v.power_state}</span></td>
                  <td>{v.vcpu ?? '—'}</td>
                  <td>{v.mem_mb ? `${v.mem_mb} MB` : '—'}</td>
                  <td>{v.guest_os ?? '—'}</td>
                  <td>{v.primary_ip ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <DeviceOps deviceId={id ?? ''} />
    </div>
  )
}
