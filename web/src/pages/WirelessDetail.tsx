import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type AccessPoint, type WLANControllerInfo } from '../api'

const apBadge = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')

// Wireless controller template — controller summary + AP inventory. The AP
// list and client counts are populated by the vendor-REST transport
// (deferred); reachability of the controller is monitored today.
export function WirelessDetail() {
  const { id } = useParams<{ id: string }>()
  const info = useQuery({ queryKey: ['wlan', id], queryFn: () => api.get<WLANControllerInfo>(`/devices/${id}/wlan`) })
  const aps = useQuery({ queryKey: ['aps', id], queryFn: () => api.get<AccessPoint[]>(`/devices/${id}/access-points`) })

  return (
    <div>
      <div className="card">
        <h2>Wireless controller</h2>
        <dl className="kv">
          <div><dt>Vendor</dt><dd>{info.data?.vendor ?? '—'}</dd></div>
          <div><dt>Version</dt><dd>{info.data?.version ?? '—'}</dd></div>
          <div><dt>APs</dt><dd>{info.data?.ap_count ?? '—'}</dd></div>
          <div><dt>Clients</dt><dd>{info.data?.client_count ?? '—'}</dd></div>
        </dl>
      </div>

      <div className="card">
        <h2>Access points</h2>
        {aps.data && aps.data.length === 0 && (
          <div className="muted">
            No AP inventory yet. Per-AP detail (model, clients, SSIDs) is populated by the vendor
            REST transport (UniFi/Omada/Ruckus) — deferred. Controller reachability is monitored
            today.
          </div>
        )}
        {aps.data && aps.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Model</th><th>IP</th><th>MAC</th><th>Clients</th><th>Status</th></tr></thead>
            <tbody>
              {aps.data.map((a) => (
                <tr key={a.id}>
                  <td><strong>{a.name}</strong></td>
                  <td>{a.model ?? '—'}</td>
                  <td>{a.ip ?? '—'}</td>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{a.mac ?? '—'}</td>
                  <td>{a.client_count}</td>
                  <td><span className={`badge badge-${apBadge(a.status)}`}>{a.status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
