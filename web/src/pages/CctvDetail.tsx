import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Camera, Video, Film } from 'lucide-react'
import { api, type CameraInfo, type NVRChannel } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill } from '../components/ui'

const chStatus = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')

// Camera / NVR Intelligence (#16): device info + (for recorders) channel map.
// Deep fields (model/resolution/streams/channels) come from the ONVIF / vendor
// transport; reachability (RTSP 554 / HTTP) is monitored continuously.
export function CctvDetail() {
  const { id } = useParams<{ id: string }>()
  const cam = useQuery({ queryKey: ['camera', id], queryFn: () => api.get<CameraInfo>(`/devices/${id}/camera`) })
  const channels = useQuery({ queryKey: ['nvr-channels', id], queryFn: () => api.get<NVRChannel[]>(`/devices/${id}/nvr-channels`) })

  const c = cam.data
  const hasCam = !!(c && c.device_id)
  const ch = channels.data ?? []
  const chOnline = ch.filter((x) => x.status === 'online').length

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Camera} />

      <div className="kpi-grid">
        <Kpi label="Manufacturer" value={c?.manufacturer || '—'} icon={Camera} tone="info" />
        <Kpi label="Model" value={c?.model || '—'} icon={Video} />
        <Kpi label="Resolution" value={c?.resolution || '—'} icon={Film} />
        <Kpi label="Channels" value={ch.length || '—'} icon={Video} sub={ch.length ? `${chOnline} online` : undefined} />
      </div>

      <Panel title="Device Information" icon={Camera}>
        {hasCam ? (
          <DefList items={[
            { label: 'Manufacturer', value: c?.manufacturer || '—' },
            { label: 'Model', value: c?.model || '—' },
            { label: 'Resolution', value: c?.resolution || '—' },
            { label: 'RTSP stream', value: c?.rtsp_url ? <span className="mono">{c.rtsp_url}</span> : '—' },
            { label: 'ONVIF endpoint', value: c?.onvif_url ? <span className="mono">{c.onvif_url}</span> : '—' },
          ]} />
        ) : (
          <EmptyState icon={Camera} title="No ONVIF detail collected yet"
            message="Manufacturer / model / resolution / stream URLs populate from the ONVIF transport (bind an ONVIF or http_basic credential and collect). Reachability is already monitored." />
        )}
      </Panel>

      <Panel title="Channels" icon={Video} subtitle={ch.length ? `${chOnline}/${ch.length} online` : undefined} pad={false}>
        {channels.data && ch.length === 0 && <EmptyState icon={Video} title="No channels" message="Single camera, or the NVR channel map awaits ONVIF/vendor collection." />}
        {ch.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Channel</th><th>Camera</th><th>IP</th><th>Status</th></tr></thead>
            <tbody>
              {ch.map((x) => (
                <tr key={x.id}>
                  <td className="cell-name">{x.channel_no}</td>
                  <td>{x.camera_name ?? '—'}</td>
                  <td className="mono">{x.camera_ip ?? '—'}</td>
                  <td><StatusPill status={chStatus(x.status)} label={x.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
