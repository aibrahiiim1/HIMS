import { useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Camera, Video, Film, HardDrive, Disc, Network, Activity, Wrench, RefreshCw, Cpu } from 'lucide-react'
import { api, type CameraInfo, type NVRChannel, type NVRDetail, type Device } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill } from '../components/ui'

const chStatus = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')

function fmtMB(mb?: number): string {
  if (!mb || mb <= 0) return '—'
  const gb = mb / 1024
  return gb >= 1024 ? `${(gb / 1024).toFixed(2)} TB` : `${gb.toFixed(1)} GB`
}

type Tab = 'overview' | 'channels' | 'storage' | 'recording' | 'network' | 'health' | 'ops'

// Camera / NVR detail. For a recorder (category nvr) this is a full multi-tab NVR
// console — identity, channels/cameras, storage/HDD, recording, streams, collection
// health and operations — populated read-only from Hikvision ISAPI. A plain camera
// keeps the compact single-view layout.
export function CctvDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const [tab, setTab] = useState<Tab>('overview')

  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const dev = (devices.data ?? []).find((d) => d.id === id)
  const cam = useQuery({ queryKey: ['camera', id], queryFn: () => api.get<CameraInfo>(`/devices/${id}/camera`) })
  const nvr = useQuery({ queryKey: ['nvr', id], queryFn: () => api.get<NVRDetail>(`/devices/${id}/nvr`) })

  const collect = useMutation({
    mutationFn: () => api.post<{ collected: boolean; reason?: string; detail?: string; category?: string; credential_used?: string }>(`/devices/${id}/collect-cctv`, {}),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['nvr', id] })
      qc.invalidateQueries({ queryKey: ['camera', id] })
      qc.invalidateQueries({ queryKey: ['devices', 'all'] })
    },
  })

  const c = cam.data
  const info = nvr.data?.info ?? null
  const channels = nvr.data?.channels ?? []
  const storage = nvr.data?.storage ?? []
  const isNVR = useMemo(() => {
    const dt = (info?.device_type ?? '').toLowerCase()
    return dev?.category === 'nvr' || dt.includes('nvr') || dt.includes('dvr') || channels.length > 0 || storage.length > 0
  }, [dev?.category, info?.device_type, channels.length, storage.length])

  const chOnline = channels.filter((x) => x.status === 'online').length
  const ip = dev?.primary_ip || ''

  // ---- camera (non-recorder): compact layout --------------------------------
  if (!isNVR) {
    const ch = channels
    return (
      <div>
        <DeviceHeader deviceId={id!} icon={Camera} />
        <CollectBar collect={collect} />
        <div className="kpi-grid">
          <Kpi label="Manufacturer" value={c?.manufacturer || '—'} icon={Camera} tone="info" />
          <Kpi label="Model" value={c?.model || '—'} icon={Video} />
          <Kpi label="Resolution" value={c?.resolution || '—'} icon={Film} />
          <Kpi label="Channels" value={ch.length || '—'} icon={Video} sub={ch.length ? `${chOnline} online` : undefined} />
        </div>
        <Panel title="Device Information" icon={Camera}>
          {c && c.device_id ? (
            <DefList items={[
              { label: 'Manufacturer', value: c?.manufacturer || '—' },
              { label: 'Model', value: c?.model || '—' },
              { label: 'Resolution', value: c?.resolution || '—' },
              { label: 'RTSP stream', value: c?.rtsp_url ? <span className="mono">{c.rtsp_url}</span> : '—' },
              { label: 'ONVIF endpoint', value: c?.onvif_url ? <span className="mono">{c.onvif_url}</span> : '—' },
            ]} />
          ) : (
            <EmptyState icon={Camera} title="No detail collected yet"
              message="Bind the device's web (ONVIF/http_basic) credential and click Collect — identity populates from ONVIF/ISAPI. Reachability is already monitored." />
          )}
        </Panel>
      </div>
    )
  }

  // ---- NVR/DVR: full multi-tab console --------------------------------------
  const tabs: { key: Tab; label: string; icon: typeof Camera }[] = [
    { key: 'overview', label: 'Overview', icon: Activity },
    { key: 'channels', label: `Channels / Cameras${channels.length ? ` (${channels.length})` : ''}`, icon: Video },
    { key: 'storage', label: `Storage / HDD${storage.length ? ` (${storage.length})` : ''}`, icon: HardDrive },
    { key: 'recording', label: 'Recording', icon: Disc },
    { key: 'network', label: 'Network / Streams', icon: Network },
    { key: 'health', label: 'Collection health', icon: Cpu },
    { key: 'ops', label: 'Operations', icon: Wrench },
  ]
  const notExposed = (v?: string | null) => !v ? <span className="muted">not exposed by device</span> : v

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Video} />

      <div className="kpi-grid">
        <Kpi label="Type" value={(info?.device_type || 'NVR').toUpperCase()} icon={Video} tone="info" />
        <Kpi label="Model" value={info?.model || c?.model || '—'} icon={Cpu} />
        <Kpi label="Channels" value={channels.length || info?.channel_count || '—'} icon={Video} sub={channels.length ? `${chOnline} online` : undefined} />
        <Kpi label="HDDs" value={storage.length || info?.hdd_count || '—'} icon={HardDrive} />
      </div>

      <div className="seg" role="tablist" style={{ margin: '4px 0 14px' }}>
        {tabs.map((t) => (
          <button key={t.key} className={tab === t.key ? 'active' : ''} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>

      {tab === 'overview' && (
        <Panel title="Recorder Identity" icon={Video}>
          {info || c?.model ? (
            <DefList items={[
              { label: 'Vendor', value: info?.manufacturer || c?.manufacturer || '—' },
              { label: 'Model', value: info?.model || c?.model || '—' },
              { label: 'Device type', value: (info?.device_type || 'NVR').toUpperCase() },
              { label: 'Serial number', value: info?.serial ? <span className="mono">{info.serial}</span> : '—' },
              { label: 'Firmware', value: info?.firmware || '—' },
              { label: 'Channels', value: String(channels.length || info?.channel_count || 0) },
              { label: 'HDDs', value: String(storage.length || info?.hdd_count || 0) },
              { label: 'Recording', value: notExposed(info?.recording) },
              { label: 'Health', value: notExposed(info?.health) },
            ]} />
          ) : (
            <EmptyState icon={Video} title="No NVR data collected yet"
              message="Bind this NVR's web credential (the device admin login — the ONVIF user does not work for ISAPI) and click Collect on the Operations tab." />
          )}
        </Panel>
      )}

      {tab === 'channels' && (
        <Panel title="Channels / Cameras" icon={Video} subtitle={channels.length ? `${chOnline}/${channels.length} online` : undefined} pad={false}>
          {channels.length === 0 ? (
            <EmptyState icon={Video} title="No channels collected" message="Run Collect — channels populate from ISAPI /ContentMgmt/InputProxy/channels. If the recorder exposes none, it will report empty." />
          ) : (
            <table className="data-table">
              <thead><tr><th>Ch</th><th>Camera name</th><th>Camera IP</th><th>Status</th></tr></thead>
              <tbody>
                {channels.map((x: NVRChannel) => (
                  <tr key={x.id}>
                    <td className="cell-name">{x.channel_no}</td>
                    <td>{x.camera_name || '—'}</td>
                    <td className="mono">{x.camera_ip || '—'}</td>
                    <td><StatusPill status={chStatus(x.status)} label={x.status} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'storage' && (
        <Panel title="Storage / HDD" icon={HardDrive} pad={false}>
          {storage.length === 0 ? (
            <EmptyState icon={HardDrive} title="No storage reported" message="Run Collect — HDDs populate from ISAPI /ContentMgmt/Storage/hdd. A recorder with no HDD data will report empty." />
          ) : (
            <table className="data-table">
              <thead><tr><th>HDD</th><th>Name</th><th>Status</th><th>Capacity</th><th>Free</th><th>Used</th><th>Mode</th></tr></thead>
              <tbody>
                {storage.map((h) => {
                  const used = h.capacity_mb > 0 ? Math.round(((h.capacity_mb - h.free_mb) / h.capacity_mb) * 100) : 0
                  return (
                    <tr key={h.id}>
                      <td className="cell-name">{h.hdd_id}</td>
                      <td>{h.name || '—'}</td>
                      <td><StatusPill status={h.status === 'ok' ? 'up' : h.status === 'error' ? 'down' : 'unknown'} label={h.status} /></td>
                      <td>{fmtMB(h.capacity_mb)}</td>
                      <td>{fmtMB(h.free_mb)}</td>
                      <td>{h.capacity_mb > 0 ? `${used}%` : '—'}</td>
                      <td>{h.property || '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'recording' && (
        <Panel title="Recording status" icon={Disc}>
          <DefList items={[
            { label: 'Recording', value: notExposed(info?.recording) },
            { label: 'HDDs available', value: String(storage.length) },
            { label: 'Writable HDDs', value: String(storage.filter((h) => h.property.toUpperCase().includes('RW') || h.property === '').length) },
          ]} />
          {!info?.recording && <p className="muted" style={{ marginTop: 10, fontSize: 13 }}>Recording detail is read from ISAPI /ContentMgmt/record/tracks; if the firmware does not expose it, it is shown as “not exposed by device” rather than a failure.</p>}
        </Panel>
      )}

      {tab === 'network' && (
        <Panel title="Network / Streams" icon={Network}>
          <DefList items={[
            { label: 'Management IP', value: ip ? <span className="mono">{ip}</span> : '—' },
            { label: 'ONVIF endpoint', value: c?.onvif_url ? <span className="mono">{c.onvif_url}</span> : '—' },
            { label: 'RTSP (main) URL pattern', value: ip ? <span className="mono">{`rtsp://${ip}:554/Streaming/Channels/<ch>01`}</span> : '—' },
          ]} />
          {channels.length > 0 && (
            <table className="data-table" style={{ marginTop: 12 }}>
              <thead><tr><th>Ch</th><th>Camera</th><th>RTSP main stream</th></tr></thead>
              <tbody>
                {channels.map((x) => (
                  <tr key={x.id}>
                    <td className="cell-name">{x.channel_no}</td>
                    <td>{x.camera_name || '—'}</td>
                    <td className="mono">{ip ? `rtsp://${ip}:554/Streaming/Channels/${x.channel_no}01` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'health' && (
        <Panel title="Collection health" icon={Cpu}>
          <DefList items={[
            { label: 'Source', value: info?.source || 'isapi' },
            { label: 'Identity', value: info?.model ? <StatusPill status="up" label="collected" /> : <StatusPill status="unknown" label="pending" /> },
            { label: 'Channels', value: channels.length ? <StatusPill status="up" label={`${channels.length} collected`} /> : <StatusPill status="unknown" label="none / not exposed" /> },
            { label: 'Storage', value: storage.length ? <StatusPill status="up" label={`${storage.length} collected`} /> : <StatusPill status="unknown" label="none / not exposed" /> },
            { label: 'Last collected', value: info?.collected_at ? new Date(info.collected_at).toLocaleString() : '—' },
          ]} />
          {collect.data && <CollectResult data={collect.data} />}
          <p className="muted" style={{ marginTop: 10, fontSize: 13 }}>
            ISAPI is tried over HTTPS/HTTP with the device's bound credential only (no credential spraying — that would trigger a Hikvision IP lockout). Unsupported endpoints are reported as “not exposed by device”, not as a generic failure.
          </p>
        </Panel>
      )}

      {tab === 'ops' && (
        <Panel title="Operations" icon={Wrench}>
          <p className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
            Collect uses the credential <strong>bound to this device</strong> only. Bind the NVR's <strong>web admin</strong> login (an ONVIF/http_basic credential) — the ONVIF integration user is separate and is rejected by ISAPI. Repeated wrong logins can trigger a Hikvision IP lockout.
          </p>
          <button className="btn btn-primary" disabled={collect.isPending} onClick={() => collect.mutate()}>
            <RefreshCw size={15} className={collect.isPending ? 'spin' : ''} /> {collect.isPending ? 'Collecting…' : 'Collect NVR data'}
          </button>
          {collect.isError && <div className="enc-banner crit" style={{ marginTop: 12 }}>{(collect.error as Error).message}</div>}
          {collect.data && <CollectResult data={collect.data} />}
        </Panel>
      )}
    </div>
  )
}

function CollectBar({ collect }: { collect: { mutate: () => void; isPending: boolean; data?: CollectResp; isError?: boolean; error?: unknown } }) {
  return (
    <Panel>
      <div className="row" style={{ justifyContent: 'space-between' }}>
        <span className="muted" style={{ fontSize: 13 }}>Identity is collected over ONVIF/ISAPI using the bound credential.</span>
        <button className="btn btn-primary btn-sm" disabled={collect.isPending} onClick={() => collect.mutate()}>
          <RefreshCw size={14} className={collect.isPending ? 'spin' : ''} /> {collect.isPending ? 'Collecting…' : 'Collect'}
        </button>
      </div>
      {collect.data && <CollectResult data={collect.data} />}
    </Panel>
  )
}

type CollectResp = { collected: boolean; reason?: string; detail?: string; category?: string; credential_used?: string }

function CollectResult({ data }: { data: CollectResp }) {
  const lockout = (data.detail || '').toLowerCase().includes('lockout') || (data.reason === 'auth_failed')
  const cls = data.collected ? 'ok' : lockout ? 'warn' : 'crit'
  return (
    <div className={`enc-banner ${cls}`} style={{ marginTop: 12 }}>
      {data.collected
        ? `Collected${data.category ? ` (${data.category})` : ''}${data.credential_used ? ` via ${data.credential_used}` : ''}: ${data.detail || ''}`
        : `${data.reason || 'failed'} — ${data.detail || ''}`}
    </div>
  )
}
