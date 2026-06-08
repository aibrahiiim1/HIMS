import { useMemo, useState, type ComponentType } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { HardDrive, Radar, ShieldCheck, MapPin, Wifi, Wrench, KeyRound, Pencil, Lock } from 'lucide-react'
import { api, type Device, type Location, type MonitoringCheck, type Credential, locationPaths } from '../api'
import { HealthRing, colorFor, timeAgo } from './ui'
import { ReachabilityBadge, ManagementBadge } from './StatusBadges'
import { EditDevice } from './EditDevice'
import { RescanSplit } from './RescanSplit'

const PORT_SOURCE_LABEL: Record<string, string> = {
  discovered_open_port: 'discovered open port',
  os_fallback: 'OS-aware fallback',
  manual: 'manual',
}

function deviceHealth(checks: MonitoringCheck[]): { score: number; status: string } {
  if (checks.length === 0) return { score: 0, status: 'unknown' }
  const score = Math.round(checks.reduce((a, c) => {
    const s = (c.last_status || '').toLowerCase()
    return a + (s === 'up' ? 100 : s === 'warning' ? 50 : s === 'down' ? 0 : 60)
  }, 0) / checks.length)
  const status = checks.some((c) => c.last_status === 'down') ? 'down'
    : checks.some((c) => c.last_status === 'warning') ? 'warning'
    : checks.some((c) => c.last_status === 'up') ? 'up' : 'unknown'
  return { score, status }
}

/**
 * Shared device-detail header: identity, badges, health score and a summary
 * stat strip (monitoring, discovery, location, credential/driver). Reused by
 * every per-category detail page for a consistent enterprise device view.
 */
export function DeviceHeader({ deviceId, icon: Icon = HardDrive }: {
  deviceId: string; icon?: ComponentType<{ size?: number | string }>
}) {
  const qc = useQueryClient()
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const checksQ = useQuery({ queryKey: ['dev-checks', deviceId], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${deviceId}/monitoring/checks`) })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  const [credMsg, setCredMsg] = useState<string | null>(null)

  const [repairing, setRepairing] = useState(false)
  const [repairMsg, setRepairMsg] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [scanMsg, setScanMsg] = useState<string | null>(null)

  const d = (devices.data ?? []).find((x) => x.id === deviceId)
  const checks = checksQ.data ?? []
  const { score } = deviceHealth(checks)

  // The reachability (TCP) check is the monitoring target that decides online/offline.
  const tcpCheck = checks.find((c) => c.kind === 'tcp') ?? checks.find((c) => c.target_port != null)

  async function repairReachability() {
    setRepairing(true)
    setRepairMsg(null)
    try {
      const res = await api.post<{ target_port: number; source: string }>(`/devices/${deviceId}/repair-reachability`, {})
      setRepairMsg(`Reachability check set to port ${res.target_port} (${PORT_SOURCE_LABEL[res.source] ?? res.source}).`)
      qc.invalidateQueries({ queryKey: ['dev-checks', deviceId] })
      qc.invalidateQueries({ queryKey: ['devices'] })
    } catch (e) {
      setRepairMsg(`Repair failed: ${(e as Error).message}`)
    } finally {
      setRepairing(false)
    }
  }

  // RescanSplit launches the discovery scan (all credentials, or one chosen
  // credential); after it fires we refresh the device + checks so the page
  // reflects the new result once the scan completes.
  function onScanMsg(m: string) {
    setScanMsg(m)
    setTimeout(() => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      qc.invalidateQueries({ queryKey: ['dev-checks', deviceId] })
    }, 6000)
  }

  // Bind/unbind the credential HIMS uses for this device. Picking the wrong kind
  // (e.g. an iDRAC http_basic cred on a plain web server) is the usual reason a
  // device shows "managed" but collects nothing — so make it changeable here.
  async function setCredential(credID: string) {
    setCredMsg(null)
    try {
      await api.put(`/devices/${deviceId}/credential`, { credential_id: credID }) // "" clears the binding
      setCredMsg(credID ? 'Credential bound. Re-scan to collect with it.' : 'Credential unbound.')
      qc.invalidateQueries({ queryKey: ['devices'] })
    } catch (e) {
      setCredMsg(`Failed: ${(e as Error).message}`)
    }
  }

  if (!d) {
    return (
      <div className="panel"><div className="panel-body">
        <div className="dev-cell"><span className="dev-avatar" style={{ background: 'var(--neutral)' }}><Icon size={18} /></span>
          <div className="dev-meta"><span className="cell-name">{devices.isLoading ? 'Loading device…' : 'Device'}</span><small>{deviceId}</small></div>
        </div>
      </div></div>
    )
  }

  const badges: { label: string; value: string }[] = []
  if (d.vendor) badges.push({ label: 'Vendor', value: d.vendor })
  if (d.model) badges.push({ label: 'Model', value: d.model })
  if (d.os_version) badges.push({ label: 'OS', value: d.os_version })
  if (d.driver) badges.push({ label: 'Driver', value: d.driver })

  return (
    <div className="device-hero">
      <div className="device-hero-id">
        <span className="device-hero-avatar" style={{ background: colorFor(d.category) }}><Icon size={26} /></span>
        <div className="device-hero-text">
          <div className="device-hero-title">
            <h1>{d.name}</h1>
            <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
              <ReachabilityBadge value={d.reachability} />
              <ManagementBadge value={d.management} managedBy={d.managed_by} />
              {d.previously_managed && d.reachability === 'offline' && (
                <span className="badge badge-unknown" title="Offline now, but has a working management method on record">was Managed</span>
              )}
              {d.classification_locked && (
                <span className="badge badge-warning" title={d.manual_classification_reason || 'Classification locked — scans will not overwrite identity'}><Lock size={11} style={{ verticalAlign: -1 }} /> manual</span>
              )}
              <button className="btn btn-ghost btn-xs" onClick={() => setEditing(true)} title="Edit device identity, location, criticality, classification lock"><Pencil size={12} /> Edit</button>
            </span>
          </div>
          <div className="device-hero-sub">
            <span className="mono">{d.primary_ip ?? 'no IP'}</span>
            <span>·</span>
            <span style={{ textTransform: 'capitalize' }}>{d.category.replace(/_/g, ' ')}</span>
            {d.hostname && <><span>·</span><span>{d.hostname}</span></>}
          </div>
          <div className="device-hero-badges">
            {badges.map((b) => (
              <span key={b.label} className="hero-badge"><em>{b.label}</em>{b.value}</span>
            ))}
          </div>
        </div>
      </div>

      <div className="device-hero-metrics">
        {checks.length > 0 && <HealthRing score={score} size={84} label="Health" />}
        <div className="device-hero-stats">
          <div className="hero-stat"><span className="hero-stat-ico tone-info"><Wifi size={15} /></span>
            <div>
              <b>{tcpCheck?.target_port ? `:${tcpCheck.target_port}` : '—'}{tcpCheck ? ` · ${tcpCheck.last_status || 'unknown'}` : ''}</b>
              <small>reachability target {tcpCheck?.last_run_at ? `· ${timeAgo(tcpCheck.last_run_at)}` : ''}</small>
            </div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><ShieldCheck size={15} /></span>
            <div>
              <b style={{ whiteSpace: 'normal', wordBreak: 'break-word' }} title={d.managed_by && d.managed_by.length ? d.managed_by.map((p) => p.toUpperCase()).join(', ') : (d.driver ?? 'none')}>{d.managed_by && d.managed_by.length ? d.managed_by.map((p) => p.toUpperCase()).join(', ') : (d.driver ?? 'none')}</b>
              <small>managed via</small>
            </div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><Radar size={15} /></span>
            <div><b>{timeAgo(d.last_discovery_at)}</b><small>last discovery</small></div></div>
          <div className="hero-stat"><span className="hero-stat-ico"><MapPin size={15} /></span>
            <div><b>{d.location_id ? (locPath[d.location_id] ?? '—') : '—'}</b><small>location</small></div></div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, alignItems: 'flex-end', minWidth: 230 }}>
          <div className="row" style={{ gap: 6, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
            <RescanSplit targets={d.primary_ip ?? ''} label="Re-scan this device" size="sm" onMsg={onScanMsg} />
            <button className="btn btn-ghost btn-sm" onClick={repairReachability} disabled={repairing} title="Recompute the reachability monitoring target from discovered open ports">
              <Wrench size={13} /> {repairing ? 'Repairing…' : 'Repair check'}
            </button>
          </div>
          {scanMsg && <span className="muted" style={{ fontSize: 11, maxWidth: 280, textAlign: 'right' }}>{scanMsg} <Link to="/discovery">View scan jobs</Link></span>}
          {repairMsg && <span className="muted" style={{ fontSize: 11, maxWidth: 280, textAlign: 'right' }}>{repairMsg}</span>}
          <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 11, alignItems: 'flex-end' }} title="Bind the credential HIMS uses to collect from this device, or set to none to unbind">
            <span className="muted" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><KeyRound size={12} /> Collection credential</span>
            <select value={d.credential_id ?? ''} onChange={(e) => setCredential(e.target.value)} style={{ fontSize: 12, maxWidth: 260, minWidth: 200 }}>
              <option value="">— no credential (unbind) —</option>
              {(creds.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>{c.name} · {c.kind}</option>
              ))}
            </select>
          </label>
          {credMsg && <span className="muted" style={{ fontSize: 11, maxWidth: 260, textAlign: 'right' }}>{credMsg}</span>}
        </div>
      </div>
      {editing && <EditDevice device={d} onClose={() => setEditing(false)} onSaved={() => qc.invalidateQueries({ queryKey: ['devices', 'all'] })} />}
    </div>
  )
}
