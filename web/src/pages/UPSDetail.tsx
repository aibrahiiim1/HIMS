import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type UPSStatus } from '../api'

const battBadge = (s?: string) =>
  s === 'normal' ? 'up' : s === 'low' ? 'warning' : s === 'depleted' ? 'down' : 'unknown'

// UPS template — battery status, charge, runtime, output load (UPS-MIB).
export function UPSDetail() {
  const { id } = useParams<{ id: string }>()
  const ups = useQuery({ queryKey: ['ups', id], queryFn: () => api.get<UPSStatus>(`/devices/${id}/ups`) })
  const u = ups.data

  return (
    <div className="card">
      <h2>UPS
        {u?.battery_status && <span className={`badge badge-${battBadge(u.battery_status)}`} style={{ marginLeft: 8 }}>{u.battery_status}</span>}
      </h2>
      {!u?.device_id && <div className="muted">No UPS status yet. Bind an SNMP credential and collect (UPS-MIB).</div>}
      {u?.device_id && (
        <dl className="kv">
          <div><dt>Manufacturer</dt><dd>{u.manufacturer ?? '—'}</dd></div>
          <div><dt>Model</dt><dd>{u.model ?? '—'}</dd></div>
          <div><dt>Charge</dt><dd>{u.charge_pct != null ? `${u.charge_pct}%` : '—'}</dd></div>
          <div><dt>Runtime</dt><dd>{u.runtime_min != null ? `${u.runtime_min} min` : '—'}</dd></div>
          <div><dt>Output load</dt><dd>{u.load_pct != null ? `${u.load_pct}%` : '—'}</dd></div>
        </dl>
      )}
    </div>
  )
}
