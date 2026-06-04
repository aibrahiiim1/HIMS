import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Phone, PhoneCall, Server } from 'lucide-react'
import { api, type DeviceFact, type PhoneExtension } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, EmptyState } from '../components/ui'

// Voice / PBX Intelligence (#17): registered-phone inventory from Cisco CUCM
// over AXL (listPhone). Bind an AXL application-user (http_basic) credential
// and collect to populate; reachability is monitored continuously.
export function PbxDetail() {
  const { id } = useParams<{ id: string }>()
  const phones = useQuery({ queryKey: ['phones', id], queryFn: () => api.get<PhoneExtension[]>(`/devices/${id}/phones`) })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })

  const list = phones.data ?? []
  const f = new Map((facts.data ?? []).map((x) => [x.key, x.value ?? '']))
  const registered = f.get('phone_count') ?? (phones.data ? String(list.length) : '—')
  const pools = new Set(list.map((p) => p.device_pool).filter(Boolean)).size
  const models = new Set(list.map((p) => p.model).filter(Boolean)).size

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Phone} />

      <div className="kpi-grid">
        <Kpi label="Registered Phones" value={registered} icon={PhoneCall} tone="info" />
        <Kpi label="Device Pools" value={pools || '—'} icon={Server} />
        <Kpi label="Phone Models" value={models || '—'} icon={Phone} />
      </div>

      <Panel title="Phones" icon={PhoneCall} subtitle={list.length ? `${list.length}` : undefined} pad={false}>
        {phones.data && list.length === 0 && (
          <EmptyState icon={PhoneCall} title="No phones collected"
            message="The phone registry is pulled from Cisco CUCM over AXL (listPhone). Bind an AXL application-user (http_basic) credential and collect to populate." />
        )}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Model</th><th>Description</th><th>Device Pool</th></tr></thead>
            <tbody>
              {list.map((p) => (
                <tr key={p.id}>
                  <td className="cell-name">{p.name}</td>
                  <td>{p.model ?? '—'}</td>
                  <td>{p.description ?? '—'}</td>
                  <td>{p.device_pool ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
