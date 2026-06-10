import { useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { Camera, Video, Film, HardDrive, Disc, Network, Activity, Wrench, Cpu, Globe } from 'lucide-react'
import { api, type CameraInfo, type NVRChannel, type NVRDetail, type Device, type DeviceWebAccess, type CredTestResult } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { CctvCollect } from '../components/CctvCollect'
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
  const [tab, setTab] = useState<Tab>('overview')

  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const dev = (devices.data ?? []).find((d) => d.id === id)
  const cam = useQuery({ queryKey: ['camera', id], queryFn: () => api.get<CameraInfo>(`/devices/${id}/camera`) })
  const nvr = useQuery({ queryKey: ['nvr', id], queryFn: () => api.get<NVRDetail>(`/devices/${id}/nvr`) })

  const c = cam.data
  const info = nvr.data?.info ?? null
  const channels = nvr.data?.channels ?? []
  const storage = nvr.data?.storage ?? []
  const isNVR = useMemo(() => {
    const dt = (info?.device_type ?? '').toLowerCase()
    return dev?.category === 'nvr' || dev?.category === 'dvr' || dt.includes('nvr') || dt.includes('dvr') || channels.length > 0 || storage.length > 0
  }, [dev?.category, info?.device_type, channels.length, storage.length])

  const chOnline = channels.filter((x) => x.status === 'online').length
  const ip = dev?.primary_ip || ''

  // ---- camera (non-recorder): compact layout --------------------------------
  if (!isNVR) {
    const ch = channels
    return (
      <div>
        <DeviceHeader deviceId={id!} icon={Camera} />
        <Panel>
          <CctvCollect deviceId={id!} boundCredId={dev?.cctv_credential_id ?? dev?.credential_id} generalCredId={dev?.credential_id} compact label="Collect" />
        </Panel>
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
        <WebAccessPanel deviceId={id!} />
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

      {tab === 'channels' && <ChannelsTab channels={channels} chOnline={chOnline} />}

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
          <p className="muted" style={{ marginTop: 10, fontSize: 13 }}>
            ISAPI is tried over HTTPS/HTTP with the credential(s) you select on the Operations tab. Unsupported endpoints are reported as “not exposed by device”, not as a generic failure.
          </p>
        </Panel>
      )}

      {tab === 'ops' && (
        <Panel title="Operations" icon={Wrench}>
          <p className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
            Pick the NVR's <strong>web admin</strong> login credential(s) to try — the ONVIF integration user is separate and is rejected by ISAPI. HIMS tries each selected credential and binds the first that authenticates. Selecting more than 3 of the same type warns first, since repeated wrong logins can trigger a Hikvision IP lockout.
          </p>
          <CctvCollect deviceId={id!} boundCredId={dev?.cctv_credential_id ?? dev?.credential_id} generalCredId={dev?.credential_id} label="Collect NVR data" />
        </Panel>
      )}

      {tab === 'ops' && <WebAccessPanel deviceId={id!} />}
    </div>
  )
}

// WebAccessPanel shows the device's discovered web ports (classified), the ordered
// endpoints the collector will try, the last successful endpoint, and a per-device
// override (preferred scheme/port + alternates + notes) for devices on custom web
// ports. Discovered ports are always tried before any guessed ladder.
function WebAccessPanel({ deviceId }: { deviceId: string }) {
  const q = useQuery({ queryKey: ['web-access', deviceId], queryFn: () => api.get<DeviceWebAccess>(`/devices/${deviceId}/web-access`) })
  if (q.isLoading || !q.data) {
    return <Panel title="Web / API Access" icon={Globe}><div className="loading">Loading…</div></Panel>
  }
  return <WebAccessForm deviceId={deviceId} data={q.data} />
}

function WebAccessForm({ deviceId, data: d }: { deviceId: string; data: DeviceWebAccess }) {
  const qc = useQueryClient()
  // Initialised once from the loaded data (state initialisers run on mount only);
  // the read-only sections below render from the `d` prop, which refreshes on save.
  const [scheme, setScheme] = useState(d.scheme)
  const [port, setPort] = useState(d.port != null ? String(d.port) : '')
  const [alt, setAlt] = useState(d.alt_ports)
  const [notes, setNotes] = useState(d.notes)
  const [prefProto, setPrefProto] = useState(d.pref_proto)
  const save = useMutation({
    mutationFn: () => api.put<DeviceWebAccess>(`/devices/${deviceId}/web-access`, {
      scheme, port: port ? Number(port) : null, alt_ports: alt, notes, pref_proto: prefProto,
    }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['web-access', deviceId] }),
  })
  // Recent failed collection attempts (categorised) for this device.
  const fails = useQuery({
    queryKey: ['cred-tests', deviceId],
    queryFn: () => api.get<CredTestResult[]>(`/devices/${deviceId}/credential-tests?limit=20`),
  })
  const recentFails = (fails.data ?? []).filter((r) => !r.success).slice(0, 8)
  return (
    <Panel title="Web / API Access" icon={Globe} subtitle="discovered ports, the endpoints HIMS tries, and per-device overrides">
      {(
        <>
          <div style={{ marginBottom: 14, padding: 10, border: '1px solid var(--border, #2a3a47)', borderRadius: 8 }}>
            <div className="row" style={{ gap: 10, alignItems: 'center', marginBottom: 6 }}>
              <span className="muted" style={{ fontSize: 12 }}>Last successful source</span>
              {d.last_proto
                ? <span className="badge badge-up" style={{ textTransform: 'uppercase' }}>{d.last_proto}</span>
                : <span className="muted">— never collected</span>}
            </div>
            {d.last_ok && (
              <div className="row" style={{ gap: 18, flexWrap: 'wrap', fontSize: 13 }}>
                <span>Endpoint <span className="mono">{d.last_ok}</span></span>
                <span>Scheme <strong>{d.last_scheme || '—'}</strong></span>
                <span>Port <strong>{d.last_port ?? '—'}</strong></span>
                <span>Credential <strong>{d.last_credential || '—'}</strong></span>
                {d.last_ok_at && <span className="muted">{d.last_ok_at.slice(0, 19).replace('T', ' ')}</span>}
              </div>
            )}
          </div>

          <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 4 }}>Discovered ports</div>
          {d.discovered.length === 0 ? <div className="muted" style={{ fontSize: 12, marginBottom: 12 }}>No open ports recorded — run a scan.</div> : (
            <table style={{ marginBottom: 14 }}>
              <thead><tr><th>Port</th><th>Looks like</th><th>Scheme</th><th>Web candidate</th></tr></thead>
              <tbody>
                {d.discovered.map((p) => (
                  <tr key={p.port}>
                    <td className="mono"><strong>{p.port}</strong></td>
                    <td>{p.kind}</td>
                    <td className="mono">{p.scheme || '—'}</td>
                    <td>{p.web ? <span className="badge badge-up">yes</span> : <span className="muted">—</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 4 }}>Endpoints tried (in order)</div>
          <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>The collector tries these before any default ladder — discovered/override/configured first.</div>
          <ol style={{ margin: '0 0 14px 18px', fontSize: 13 }}>
            {d.candidates.length === 0 ? <li className="muted">none — falls back to the default scheme/port ladder</li> : d.candidates.map((c, i) => <li key={i} className="mono">{c}</li>)}
          </ol>

          <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 6 }}>Per-device override</div>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label>Preferred protocol
              <select className="field" style={{ display: 'block' }} value={prefProto} onChange={(e) => setPrefProto(e.target.value)}>
                <option value="">(auto)</option><option value="isapi">ISAPI</option><option value="onvif">ONVIF</option><option value="http">HTTP</option>
              </select>
            </label>
            <label>Preferred scheme
              <select className="field" style={{ display: 'block' }} value={scheme} onChange={(e) => setScheme(e.target.value)}>
                <option value="">(auto)</option><option value="http">http</option><option value="https">https</option>
              </select>
            </label>
            <label>Preferred port<input className="field" style={{ width: 110, display: 'block' }} type="number" value={port} onChange={(e) => setPort(e.target.value)} placeholder="8010" /></label>
            <label style={{ minWidth: 150 }}>Alternate ports<input className="field" style={{ width: '100%', display: 'block' }} value={alt} onChange={(e) => setAlt(e.target.value)} placeholder="8000, 8008" /></label>
            <label style={{ flex: 1, minWidth: 160 }}>Notes<input className="field" style={{ width: '100%', display: 'block' }} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="e.g. uses 8010" /></label>
            <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{save.isPending ? 'Saving…' : 'Save'}</button>
            {save.isSuccess && <span className="badge badge-up">saved</span>}
          </div>
          {save.error && <div className="error-msg" style={{ marginTop: 8 }}>{(save.error as Error).message}</div>}

          {recentFails.length > 0 && (
            <div style={{ marginTop: 16 }}>
              <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 4 }}>Recent failed attempts</div>
              <table>
                <thead><tr><th>When</th><th>Protocol</th><th>Credential</th><th>Reason</th><th>Detail</th></tr></thead>
                <tbody>
                  {recentFails.map((r, i) => (
                    <tr key={i}>
                      <td className="muted" style={{ fontSize: 12 }}>{r.tested_at?.slice(0, 19).replace('T', ' ')}</td>
                      <td>{r.protocol || r.kind}</td>
                      <td>{r.credential_name}</td>
                      <td><span className="badge badge-warning">{r.category.replace(/_/g, ' ')}</span></td>
                      <td className="muted" style={{ fontSize: 12, maxWidth: 360 }}>{r.detail}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </Panel>
  )
}

// ChannelsTab renders the recorder's camera channels with a live filter (by name /
// IP / channel number / status) and pagination, so a recorder with many channels
// stays usable. A channel whose camera IP matched an already-discovered camera
// device links to that device.
function ChannelsTab({ channels, chOnline }: { channels: NVRChannel[]; chOnline: number }) {
  const [q, setQ] = useState('')
  const [page, setPage] = useState(0)
  const PAGE = 25
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    if (!t) return channels
    return channels.filter((x) =>
      (x.camera_name || '').toLowerCase().includes(t) ||
      (x.camera_ip || '').toLowerCase().includes(t) ||
      String(x.channel_no).includes(t) ||
      (x.status || '').toLowerCase().includes(t))
  }, [channels, q])
  const pages = Math.max(1, Math.ceil(filtered.length / PAGE))
  const cur = Math.min(page, pages - 1)
  const rows = filtered.slice(cur * PAGE, cur * PAGE + PAGE)

  return (
    <Panel title="Channels / Cameras" icon={Video} subtitle={channels.length ? `${chOnline}/${channels.length} online` : undefined} pad={false}>
      {channels.length === 0 ? (
        <EmptyState icon={Video} title="No channels collected" message="Run Collect — channels populate from ISAPI /ContentMgmt/InputProxy/channels. If the recorder exposes none, it will report empty." />
      ) : (
        <>
          <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center', padding: '10px 12px', gap: 10, flexWrap: 'wrap' }}>
            <input placeholder="Search name / IP / channel / status…" value={q}
              onChange={(e) => { setQ(e.target.value); setPage(0) }}
              style={{ padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13, width: 320, maxWidth: '100%' }} />
            <span className="muted" style={{ fontSize: 13 }}>{filtered.length} of {channels.length}</span>
          </div>
          <table className="data-table">
            <thead><tr><th>Ch</th><th>Camera name</th><th>Camera IP</th><th>Linked device</th><th>Status</th></tr></thead>
            <tbody>
              {rows.map((x: NVRChannel) => (
                <tr key={x.id}>
                  <td className="cell-name">{x.channel_no}</td>
                  <td>{x.camera_name || '—'}</td>
                  <td className="mono">{x.camera_ip || '—'}</td>
                  <td>{x.camera_device_id ? <Link className="cell-name" to={`/cctv/${x.camera_device_id}`}>camera device</Link> : <span className="muted">—</span>}</td>
                  <td><StatusPill status={chStatus(x.status)} label={x.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
          {pages > 1 && (
            <div className="row" style={{ justifyContent: 'center', gap: 8, padding: 10 }}>
              <button className="btn btn-sm" disabled={cur === 0} onClick={() => setPage(cur - 1)}>Prev</button>
              <span className="muted" style={{ fontSize: 13 }}>Page {cur + 1} / {pages}</span>
              <button className="btn btn-sm" disabled={cur >= pages - 1} onClick={() => setPage(cur + 1)}>Next</button>
            </div>
          )}
        </>
      )}
    </Panel>
  )
}
