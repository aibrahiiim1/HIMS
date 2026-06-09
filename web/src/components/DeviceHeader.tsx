import { useMemo, useState, type ComponentType } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { HardDrive, Radar, ShieldCheck, MapPin, Wifi, Wrench, Pencil, Lock, Ghost, Trash2 } from 'lucide-react'
import { api, type Device, type Location, type MonitoringCheck, locationPaths } from '../api'
import { HealthRing, colorFor, timeAgo } from './ui'
import { ReachabilityBadge, ManagementBadge } from './StatusBadges'
import { EditDevice } from './EditDevice'
import { RescanSplit } from './RescanSplit'
import { CredentialBindSelect } from './CredentialBindSelect'

const PORT_SOURCE_LABEL: Record<string, string> = {
  discovered_open_port: 'discovered open port',
  os_fallback: 'OS-aware fallback',
  manual: 'manual',
}

// Friendly labels for the raw managed_by tokens the backend records, so the header
// reads "SNMP · Vendor API" instead of "SNMP_V2C, VENDOR_API". Generic across all
// device types (a vendor profile is "Vendor API" — REST/XML, vSphere, ONVIF, …).
const MANAGED_VIA_LABEL: Record<string, string> = {
  snmp: 'SNMP', snmp_v2c: 'SNMP', snmp_v3: 'SNMP', snmp_metric: 'SNMP',
  vendor_api: 'Vendor API', rest_xml: 'Vendor API',
  ssh: 'SSH', ssh_cli: 'SSH', cli: 'SSH',
  winrm: 'WinRM', wmi: 'WMI', redfish: 'Redfish',
  http_basic: 'HTTP', http: 'HTTP', web: 'HTTP', agent: 'Agent',
}
function managedViaLabels(tokens?: string[] | null, fallback = 'none'): string {
  if (!tokens || tokens.length === 0) return fallback
  const seen = new Set<string>()
  const out: string[] = []
  for (const t of tokens) {
    const label = MANAGED_VIA_LABEL[t.toLowerCase()] ?? t.toUpperCase()
    if (!seen.has(label)) { seen.add(label); out.push(label) }
  }
  return out.join(' · ')
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
export function DeviceHeader({ deviceId, icon: Icon = HardDrive, showCredential = true }: {
  deviceId: string; icon?: ComponentType<{ size?: number | string }>
  // showCredential=false hides the in-header "Collection credential" binder — used
  // by pages (e.g. the wireless controller) that surface it in their Manage tab.
  showCredential?: boolean
}) {
  const qc = useQueryClient()
  const nav = useNavigate()
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const checksQ = useQuery({ queryKey: ['dev-checks', deviceId], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${deviceId}/monitoring/checks`) })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [deleteErr, setDeleteErr] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const doDelete = async () => {
    setDeleting(true); setDeleteErr(null)
    try {
      await api.del(`/devices/${deviceId}`)
      qc.invalidateQueries({ queryKey: ['devices'] })
      nav('/inventory')
    } catch (e) {
      setDeleteErr((e as Error).message); setDeleting(false)
    }
  }

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
              {d.is_virtual && (
                <span className="badge" title="Virtual device — manually entered, not probed" style={{ background: 'rgba(139,92,246,.15)', color: '#8b5cf6' }}><Ghost size={11} style={{ verticalAlign: -1 }} /> Virtual</span>
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
        <div className="device-hero-telemetry">
          {checks.length > 0 && <HealthRing score={score} size={76} label="Health" />}
          <div className="device-hero-stats">
            <div className="hero-stat"><span className="hero-stat-ico tone-info"><Wifi size={15} /></span>
              <div>
                <b>{tcpCheck?.target_port ? `:${tcpCheck.target_port}` : '—'}{tcpCheck ? ` · ${tcpCheck.last_status || 'unknown'}` : ''}</b>
                <small>reachability target {tcpCheck?.last_run_at ? `· ${timeAgo(tcpCheck.last_run_at)}` : ''}</small>
              </div></div>
            <div className="hero-stat"><span className="hero-stat-ico"><ShieldCheck size={15} /></span>
              <div>
                <b style={{ whiteSpace: 'normal', wordBreak: 'break-word' }} title={managedViaLabels(d.managed_by, d.driver ?? 'none')}>{managedViaLabels(d.managed_by, d.driver ?? 'none')}</b>
                <small>managed via</small>
              </div></div>
            <div className="hero-stat"><span className="hero-stat-ico"><Radar size={15} /></span>
              <div><b>{timeAgo(d.last_discovery_at)}</b><small>last discovery</small></div></div>
            <div className="hero-stat"><span className="hero-stat-ico"><MapPin size={15} /></span>
              <div><b>{d.location_id ? (locPath[d.location_id] ?? '—') : '—'}</b><small>location</small></div></div>
          </div>
        </div>
        <div className="device-hero-actions">
          {d.is_virtual ? (
            <>
              <Link className="btn btn-primary btn-sm" to={`/devices/virtual/${deviceId}/edit`} title="Edit this virtual device's identity + configuration"><Pencil size={13} /> Edit virtual device</Link>
              <button className="btn btn-danger btn-sm" onClick={() => { setDeleteErr(null); setConfirmingDelete(true) }} title="Delete this virtual device and its manual ports/VLANs/neighbors/MACs"><Trash2 size={13} /> Delete</button>
            </>
          ) : (
            <>
              <RescanSplit targets={d.primary_ip ?? ''} label="Re-scan this device" size="sm" onMsg={onScanMsg} />
              <button className="btn btn-ghost btn-sm" onClick={repairReachability} disabled={repairing} title="Recompute the reachability monitoring target from discovered open ports">
                <Wrench size={13} /> {repairing ? 'Repairing…' : 'Repair check'}
              </button>
            </>
          )}
        </div>
        {(scanMsg || repairMsg) && (
          <div className="device-hero-msgs">
            {scanMsg && <span className="muted">{scanMsg} <Link to="/discovery">View scan jobs</Link></span>}
            {repairMsg && <span className="muted">{repairMsg}</span>}
          </div>
        )}
        {showCredential && !d.is_virtual && <div className="device-hero-cred"><CredentialBindSelect deviceId={deviceId} align="end" /></div>}
      </div>
      {editing && <EditDevice device={d} onClose={() => setEditing(false)} onSaved={() => qc.invalidateQueries({ queryKey: ['devices', 'all'] })} />}
      {confirmingDelete && (
        <div role="dialog" aria-modal="true" onClick={() => !deleting && setConfirmingDelete(false)}
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.45)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div className="card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 480, width: '90%', padding: 20 }}>
            <h2 style={{ margin: '0 0 8px', display: 'inline-flex', gap: 8, alignItems: 'center' }}><Trash2 size={18} /> Delete virtual device?</h2>
            <p style={{ margin: '0 0 6px' }}>Delete <strong>{d.name}</strong>?</p>
            <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
              This permanently removes the device and all its manually-entered ports, VLANs, neighbors,
              learned MACs and category-specific data (interfaces, firewall/UPS/wireless records, OS inventory).
              This cannot be undone.
            </p>
            {deleteErr && <div className="enc-banner crit" style={{ margin: '8px 0' }}>{deleteErr}</div>}
            <div className="row" style={{ gap: 10, justifyContent: 'flex-end', marginTop: 14 }}>
              <button className="btn btn-ghost" disabled={deleting} onClick={() => setConfirmingDelete(false)}>Cancel</button>
              <button className="btn btn-danger" disabled={deleting} onClick={doDelete}><Trash2 size={13} /> {deleting ? 'Deleting…' : 'Delete device'}</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
