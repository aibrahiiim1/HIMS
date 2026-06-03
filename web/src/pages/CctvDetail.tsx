import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type CameraInfo, type NVRChannel } from '../api'

const chBadge = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')

// CCTV detail template — shared by cameras and NVR/DVRs. Shows per-camera
// info and (for recorders) the channel list. Deep fields are populated by the
// ONVIF/vendor-REST transport (deferred); reachability is monitored today.
export function CctvDetail() {
  const { id } = useParams<{ id: string }>()
  const cam = useQuery({ queryKey: ['camera', id], queryFn: () => api.get<CameraInfo>(`/devices/${id}/camera`) })
  const channels = useQuery({ queryKey: ['nvr-channels', id], queryFn: () => api.get<NVRChannel[]>(`/devices/${id}/nvr-channels`) })

  const hasCam = cam.data && cam.data.device_id
  const hasChannels = channels.data && channels.data.length > 0

  return (
    <div>
      <div className="card">
        <h2>CCTV device</h2>
        {hasCam ? (
          <dl className="kv">
            <div><dt>Manufacturer</dt><dd>{cam.data?.manufacturer ?? '—'}</dd></div>
            <div><dt>Model</dt><dd>{cam.data?.model ?? '—'}</dd></div>
            <div><dt>Resolution</dt><dd>{cam.data?.resolution ?? '—'}</dd></div>
            <div><dt>RTSP</dt><dd style={{ fontFamily: 'monospace', fontSize: 12 }}>{cam.data?.rtsp_url ?? '—'}</dd></div>
          </dl>
        ) : (
          <div className="muted">
            No camera detail yet. Manufacturer/model/resolution/stream URLs are populated by the
            ONVIF / vendor-REST transport — deferred. Reachability (RTSP 554 / HTTP) is monitored
            by the monitoring engine today.
          </div>
        )}
      </div>

      <div className="card">
        <h2>Channels</h2>
        {!hasChannels && <div className="muted">No channels (single camera, or NVR channel map awaits ONVIF collection).</div>}
        {hasChannels && (
          <table>
            <thead><tr><th>Ch</th><th>Camera</th><th>IP</th><th>Status</th></tr></thead>
            <tbody>
              {channels.data!.map((c) => (
                <tr key={c.id}>
                  <td>{c.channel_no}</td>
                  <td>{c.camera_name ?? '—'}</td>
                  <td>{c.camera_ip ?? '—'}</td>
                  <td><span className={`badge badge-${chBadge(c.status)}`}>{c.status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
