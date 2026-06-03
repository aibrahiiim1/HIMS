import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type ServerStorage, type DeviceFact, type DeviceRole, type Interface } from '../api'
import { DeviceOps } from '../components/DeviceOps'

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

  const factMap = new Map((facts.data ?? []).map((f) => [f.key, f.value ?? '']))

  return (
    <div>
      <div className="card">
        <h2>Server detail</h2>
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

      <DeviceOps deviceId={deviceId} />
    </div>
  )
}
