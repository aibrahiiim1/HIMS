import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type DeviceFact, type PhoneExtension } from '../api'

// PBX / call-manager template — the registered phone inventory pulled from
// Cisco CUCM over AXL (listPhone). Live-validation pending (needs a real CUCM
// publisher + AXL application user).
export function PbxDetail() {
  const { id } = useParams<{ id: string }>()
  const phones = useQuery({ queryKey: ['phones', id], queryFn: () => api.get<PhoneExtension[]>(`/devices/${id}/phones`) })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const count = (facts.data ?? []).find((f) => f.key === 'phone_count')?.value

  return (
    <div>
      <div className="card">
        <h2>Call Manager</h2>
        <dl className="kv">
          <div><dt>Registered phones</dt><dd>{count ?? (phones.data ? phones.data.length : '—')}</dd></div>
        </dl>
      </div>

      <div className="card">
        <h2>Phones</h2>
        {phones.data && phones.data.length === 0 && (
          <div className="muted">No phones collected. Bind an AXL application-user credential (http_basic) and collect.</div>
        )}
        {phones.data && phones.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Model</th><th>Description</th><th>Device Pool</th></tr></thead>
            <tbody>
              {phones.data.map((p) => (
                <tr key={p.id}>
                  <td>{p.name}</td>
                  <td>{p.model ?? '—'}</td>
                  <td>{p.description ?? '—'}</td>
                  <td>{p.device_pool ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
