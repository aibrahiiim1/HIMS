import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Device } from '../api'

const CATEGORIES = ['switch', 'router', 'firewall', 'server', 'access_point', 'camera', 'nvr', 'printer']

export function DeviceList() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', 'switch'],
    queryFn: () => api.get<Device[]>('/devices?category=switch'),
  })

  return (
    <div>
      <div className="card">
        <h2>Inventory — Switches</h2>
        <p className="muted">
          Categories: {CATEGORIES.join(' · ')} (switch shown; more device types land in later phases)
        </p>
      </div>
      <div className="card">
        {isLoading && <div className="loading">Loading devices…</div>}
        {error && <div className="error-msg">Failed to load: {(error as Error).message}</div>}
        {data && data.length === 0 && <div className="muted">No switches discovered yet. Run a discovery scan.</div>}
        {data && data.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Name</th><th>IP</th><th>Vendor</th><th>Model</th>
                <th>OS</th><th>Driver</th><th>Status</th>
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.id}>
                  <td><Link to={`/devices/${d.id}`}>{d.name}</Link></td>
                  <td>{d.primary_ip ?? '—'}</td>
                  <td>{d.vendor ?? '—'}</td>
                  <td>{d.model ?? '—'}</td>
                  <td>{d.os_version ?? '—'}</td>
                  <td>{d.driver ?? '—'}</td>
                  <td><span className={`badge badge-${d.status}`}>{d.status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
