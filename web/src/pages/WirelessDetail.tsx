import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Wifi, Router, Users, Radio } from 'lucide-react'
import { api, type AccessPoint, type WLANControllerInfo } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill } from '../components/ui'

const apStatus = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')

// Wireless Intelligence (#13): controller summary + AP inventory. The AP list
// and client counts come from the vendor REST transport (UniFi/Omada/Ruckus/
// Extreme); controller reachability is monitored continuously.
export function WirelessDetail() {
  const { id } = useParams<{ id: string }>()
  const info = useQuery({ queryKey: ['wlan', id], queryFn: () => api.get<WLANControllerInfo>(`/devices/${id}/wlan`) })
  const aps = useQuery({ queryKey: ['aps', id], queryFn: () => api.get<AccessPoint[]>(`/devices/${id}/access-points`) })

  const w = info.data
  const list = aps.data ?? []
  const online = list.filter((a) => a.status === 'online').length
  const clients = list.reduce((a, x) => a + (x.client_count ?? 0), 0) || w?.client_count || 0

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Wifi} />

      <div className="kpi-grid">
        <Kpi label="Vendor" value={w?.vendor || '—'} icon={Router} tone="info" sub={w?.version || undefined} />
        <Kpi label="Access Points" value={list.length || w?.ap_count || '—'} icon={Radio} sub={list.length ? `${online} online` : undefined} />
        <Kpi label="Clients" value={clients || '—'} icon={Users} />
        <Kpi label="Online APs" value={list.length ? `${online}/${list.length}` : '—'} icon={Radio} tone={list.length && online < list.length ? 'warn' : 'default'} />
      </div>

      <Panel title="Controller" icon={Router}>
        {w && (w.vendor || w.version || w.ap_count != null)
          ? <DefList items={[
              { label: 'Vendor', value: w.vendor || '—' },
              { label: 'Version', value: w.version || '—' },
              { label: 'Access points', value: w.ap_count ?? '—' },
              { label: 'Clients', value: w.client_count ?? '—' },
            ]} />
          : <EmptyState icon={Router} title="No controller detail collected yet" message="Vendor/version/AP counts populate from the wireless REST transport (bind an http_basic / vendor_api credential and collect)." />}
      </Panel>

      <Panel title="Access Points" icon={Radio} subtitle={list.length ? `${online}/${list.length} online` : undefined} pad={false}>
        {aps.data && list.length === 0 && (
          <EmptyState icon={Radio} title="No AP inventory yet"
            message="Per-AP detail (model, clients, SSIDs, signal) comes from the vendor REST transport (UniFi/Omada/Ruckus/Extreme). Controller reachability is monitored today." />
        )}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Model</th><th>IP</th><th>MAC</th><th>Clients</th><th>Status</th></tr></thead>
            <tbody>
              {list.map((a) => (
                <tr key={a.id}>
                  <td className="cell-name">{a.name}</td>
                  <td>{a.model ?? '—'}</td>
                  <td className="mono">{a.ip ?? '—'}</td>
                  <td className="mono">{a.mac ?? '—'}</td>
                  <td>{a.client_count}</td>
                  <td><StatusPill status={apStatus(a.status)} label={a.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
