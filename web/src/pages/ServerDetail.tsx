import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Server } from 'lucide-react'
import { api, type ServerStorage, type DeviceFact, type DeviceRole, type Interface, type BMCInfo, type BMCSensor } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { DeepOSInventory } from '../components/DeepOSInventory'

const healthBadge = (h?: string | null) =>
  h === 'OK' ? 'up' : h === 'Critical' ? 'down' : h === 'Warning' ? 'warning' : 'unknown'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

// The server template — HOST-RESOURCES-MIB collected data: resource facts,
// storage volumes, roles, and network interfaces. Vendor-agnostic.
export function ServerDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const roles = useQuery({ queryKey: ['roles', id], queryFn: () => api.get<DeviceRole[]>(`/devices/${id}/roles`) })
  const storage = useQuery({ queryKey: ['storage', id], queryFn: () => api.get<ServerStorage[]>(`/devices/${id}/storage`) })
  const ifaces = useQuery({ queryKey: ['interfaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })
  const bmc = useQuery({ queryKey: ['bmc', id], queryFn: () => api.get<BMCInfo>(`/devices/${id}/bmc`) })
  const sensors = useQuery({ queryKey: ['bmc-sensors', id], queryFn: () => api.get<BMCSensor[]>(`/devices/${id}/bmc-sensors`) })

  const factMap = new Map((facts.data ?? []).map((f) => [f.key, f.value ?? '']))
  const hasBMC = bmc.data && bmc.data.device_id

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Server} />
      <DeepOSInventory deviceId={deviceId} />
      <div className="card">
        <h2>Resource Summary</h2>
        {roles.data && roles.data.length > 0 && (
          <div style={{ marginBottom: 12 }}>
            <span className="muted">Roles: </span>
            {roles.data.map((r) => <span key={r.role} className="badge badge-access" style={{ marginRight: 6 }}>{r.role}</span>)}
          </div>
        )}
        <dl className="kv">
          <div><dt>CPU load</dt><dd>{factMap.has('cpu.load_pct') ? `${factMap.get('cpu.load_pct')}%` : '—'}</dd></div>
          <div><dt>Memory total</dt><dd>{fmtBytes(Number(factMap.get('memory.total_bytes')) || null)}</dd></div>
          <div><dt>Memory used</dt><dd>{fmtBytes(Number(factMap.get('memory.used_bytes')) || null)}</dd></div>
          <div><dt>Uptime (cs)</dt><dd>{factMap.get('hardware.uptime_centisec') ?? '—'}</dd></div>
        </dl>
      </div>

      <div className="card">
        <h2>Storage</h2>
        {storage.isLoading && <div className="loading">Loading…</div>}
        {storage.data && storage.data.length === 0 && <div className="muted">No storage collected.</div>}
        {storage.data && storage.data.length > 0 && (
          <table>
            <thead><tr><th>Volume</th><th>Type</th><th>Total</th><th>Used</th><th>Used %</th></tr></thead>
            <tbody>
              {storage.data.map((s) => {
                const pct = s.total_bytes && s.used_bytes ? Math.round((s.used_bytes / s.total_bytes) * 100) : null
                return (
                  <tr key={s.id}>
                    <td>{s.descr ?? '—'}</td>
                    <td><span className="badge badge-unknown">{s.storage_type}</span></td>
                    <td>{fmtBytes(s.total_bytes)}</td>
                    <td>{fmtBytes(s.used_bytes)}</td>
                    <td>{pct != null ? `${pct}%` : '—'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h2>Interfaces</h2>
        {ifaces.data && ifaces.data.length === 0 && <div className="muted">No interfaces collected.</div>}
        {ifaces.data && ifaces.data.length > 0 && (
          <table>
            <thead><tr><th>Index</th><th>Name</th><th>MAC</th><th>Speed</th></tr></thead>
            <tbody>
              {ifaces.data.map((i) => (
                <tr key={i.id}>
                  <td>{i.if_index}</td>
                  <td>{i.if_name ?? i.if_descr ?? '—'}</td>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{i.mac ?? '—'}</td>
                  <td>{i.speed_mbps ? `${i.speed_mbps} Mb` : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {hasBMC && (
        <div className="card">
          <h2>BMC / hardware health
            <span className={`badge badge-${healthBadge(bmc.data?.health)}`} style={{ marginLeft: 8 }}>{bmc.data?.health ?? 'unknown'}</span>
          </h2>
          <dl className="kv">
            <div><dt>Controller</dt><dd>{bmc.data?.vendor ?? '—'} {bmc.data?.controller_kind ?? ''}</dd></div>
            <div><dt>Model</dt><dd>{bmc.data?.model ?? '—'}</dd></div>
            <div><dt>Serial</dt><dd>{bmc.data?.serial ?? '—'}</dd></div>
            <div><dt>Firmware</dt><dd>{bmc.data?.firmware_version ?? '—'}</dd></div>
            <div><dt>Power</dt><dd>{bmc.data?.power_state ?? '—'}</dd></div>
          </dl>
          {sensors.data && sensors.data.length > 0 && (
            <table>
              <thead><tr><th>Kind</th><th>Name</th><th>Status</th><th>Reading</th></tr></thead>
              <tbody>
                {sensors.data.map((s) => (
                  <tr key={s.id}>
                    <td>{s.kind}</td>
                    <td>{s.name}</td>
                    <td><span className={`badge badge-${healthBadge(s.status)}`}>{s.status ?? '—'}</span></td>
                    <td>{s.has_reading ? `${s.reading} ${s.unit ?? ''}` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <DeviceOps deviceId={deviceId} />
    </div>
  )
}
