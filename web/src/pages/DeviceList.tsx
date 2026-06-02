import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Device } from '../api'

interface Props {
  category: string
  title: string
  detailBase: string
}

export function DeviceList({ category, title, detailBase }: Props) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', category],
    queryFn: () => api.get<Device[]>(`/devices?category=${category}`),
  })

  return (
    <div>
      <div className="card">
        <h2>Inventory — {title}</h2>
        <p className="muted">
          Generic CMDB list. Any vendor of this category renders through the same
          template — adding a driver requires no UI change.
        </p>
      </div>
      <div className="card">
        {isLoading && <div className="loading">Loading {title.toLowerCase()}…</div>}
        {error && <div className="error-msg">Failed to load: {(error as Error).message}</div>}
        {data && data.length === 0 && (
          <div className="muted">No {title.toLowerCase()} discovered yet. Run a discovery scan.</div>
        )}
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
                  <td><Link to={`${detailBase}/${d.id}`}>{d.name}</Link></td>
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
