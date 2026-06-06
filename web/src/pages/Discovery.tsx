import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Radar, Boxes, CircleX, Clock, KeyRound } from 'lucide-react'
import {
  api, locationPaths,
  type DiscoveryJob, type DiscoveryResult, type Location, type Credential, type AccessCoverage,
  type ScanPreflight,
} from '../api'
import { PageHeader, Kpi, timeAgo } from '../components/ui'

// ---------- shared styles ----------
const btn: React.CSSProperties = { padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600 }
const ghost: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: '#ef9a9a', border: '1px solid #ef9a9a', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const input: React.CSSProperties = { padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13 }
// readable on the white dropdown panel
const pickerBtn: React.CSSProperties = { padding: '3px 10px', background: '#f0f4f8', color: '#1565c0', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }

const TABS = ['Network scan', 'Import', 'Controllers', 'Active Directory', 'Jobs'] as const
type Tab = typeof TABS[number]

type ScanMode = 'single' | 'range' | 'cidr' | 'site_subnets'
const MODE_LABEL: Record<ScanMode, string> = { single: 'Single IP', range: 'IP Range', cidr: 'Subnet / CIDR', site_subnets: 'By Site' }
const MODE_PH: Record<ScanMode, string> = {
  single: '10.20.0.10', range: '172.21.96.1-172.21.96.254  (or 172.21.96.1-254)',
  cidr: '172.21.96.0/24', site_subnets: '(scans every subnet bound to the selected site)',
}
const CATEGORIES = ['unknown', 'switch', 'router', 'firewall', 'access_point', 'wireless_controller', 'server', 'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer', 'ip_phone', 'pbx', 'voice_gateway', 'database', 'directory', 'dns', 'dhcp', 'fingerprint', 'endpoint', 'ups', 'isp_router', 'application']
const CTRL_KINDS = ['unifi', 'ruckus', 'omada', 'extreme', 'vsphere', 'hyperv', 'redfish', 'onvif', 'cucm']

const jobBadge = (s: string) => (s === 'running' ? 'warning' : s === 'completed' ? 'up' : s === 'failed' || s === 'cancelled' ? 'down' : 'unknown')
const outcomeBadge = (o: string) => (o === 'enrolled' ? 'up' : o === 'failed' ? 'down' : o === 'classified' ? 'access' : 'unknown')

// profileVendorHint maps a scanned category to the vendor_type to pre-select when
// creating a profile from a Scan Result. Wireless has several vendor types, so the
// operator picks — we leave it blank there.
function profileVendorHint(category?: string | null): string {
  switch (category) {
    case 'virtual_host': return 'vmware'
    case 'camera': case 'nvr': return 'cctv'
    case 'pbx': case 'voice_gateway': case 'ip_phone': return 'cucm'
    default: return ''
  }
}

// ProfileCell renders the Vendor Connection Profile state for a scanned
// VMware/CCTV/wireless/voice candidate with unambiguous messaging + actions, so
// the operator never has to guess why a deep collection did or didn't happen:
//   • No matching profile found        → Create Vendor Profile
//   • Matching profile found           → Open Vendor Profile
//   • Profile test succeeded / failed  → (with the failure reason)
//   • Collection succeeded / failed    → Retry with profile
function ProfileCell({ r, qc, jobID }: { r: DiscoveryResult; qc: ReturnType<typeof useQueryClient>; jobID: string | null }) {
  const p = r.probe_data?.profile
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

  // Categories that don't use vendor profiles: nothing to show.
  if (!p) return <span className="muted">—</span>

  const linkCell: React.CSSProperties = { color: '#90caf9', fontSize: 11, cursor: 'pointer', textDecoration: 'underline' }

  // ---- No matching profile -------------------------------------------------
  if (!p.resolved) {
    const vt = profileVendorHint(r.category)
    const params = new URLSearchParams({ create: '1' })
    if (vt) params.set('vendor_type', vt)
    if (r.device_id) params.set('device_id', r.device_id)
    if (r.ip) params.set('target_url', r.ip)
    return (
      <div>
        <span className="badge badge-warning">No matching profile</span>
        <div style={{ marginTop: 4 }}>
          <Link to={`/vendor-profiles?${params.toString()}`} style={linkCell}>+ Create Vendor Profile</Link>
        </div>
      </div>
    )
  }

  // ---- Matching profile found ----------------------------------------------
  const retry = async () => {
    setBusy(true); setMsg('')
    try {
      const res = await api.post<{ collected: boolean; detail: string }>(`/vendor-profiles/${p.id}/run-collection`, { device_id: r.device_id ?? '' })
      setMsg(res.detail)
      if (jobID) qc.invalidateQueries({ queryKey: ['discovery-job', jobID] })
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const testState = p.test_ok === undefined ? null
    : <span className={`badge badge-${p.test_ok ? 'up' : 'down'}`} title={p.detail ?? ''}>{p.test_ok ? 'Profile test succeeded' : 'Profile test failed'}</span>
  const collState = p.collection_ok === undefined ? null
    : <span className={`badge badge-${p.collection_ok ? 'up' : 'down'}`} title={p.detail ?? ''}>{p.collection_ok ? 'Collection succeeded' : 'Collection failed'}</span>

  return (
    <div>
      <div style={{ fontSize: 11 }}><span className="badge badge-up">Matching profile</span> <strong>{p.name || p.vendor_type}</strong></div>
      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginTop: 3 }}>{testState}{collState}</div>
      {!p.collection_ok && p.detail && <div className="muted" style={{ fontSize: 11, marginTop: 3 }}>{p.detail}</div>}
      <div style={{ display: 'flex', gap: 10, marginTop: 4 }}>
        <Link to={`/vendor-profiles?open=${p.id}`} style={linkCell}>Open Vendor Profile</Link>
        {r.device_id && <span style={{ ...linkCell, opacity: busy ? 0.5 : 1 }} onClick={() => !busy && retry()}>{busy ? 'Retrying…' : 'Retry with profile'}</span>}
      </div>
      {msg && <div className="muted" style={{ fontSize: 11, marginTop: 3 }}>{msg}</div>}
    </div>
  )
}

function duration(a?: string | null, b?: string | null): string {
  if (!a) return '—'
  const end = b ? new Date(b).getTime() : Date.now()
  const s = Math.max(0, Math.round((end - new Date(a).getTime()) / 1000))
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`
}

export function Discovery() {
  const qc = useQueryClient()
  const [tab, setTab] = useState<Tab>('Network scan')
  const [jobID, setJobID] = useState<string | null>(null)
  const [msg, setMsg] = useState('')

  const locations = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const locPath = locationPaths(locations.data ?? [])

  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'), refetchInterval: 5000 })
  const detail = useQuery({
    queryKey: ['discovery-job', jobID],
    queryFn: () => api.get<{ job: DiscoveryJob; results: DiscoveryResult[] }>(`/discovery/jobs/${jobID}`),
    enabled: !!jobID, refetchInterval: 4000,
  })
  const afterLaunch = (j: DiscoveryJob) => { setJobID(j.id); setTab('Jobs'); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) }

  const cov = useQuery({ queryKey: ['access-coverage'], queryFn: () => api.get<AccessCoverage>('/dashboard/access-coverage'), retry: 0 })

  const jobList = jobs.data ?? []
  const lastJob = [...jobList].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0]
  const foundTotal = jobList.reduce((a, j) => a + j.found_count, 0)
  const failedCount = jobList.filter((j) => j.status === 'failed').length

  return (
    <div>
      <PageHeader title="Discovery Center" icon={Radar} subtitle="Scan networks, import assets, and onboard devices into inventory" />

      <div className="kpi-grid">
        <Kpi label="Scan Jobs" value={jobList.length} icon={Radar} tone="info" />
        <Kpi label="Devices Found" value={foundTotal} icon={Boxes} tone="default" sub="all scans" />
        <Kpi label="Failed Scans" value={failedCount} icon={CircleX} tone={failedCount > 0 ? 'crit' : 'default'} />
        <Kpi label="Last Scan" value={lastJob ? timeAgo(lastJob.created_at) : '—'} icon={Clock} tone="default" sub={lastJob?.scope_cidr ?? undefined} />
        <Link to="/inventory?access=unmanaged" style={{ textDecoration: 'none' }}>
          <Kpi label="Credential Coverage" value={cov.data ? `${cov.data.coverage_percent}%` : '—'} icon={KeyRound}
            tone={cov.data ? (cov.data.coverage_percent >= 75 ? 'ok' : cov.data.coverage_percent >= 40 ? 'warn' : 'crit') : 'default'}
            sub={cov.data ? `${cov.data.managed_devices}/${cov.data.total_devices} managed · ${cov.data.unmanaged_devices} need creds` : undefined} />
        </Link>
      </div>

      <div className="card">
        <div className="seg">
          {TABS.map((t) => (
            <button key={t} className={'seg-chip' + (tab === t ? ' active' : '')} onClick={() => setTab(t)}>
              {t}{t === 'Jobs' && jobs.data ? ` (${jobs.data.length})` : ''}
            </button>
          ))}
        </div>
        {msg && <div className="muted" style={{ fontSize: 12, marginTop: 8 }}>{msg}</div>}
      </div>

      {tab === 'Network scan' && <NetworkScan locations={locations.data ?? []} locPath={locPath} creds={creds.data ?? []} onLaunch={afterLaunch} setMsg={setMsg} />}
      {tab === 'Import' && <ImportTab locations={locations.data ?? []} locPath={locPath} setMsg={setMsg} qc={qc} />}
      {tab === 'Controllers' && <ControllersTab locations={locations.data ?? []} locPath={locPath} onLaunch={afterLaunch} setMsg={setMsg} />}
      {tab === 'Active Directory' && <ADTab locations={locations.data ?? []} locPath={locPath} onLaunch={afterLaunch} setMsg={setMsg} />}
      {tab === 'Jobs' && <JobsTab jobs={jobs.data ?? []} jobID={jobID} setJobID={setJobID} detail={detail.data} setMsg={setMsg} qc={qc} />}
    </div>
  )
}

// ---------- Network scan ----------
function NetworkScan({ locations, locPath, creds, onLaunch, setMsg }: { locations: Location[]; locPath: Record<string, string>; creds: Credential[]; onLaunch: (j: DiscoveryJob) => void; setMsg: (s: string) => void }) {
  const [mode, setMode] = useState<ScanMode>('cidr')
  const [targets, setTargets] = useState('')
  const [location, setLocation] = useState('')
  const [credIDs, setCredIDs] = useState<string[]>([])
  const siteMode = mode === 'site_subnets'
  const canScan = siteMode ? !!location : !!targets.trim()

  const scan = useMutation({
    mutationFn: () => api.post<DiscoveryJob>('/discovery/scan', {
      mode: siteMode ? 'site_subnets' : 'targets', targets: siteMode ? '' : targets.trim(),
      location_id: location || null, credential_ids: credIDs,
    }),
    onSuccess: (j) => { setTargets(''); onLaunch(j as DiscoveryJob); setMsg('Scan launched — see Jobs.') },
    onError: (e) => setMsg((e as Error).message),
  })
  const toggleCred = (id: string) => setCredIDs((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]))

  // Preflight: what protocols we're equipped to authenticate with for this scope.
  const preflight = useQuery({
    queryKey: ['scan-preflight', location, credIDs.join(',')],
    queryFn: () => {
      const p = new URLSearchParams()
      if (location) p.set('location_id', location)
      if (credIDs.length) p.set('credential_ids', credIDs.join(','))
      return api.get<ScanPreflight>(`/discovery/scan-preflight?${p.toString()}`)
    },
  })

  return (
    <div className="card">
      <h3>Network scan</h3>
      <div style={{ display: 'flex', gap: 6, marginBottom: 10, flexWrap: 'wrap' }}>
        {(Object.keys(MODE_LABEL) as ScanMode[]).map((m) => (
          <button key={m} onClick={() => setMode(m)} style={{ ...ghost, ...(mode === m ? { background: '#1565c0', color: '#fff', borderColor: '#1565c0' } : {}) }}>{MODE_LABEL[m]}</button>
        ))}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        {!siteMode && <input style={{ ...input, width: 360 }} placeholder={MODE_PH[mode]} value={targets} onChange={(e) => setTargets(e.target.value)} />}
        <select style={{ ...input, width: 280 }} value={location} onChange={(e) => setLocation(e.target.value)}>
          <option value="">{siteMode ? 'Select a site / hotel…' : 'Site scope (optional)'}</option>
          {locations.map((l) => <option key={l.id} value={l.id}>{locPath[l.id]} ({l.kind})</option>)}
        </select>
        <button style={btn} disabled={!canScan || scan.isPending} onClick={() => scan.mutate()}>{scan.isPending ? 'Launching…' : 'Start scan'}</button>
      </div>
      {siteMode && <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>{MODE_PH.site_subnets}</div>}

      <div style={{ marginTop: 12 }}>
        <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Credentials to try</div>
        <CredentialPicker creds={creds} selected={credIDs} onChange={setCredIDs} toggle={toggleCred} />
      </div>

      {preflight.data && <ScanPreflightPanel pf={preflight.data} siteSelected={!!location} />}
    </div>
  )
}

// ScanPreflightPanel shows, before the scan runs, what protocols the operator can
// actually authenticate with (credential counts by kind + VMware/CCTV profiles)
// and warns about gaps — so a subnet of Windows PCs with no WinRM credential is
// flagged up front rather than producing a wall of auth_failed results.
function ScanPreflightPanel({ pf, siteSelected }: { pf: ScanPreflight; siteSelected: boolean }) {
  const c = pf.credential_counts || {}
  const chip = (label: string, n: number) => (
    <span key={label} style={{ display: 'inline-flex', gap: 5, alignItems: 'center', padding: '2px 9px', borderRadius: 12, fontSize: 12, background: n > 0 ? '#e3f2fd' : '#fdecea', color: n > 0 ? '#1565c0' : '#b71c1c', border: `1px solid ${n > 0 ? '#90caf9' : '#ef9a9a'}` }}>
      {label}<strong>{n}</strong>
    </span>
  )
  return (
    <div style={{ marginTop: 14, padding: 12, borderRadius: 8, background: 'var(--surface-2, #f7f9fc)', border: '1px solid #d6dee8' }}>
      <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 8 }}>Scan preflight — credentials available for this scope</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {chip('WinRM', c.winrm ?? 0)}
        {chip('SSH', c.ssh ?? 0)}
        {chip('SNMP', c.snmp ?? 0)}
        {chip('ONVIF', c.onvif ?? 0)}
        {chip('HTTP', c.http_basic ?? 0)}
        {chip('Vendor API', c.vendor_api ?? 0)}
        {chip('VMware profiles', pf.vmware_profiles)}
        {chip('CCTV profiles', pf.cctv_profiles)}
      </div>
      {pf.warnings.length > 0 && (
        <ul style={{ margin: '10px 0 0', paddingLeft: 18 }}>
          {pf.warnings.map((w, i) => (
            <li key={i} style={{ fontSize: 12, color: '#8a6d00', marginBottom: 2 }}>⚠ {w}</li>
          ))}
        </ul>
      )}
      {!siteSelected && <div className="muted" style={{ fontSize: 11, marginTop: 8 }}>Select a site to scope VMware/CCTV profile checks to that site.</div>}
    </div>
  )
}

// CredentialPicker — a compact multi-select dropdown for credentials (scales to
// many): a summary button opens a searchable, scrollable checkbox panel.
// Empty selection = auto-try ALL stored credentials.
function CredentialPicker({ creds, selected, onChange, toggle }: { creds: Credential[]; selected: string[]; onChange: (ids: string[]) => void; toggle: (id: string) => void }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const sel = new Set(selected)
  const shown = creds.filter((c) => !q.trim() || c.name.toLowerCase().includes(q.toLowerCase()) || c.kind.includes(q.toLowerCase()))
  const summary = selected.length === 0 ? 'All stored credentials (auto)' : `${selected.length} selected`

  if (creds.length === 0) return <span className="muted" style={{ fontSize: 12 }}>No credentials — add them on the Credentials page.</span>

  return (
    <div style={{ position: 'relative', maxWidth: 460 }}>
      <button onClick={() => setOpen((v) => !v)} style={{ ...input, width: '100%', textAlign: 'left', cursor: 'pointer', display: 'flex', justifyContent: 'space-between', alignItems: 'center', background: '#fff', color: '#222' }}>
        <span>{summary}</span><span style={{ opacity: 0.6 }}>{open ? '▲' : '▼'}</span>
      </button>

      {/* selected chips preview under the button */}
      {selected.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 4 }}>
          {selected.map((id) => { const c = creds.find((x) => x.id === id); return (
            <span key={id} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '1px 7px', borderRadius: 10, fontSize: 11, background: '#2e7d32', color: '#fff' }}>
              {c?.name ?? id}<span onClick={() => toggle(id)} style={{ cursor: 'pointer', fontWeight: 700 }}>×</span>
            </span>
          )})}
          <button onClick={() => onChange([])} style={{ ...ghost, fontSize: 11, padding: '1px 7px' }}>clear all</button>
        </div>
      )}

      {open && (
        <div style={{ position: 'absolute', zIndex: 20, top: '100%', left: 0, right: 0, marginTop: 4, background: '#fff', color: '#222', border: '1px solid #bbb', borderRadius: 8, boxShadow: '0 6px 20px rgba(0,0,0,.25)', padding: 8 }}>
          <div style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
            <input autoFocus style={{ ...input, flex: 1, background: '#fff', color: '#222' }} placeholder="search credentials…" value={q} onChange={(e) => setQ(e.target.value)} />
            <button style={pickerBtn} onClick={() => onChange(shown.map((c) => c.id))}>all</button>
            <button style={pickerBtn} onClick={() => onChange([])}>none</button>
          </div>
          <div style={{ maxHeight: 220, overflow: 'auto' }}>
            {shown.length === 0 && <div style={{ fontSize: 12, padding: 4, color: '#888' }}>No match.</div>}
            {shown.map((c) => (
              <label key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 6px', borderRadius: 6, cursor: 'pointer', background: sel.has(c.id) ? '#e3f2fd' : 'transparent' }}>
                <input type="checkbox" checked={sel.has(c.id)} onChange={() => toggle(c.id)} />
                <span style={{ flex: 1, color: '#222' }}>{c.name}</span>
                <span style={{ fontSize: 11, color: '#777' }}>{c.kind}{c.weak ? ' ⚠' : ''}</span>
              </label>
            ))}
          </div>
          <div style={{ fontSize: 11, marginTop: 6, color: '#777' }}>Leave empty to auto-try all. Selected creds are tried first.</div>
          <div style={{ textAlign: 'right', marginTop: 4 }}><button style={pickerBtn} onClick={() => setOpen(false)}>Done</button></div>
        </div>
      )}
    </div>
  )
}

// ---------- Import (manual + CSV) ----------
function ImportTab({ locations, locPath, setMsg, qc }: { locations: Location[]; locPath: Record<string, string>; setMsg: (s: string) => void; qc: ReturnType<typeof useQueryClient> }) {
  const [man, setMan] = useState({ name: '', category: 'unknown', primary_ip: '', vendor: '', model: '', serial: '', vlan: '', class: '', location_id: '' })
  const [csv, setCsv] = useState('')
  const refresh = () => qc.invalidateQueries({ queryKey: ['devices'] })
  const addManual = useMutation({
    mutationFn: () => api.post('/devices', { ...man, location_id: man.location_id || null }),
    onSuccess: () => { setMan({ name: '', category: 'unknown', primary_ip: '', vendor: '', model: '', serial: '', vlan: '', class: '', location_id: '' }); setMsg('Device added.'); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const importCsv = useMutation({
    mutationFn: () => api.postText<{ created: number; failed: number; errors?: string[] }>('/devices/import-csv', csv),
    onSuccess: (r) => { const x = r as { created: number; failed: number }; setMsg(`Import: ${x.created} created, ${x.failed} failed.`); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const importFile = useMutation({
    mutationFn: (file: File) => { const fd = new FormData(); fd.append('file', file); return api.postForm<{ created: number; failed: number; errors?: string[] }>('/devices/import-file', fd) },
    onSuccess: (r) => { const x = r as { created: number; failed: number }; setMsg(`Import: ${x.created} created, ${x.failed} failed.`); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })

  return (
    <>
      <div className="card">
        <h3>Manual add <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— a device that can't be auto-discovered</span></h3>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <input style={{ ...input, width: 160 }} placeholder="name *" value={man.name} onChange={(e) => setMan({ ...man, name: e.target.value })} />
          <select style={{ ...input, width: 140 }} value={man.category} onChange={(e) => setMan({ ...man, category: e.target.value })}>{CATEGORIES.map((c) => <option key={c}>{c}</option>)}</select>
          <input style={{ ...input, width: 120 }} placeholder="IP (opt)" value={man.primary_ip} onChange={(e) => setMan({ ...man, primary_ip: e.target.value })} />
          <input style={{ ...input, width: 110 }} placeholder="vendor" value={man.vendor} onChange={(e) => setMan({ ...man, vendor: e.target.value })} />
          <select style={{ ...input, width: 220 }} value={man.location_id} onChange={(e) => setMan({ ...man, location_id: e.target.value })}>
            <option value="">Location (optional)…</option>
            {locations.map((l) => <option key={l.id} value={l.id}>{locPath[l.id]}</option>)}
          </select>
          <button style={btn} disabled={!man.name.trim() || addManual.isPending} onClick={() => addManual.mutate()}>Add device</button>
        </div>
      </div>

      <div className="card">
        <h3>CSV import <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— bulk manual assets</span></h3>
        <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
          Header row + any subset of: <code>name</code> (required), category, primary_ip, hostname, vendor, model, serial, os_version, vlan, class, location (name or path). Paste below, or upload a .csv file.
        </div>
        <div style={{ marginBottom: 8 }}>
          <label style={{ ...btn, display: 'inline-block' }}>
            Upload .csv / .xlsx
            <input type="file" accept=".csv,.xlsx" style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) importFile.mutate(f); e.target.value = '' }} />
          </label>
          <span className="muted" style={{ fontSize: 12, marginLeft: 8 }}>{importFile.isPending ? 'Importing…' : 'Excel or CSV file — imported directly.'}</span>
        </div>
        <div className="muted" style={{ fontSize: 12, margin: '6px 0' }}>…or paste CSV:</div>
        <textarea style={{ ...input, width: '100%', minHeight: 80, fontFamily: 'monospace', fontSize: 12 }} placeholder={'name,category,primary_ip,location\nPatch Panel A,patch_panel,,CHR\nUPS-Lobby,ups,10.0.0.30,CHR'} value={csv} onChange={(e) => setCsv(e.target.value)} />
        <div style={{ marginTop: 6 }}>
          <button style={btn} disabled={!csv.trim() || importCsv.isPending} onClick={() => importCsv.mutate()}>{importCsv.isPending ? 'Importing…' : 'Import pasted CSV'}</button>
          {(importCsv.data?.errors || importFile.data?.errors) && (
            <ul className="muted" style={{ fontSize: 12, marginTop: 6 }}>{((importCsv.data?.errors || importFile.data?.errors) ?? []).slice(0, 10).map((x, i) => <li key={i}>{x}</li>)}</ul>
          )}
        </div>
      </div>
    </>
  )
}

// ---------- Controllers ----------
function ControllersTab({ locations, locPath, onLaunch, setMsg }: { locations: Location[]; locPath: Record<string, string>; onLaunch: (j: DiscoveryJob) => void; setMsg: (s: string) => void }) {
  const [c, setC] = useState({ kind: 'unifi', ip: '', omada_cid: '', cucm_version: '12.5', extreme_base: '', location_id: '' })
  const run = useMutation({
    mutationFn: () => api.post<DiscoveryJob>('/discovery/controller-import', { ...c, location_id: c.location_id || null }),
    onSuccess: (j) => { onLaunch(j as DiscoveryJob); setMsg('Controller import launched — see Jobs.') },
    onError: (e) => setMsg((e as Error).message),
  })
  return (
    <div className="card">
      <h3>Controller import <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— UniFi / Ruckus / Omada / Extreme / vSphere / Hyper-V / Redfish / ONVIF / CUCM</span></h3>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        <select style={{ ...input, width: 130 }} value={c.kind} onChange={(e) => setC({ ...c, kind: e.target.value })}>{CTRL_KINDS.map((k) => <option key={k}>{k}</option>)}</select>
        <input style={{ ...input, width: 150 }} placeholder="controller / host IP" value={c.ip} onChange={(e) => setC({ ...c, ip: e.target.value })} />
        {c.kind === 'omada' && <input style={{ ...input, width: 160 }} placeholder="omada controller id" value={c.omada_cid} onChange={(e) => setC({ ...c, omada_cid: e.target.value })} />}
        {c.kind === 'cucm' && <input style={{ ...input, width: 110 }} placeholder="AXL ver" value={c.cucm_version} onChange={(e) => setC({ ...c, cucm_version: e.target.value })} />}
        {c.kind === 'extreme' && <input style={{ ...input, width: 200 }} placeholder="XIQ base URL (opt)" value={c.extreme_base} onChange={(e) => setC({ ...c, extreme_base: e.target.value })} />}
        <select style={{ ...input, width: 200 }} value={c.location_id} onChange={(e) => setC({ ...c, location_id: e.target.value })}>
          <option value="">Site scope (optional)…</option>
          {locations.map((l) => <option key={l.id} value={l.id}>{locPath[l.id]}</option>)}
        </select>
        <button style={btn} disabled={!c.ip.trim() || run.isPending} onClick={() => run.mutate()}>Import</button>
      </div>
      <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>Credentials resolve from your stored set (http_basic / vendor_api / winrm / onvif). Runs as a background job.</div>
    </div>
  )
}

// ---------- Active Directory (connect → OU tree → import) ----------
type OU = { dn: string; name: string }
type ADBrowse = { base_dn: string; ous: OU[] }
// parent DN = the DN with its first RDN removed.
const parentDN = (dn: string) => dn.slice(dn.indexOf(',') + 1)

function ADTab({ locations, locPath, onLaunch, setMsg }: { locations: Location[]; locPath: Record<string, string>; onLaunch: (j: DiscoveryJob) => void; setMsg: (s: string) => void }) {
  const [host, setHost] = useState('')
  const [locId, setLocId] = useState('')
  const [tree, setTree] = useState<ADBrowse | null>(null)
  const [sel, setSel] = useState<Set<string>>(new Set())

  const connect = useMutation({
    mutationFn: () => api.post<ADBrowse>('/discovery/ad/browse', { dc_host: host.trim() }),
    onSuccess: (t) => { setTree(t as ADBrowse); setSel(new Set()); setMsg(`Connected — ${(t as ADBrowse).ous.length} OUs under ${(t as ADBrowse).base_dn}.`) },
    onError: (e) => { setTree(null); setMsg((e as Error).message) },
  })
  const importOUs = useMutation({
    mutationFn: async () => {
      let last: DiscoveryJob | null = null
      for (const dn of sel) last = await api.post<DiscoveryJob>('/discovery/ad-import', { dc_host: host.trim(), base_dn: dn, location_id: locId || null })
      return last
    },
    onSuccess: (j) => { if (j) onLaunch(j as DiscoveryJob); setMsg(`Launched AD import for ${sel.size} OU(s) — see Jobs.`) },
    onError: (e) => setMsg((e as Error).message),
  })

  // build children map by parent DN (roots = OUs whose parent isn't in the set)
  const dns = new Set((tree?.ous ?? []).map((o) => o.dn))
  const kids: Record<string, OU[]> = {}
  for (const o of tree?.ous ?? []) { const p = dns.has(parentDN(o.dn)) ? parentDN(o.dn) : '__root__'; (kids[p] ??= []).push(o) }
  const toggle = (dn: string) => setSel((s) => { const n = new Set(s); if (n.has(dn)) n.delete(dn); else n.add(dn); return n })

  const Node = ({ o, depth }: { o: OU; depth: number }) => (
    <div style={{ marginLeft: depth * 18 }}>
      <label style={{ display: 'flex', gap: 6, alignItems: 'center', padding: '2px 0', cursor: 'pointer' }}>
        <input type="checkbox" checked={sel.has(o.dn)} onChange={() => toggle(o.dn)} />
        <span>📁 {o.name}</span>
        <span className="muted" style={{ fontSize: 11 }}>{o.dn}</span>
      </label>
      {(kids[o.dn] ?? []).map((k) => <Node key={k.dn} o={k} depth={depth + 1} />)}
    </div>
  )

  return (
    <>
      <div className="card">
        <h3>Active Directory <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— connect, browse OUs, import computers</span></h3>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <input style={{ ...input, width: 220 }} placeholder="DC host (dc01.corp.local)" value={host} onChange={(e) => setHost(e.target.value)} />
          <button style={btn} disabled={!host.trim() || connect.isPending} onClick={() => connect.mutate()}>{connect.isPending ? 'Connecting…' : 'Connect'}</button>
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>Needs an <code>ldap</code> credential scoped to the DC's IP (Credentials page). Connect reads the directory root and lists its OUs.</div>
      </div>

      {tree && (
        <div className="card">
          <h3>OU tree <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— {tree.base_dn}</span></h3>
          {tree.ous.length === 0 && <div className="muted">No OUs found under the directory root.</div>}
          <div style={{ maxHeight: 360, overflow: 'auto', border: '1px solid #2a2a2a', borderRadius: 6, padding: 8 }}>
            {(kids['__root__'] ?? []).map((o) => <Node key={o.dn} o={o} depth={0} />)}
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10, flexWrap: 'wrap' }}>
            <span className="muted" style={{ fontSize: 12 }}>{sel.size} OU(s) selected → assign to</span>
            <select style={{ ...input, width: 220 }} value={locId} onChange={(e) => setLocId(e.target.value)}>
              <option value="">Site scope (optional)…</option>
              {locations.map((l) => <option key={l.id} value={l.id}>{locPath[l.id]}</option>)}
            </select>
            <button style={btn} disabled={sel.size === 0 || importOUs.isPending} onClick={() => importOUs.mutate()}>{importOUs.isPending ? 'Importing…' : `Import selected (${sel.size})`}</button>
          </div>
        </div>
      )}
    </>
  )
}

// ---------- Onboarding Actions ----------
// Derives the operational blocker buckets (Windows WinRM / Linux SSH / Network
// SNMP) straight from THIS job's scan results (probe_data) and turns each into an
// actionable card: count, sample devices, cause, next action, and a button wired
// to an EXISTING endpoint (credential test / re-scan). No new discovery features —
// just makes the current blockers operationally clear.
function OnboardingActions({ results, qc, setMsg, onRescan, rescanning }: {
  results: DiscoveryResult[]
  qc: ReturnType<typeof useQueryClient>
  setMsg: (s: string) => void
  onRescan: () => void
  rescanning: boolean
}) {
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const winrmCredIds = (creds.data ?? []).filter((c) => c.kind === 'winrm').map((c) => c.id)
  const sshCredIds = (creds.data ?? []).filter((c) => c.kind === 'ssh').map((c) => c.id)

  const test = useMutation({
    mutationFn: (v: { credIds: string[]; devIds: string[] }) =>
      api.post<{ successes: number; failures: number; pairs: number }>('/credentials/test', { credential_ids: v.credIds, device_ids: v.devIds }),
    onSuccess: (r) => { setMsg(`Credential test: ${r.successes} ok / ${r.failures} failed of ${r.pairs} pair(s). Check Credential Health on the devices.`); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
    onError: (e) => setMsg((e as Error).message),
  })

  // --- bucket helpers (read straight from probe_data, identical logic to the report) ---
  const att = (r: DiscoveryResult) => r.probe_data?.cred_attempts ?? []
  const ok = (r: DiscoveryResult, p: string) => att(r).some((a) => a.protocol === p && a.success) || (r.probe_data?.bound_cred ?? '').startsWith(p === 'snmp' ? 'snmp' : p)
  const cat = (r: DiscoveryResult, p: string, c: string) => att(r).some((a) => a.protocol === p && a.category === c)
  const portOpen = (r: DiscoveryResult, ...ps: number[]) => ps.some((p) => (r.probe_data?.open_ports ?? []).includes(p))
  const ips = (rs: DiscoveryResult[], n = 6) => rs.slice(0, n).map((r) => r.ip).join(', ') + (rs.length > n ? ` … (+${rs.length - n})` : '')
  const devIds = (rs: DiscoveryResult[]) => rs.map((r) => r.device_id).filter((x): x is string => !!x)

  const collector = useQuery({
    queryKey: ['native-collector-status'],
    queryFn: () => api.get<{ url_configured: boolean; token_configured: boolean }>('/discovery/native-collector-status'),
  })

  const win = results.filter((r) => r.probe_data?.candidate === 'windows')
  // Legacy WSMan 2.0: authentication succeeded but the WSMan operation faulted —
  // its OWN bucket, kept distinct from auth_failed / unreachable / disabled.
  const isLegacy = (r: DiscoveryResult) => cat(r, 'winrm', 'auth_ok_operation_fault')
  const winLegacy = win.filter(isLegacy)
  const winOk = win.filter((r) => !isLegacy(r) && ok(r, 'winrm'))
  const winAuth = win.filter((r) => !isLegacy(r) && !ok(r, 'winrm') && cat(r, 'winrm', 'auth_failed'))
  const winUnreach = win.filter((r) => !isLegacy(r) && !ok(r, 'winrm') && cat(r, 'winrm', 'unreachable'))
  const winClosed = win.filter((r) => !isLegacy(r) && !ok(r, 'winrm') && !cat(r, 'winrm', 'auth_failed') && !cat(r, 'winrm', 'unreachable') && !portOpen(r, 5985, 5986))

  const lin = results.filter((r) => r.probe_data?.candidate === 'linux')
  const linOk = lin.filter((r) => ok(r, 'ssh'))
  const linAuth = lin.filter((r) => !ok(r, 'ssh') && cat(r, 'ssh', 'auth_failed'))
  const linClosed = lin.filter((r) => !ok(r, 'ssh') && !cat(r, 'ssh', 'auth_failed'))

  const net = results.filter((r) => ['switch', 'router', 'firewall'].includes(r.category ?? ''))
  const netOk = net.filter((r) => ok(r, 'snmp'))

  type Card = {
    key: string; title: string; tone: 'ok' | 'warn' | 'crit'; count: number; sample: string
    cause: string; action: string; instructions?: React.ReactNode; button?: React.ReactNode
  }
  const cards: Card[] = []

  if (winOk.length) cards.push({ key: 'win-ok', title: 'Windows · WinRM working', tone: 'ok', count: winOk.length, sample: ips(winOk), cause: 'WinRM authenticated; deep OS inventory collected.', action: 'None — onboarded.' })
  if (winAuth.length) cards.push({
    key: 'win-auth', title: 'Windows · WinRM auth_failed', tone: 'warn', count: winAuth.length, sample: ips(winAuth),
    cause: 'WinRM reachable (5985 open) but the credential was rejected — wrong password or username format.',
    action: 'Set the WinRM credential username as UPN (dpm@coralsearesorts.com) OR NetBIOS (coralsearesorts\\dpm). NTLM often needs the NetBIOS form. Then retry.',
    button: <button style={ghost} disabled={!winrmCredIds.length || test.isPending} onClick={() => test.mutate({ credIds: winrmCredIds, devIds: devIds(winAuth) })}>{test.isPending ? 'Testing…' : 'Retry WinRM credential test'}</button>,
  })
  if (winUnreach.length) cards.push({
    key: 'win-unreach', title: 'Windows · WinRM unreachable', tone: 'warn', count: winUnreach.length, sample: ips(winUnreach),
    cause: '5985 looked open but the WinRM/WSMan handshake failed (service not listening or filtered).',
    action: 'Confirm the WinRM service is running and listener bound; then re-scan.',
    button: <button style={ghost} disabled={rescanning} onClick={onRescan}>{rescanning ? 'Re-scanning…' : 'Re-scan scope'}</button>,
  })
  if (winClosed.length) cards.push({
    key: 'win-closed', title: 'Windows · WinRM disabled / 5985 closed', tone: 'crit', count: winClosed.length, sample: ips(winClosed),
    cause: 'No WinRM evidence — port 5985/5986 is closed, so WinRM was not attempted. This is a host-configuration/firewall blocker, NOT a credential issue. No credential will help until WinRM is enabled.',
    action: 'Enable PowerShell Remoting (ideally fleet-wide via GPO), open the firewall, then re-scan.',
    instructions: (
      <div style={{ fontSize: 11, marginTop: 6 }}>
        <div className="muted">Per host (admin PowerShell):</div>
        <code style={codeBox}>Enable-PSRemoting -Force</code>
        <div className="muted" style={{ marginTop: 4 }}>Fleet-wide (GPO): Computer Config → Policies → Admin Templates → Windows Components → Windows Remote Management (WinRM) → WinRM Service → <em>Allow remote server management through WinRM = Enabled</em>; + Firewall inbound allow TCP 5985.</div>
        <div className="muted" style={{ marginTop: 4 }}>Test a host's port:</div>
        <code style={codeBox}>Test-NetConnection {winClosed[0]?.ip ?? '<ip>'} -Port 5985</code>
      </div>
    ),
    button: <button style={ghost} disabled={rescanning} onClick={onRescan}>{rescanning ? 'Re-scanning…' : 'Re-scan after enabling WinRM'}</button>,
  })

  if (linOk.length) cards.push({ key: 'lin-ok', title: 'Linux · SSH working', tone: 'ok', count: linOk.length, sample: ips(linOk), cause: 'SSH authenticated; OS inventory collected.', action: 'None — onboarded.' })
  if (linAuth.length) cards.push({
    key: 'lin-auth', title: 'Linux · SSH auth_failed', tone: 'warn', count: linAuth.length, sample: ips(linAuth),
    cause: 'Port 22 reachable but the SSH credential was rejected.',
    action: 'Verify the account allows SSH login (sshd_config: PermitRootLogin yes, PasswordAuthentication yes) and the password is current — or add another SSH credential, then retry.',
    button: (
      <span style={{ display: 'inline-flex', gap: 8 }}>
        <button style={ghost} disabled={!sshCredIds.length || test.isPending} onClick={() => test.mutate({ credIds: sshCredIds, devIds: devIds(linAuth) })}>{test.isPending ? 'Testing…' : 'Retry SSH credential test'}</button>
        <Link to="/credentials" style={{ ...ghost, textDecoration: 'none' }}>Add SSH credential</Link>
      </span>
    ),
  })
  if (linClosed.length) cards.push({ key: 'lin-closed', title: 'Linux · SSH unreachable / 22 closed', tone: 'warn', count: linClosed.length, sample: ips(linClosed), cause: 'Port 22 not reachable — SSH not attempted.', action: 'Enable sshd / open port 22, then re-scan.', button: <button style={ghost} disabled={rescanning} onClick={onRescan}>{rescanning ? 'Re-scanning…' : 'Re-scan scope'}</button> })

  if (netOk.length) cards.push({ key: 'net-ok', title: 'Network · SNMP working', tone: 'ok', count: netOk.length, sample: ips(netOk), cause: 'Switches/firewalls authenticated via SNMP; interfaces/topology collected.', action: 'Completed — no action.' })

  if (cards.length === 0 && winLegacy.length === 0) return null
  const toneColor = { ok: '#2e7d32', warn: '#8a6d00', crit: '#b71c1c' }

  return (
    <div className="card">
      <h3>Onboarding actions <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— what's blocking the rest of this scan, and how to fix it</span></h3>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 12 }}>
        {winLegacy.length > 0 && (
          <LegacyWindowsCard
            devs={winLegacy} sampleIPs={ips(winLegacy)} devIds={devIds(winLegacy)} firstIP={winLegacy[0]?.ip ?? ''}
            collectorConfigured={!!collector.data?.url_configured} tokenConfigured={!!collector.data?.token_configured}
            winrmCredIds={winrmCredIds} qc={qc} setMsg={setMsg} onRescan={onRescan} rescanning={rescanning}
          />
        )}
        {cards.map((c) => (
          <div key={c.key} style={{ border: `1px solid ${toneColor[c.tone]}44`, borderLeft: `4px solid ${toneColor[c.tone]}`, borderRadius: 8, padding: 12, background: 'var(--surface-2, #f7f9fc)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
              <strong style={{ fontSize: 13 }}>{c.title}</strong>
              <span className="badge" style={{ background: toneColor[c.tone], color: '#fff' }}>{c.count}</span>
            </div>
            <div className="mono muted" style={{ fontSize: 11, marginTop: 4 }}>{c.sample}</div>
            <div style={{ fontSize: 12, marginTop: 6 }}><span className="muted">Cause: </span>{c.cause}</div>
            <div style={{ fontSize: 12, marginTop: 4 }}><span className="muted">Next: </span>{c.action}</div>
            {c.instructions}
            {c.button && <div style={{ marginTop: 8 }}>{c.button}</div>}
          </div>
        ))}
      </div>
    </div>
  )
}

const codeBox: React.CSSProperties = { display: 'block', background: '#0d1b2a', color: '#9be7a0', padding: '4px 8px', borderRadius: 4, fontSize: 11, margin: '2px 0', overflowX: 'auto', whiteSpace: 'pre-wrap' }

// LegacyWindowsCard — the dedicated Onboarding bucket for Windows 7 / Server
// 2008 R2 hosts where WinRM AUTHENTICATED but the WSMan operation faulted
// (w:InvalidSelectors). Distinct from auth_failed / disabled / unreachable: the
// credential is valid and must NOT be reset. Routes to the Windows Native
// Collector / WMI fallback.
function LegacyWindowsCard({ devs, sampleIPs, devIds, firstIP, collectorConfigured, tokenConfigured, winrmCredIds, qc, setMsg, onRescan, rescanning }: {
  devs: DiscoveryResult[]; sampleIPs: string; devIds: string[]; firstIP: string
  collectorConfigured: boolean; tokenConfigured: boolean; winrmCredIds: string[]
  qc: ReturnType<typeof useQueryClient>; setMsg: (s: string) => void; onRescan: () => void; rescanning: boolean
}) {
  const [showGuide, setShowGuide] = useState(false)
  const tone = '#6a1b9a' // distinct purple — not a credential failure
  const firstDevId = devIds[0]

  const deployCmds = [
    '# 1) On a trusted Windows / domain box (elevated PowerShell):',
    "$env:HIMS_NATIVE_COLLECTOR_TOKEN = '<shared-secret>'",
    ".\\windows-native-collector.ps1 -Prefix 'http://+:8092/'",
    '',
    '# 2) On the HIMS host (then restart hims-api):',
    'HIMS_WINDOWS_NATIVE_COLLECTOR_URL=http://<collector-host>:8092/',
    'HIMS_WINDOWS_NATIVE_COLLECTOR_TOKEN=<shared-secret>',
  ].join('\n')

  const diagnose = useMutation({
    mutationFn: () => {
      if (!winrmCredIds.length) throw new Error('No WinRM credential configured.')
      if (!firstIP) throw new Error('No affected device IP.')
      return api.post<{ www_authenticate: string[]; modes: { mode: string; result: string; fault_code?: string }[] }>('/credentials/winrm-diagnose', { host: firstIP, credential_id: winrmCredIds[0] })
    },
    onSuccess: (d) => {
      const enc = d.modes.find((m) => m.mode === 'ntlm-encrypted')
      setMsg(`Diagnose ${firstIP}: schemes [${d.www_authenticate.join(', ')}] · ntlm-encrypted=${enc?.result}${enc?.fault_code ? ' (' + enc.fault_code + ')' : ''}`)
    },
    onError: (e) => setMsg((e as Error).message),
  })
  const testCollector = useMutation({
    mutationFn: () => api.post<{ configured: boolean; reachable: boolean; detail: string }>('/discovery/native-collector-test', {}),
    onSuccess: (r) => setMsg(`Windows Native Collector: ${r.reachable ? 'reachable' : 'NOT reachable'} — ${r.detail}`),
    onError: (e) => setMsg((e as Error).message),
  })
  const retryCollect = useMutation({
    mutationFn: async () => {
      let ok = 0, fail = 0
      for (const id of devIds) {
        try { const r = await api.post<{ status: string }>(`/devices/${id}/collect-os`, {}); if (r.status === 'collected') ok++; else fail++ } catch { fail++ }
      }
      return { ok, fail }
    },
    onSuccess: (r) => { setMsg(`Legacy re-collection: ${r.ok} collected, ${r.fail} still blocked.`); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
    onError: (e) => setMsg((e as Error).message),
  })

  return (
    <div style={{ border: `1px solid ${tone}55`, borderLeft: `4px solid ${tone}`, borderRadius: 8, padding: 12, background: 'var(--surface-2, #f7f9fc)', gridColumn: '1 / -1' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <strong style={{ fontSize: 13 }}>Legacy Windows / WSMan 2.0 incompatible</strong>
        <span className="badge" style={{ background: tone, color: '#fff' }}>{devs.length}</span>
      </div>
      <div className="mono muted" style={{ fontSize: 11, marginTop: 4 }}>{sampleIPs}</div>
      <div style={{ fontSize: 12, marginTop: 6 }}><span className="muted">Cause: </span>Authentication succeeded, but the host uses an older WSMan stack (Windows 7 / Server 2008 R2) that the Go WinRM collector cannot execute against (w:InvalidSelectors).</div>
      <div style={{ fontSize: 12, marginTop: 4 }}><span className="muted">Next: </span>Deploy the Windows Native Collector or configure a WMI/DCOM fallback.</div>
      <div style={{ fontSize: 12, marginTop: 4, color: tone }}><strong>Credential is probably valid — do not reset the password.</strong></div>

      {/* collector configured vs not */}
      <div style={{ marginTop: 8, fontSize: 12 }}>
        {collectorConfigured ? (
          <span className="badge badge-up">Collector configured</span>
        ) : (
          <div>
            <span className="badge badge-warning">Collector not configured</span>
            <div className="muted" style={{ fontSize: 11, marginTop: 4 }}>
              Missing environment {tokenConfigured ? 'variable' : 'variables'}:
              {!collectorConfigured && <code style={{ ...codeBox, display: 'inline-block', margin: '0 4px' }}>HIMS_WINDOWS_NATIVE_COLLECTOR_URL</code>}
              {!tokenConfigured && <code style={{ ...codeBox, display: 'inline-block', margin: '0 4px' }}>HIMS_WINDOWS_NATIVE_COLLECTOR_TOKEN</code>}
            </div>
          </div>
        )}
      </div>

      {/* action buttons */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 10 }}>
        <button style={ghost} onClick={() => setShowGuide((v) => !v)}>{showGuide ? 'Hide guide' : 'Open Windows Native Collector guide'}</button>
        <button style={ghost} onClick={() => { navigator.clipboard?.writeText(deployCmds); setMsg('Deployment commands copied to clipboard.') }}>Copy deployment commands</button>
        {firstDevId && <Link to={`/devices/${firstDevId}`} style={{ ...ghost, textDecoration: 'none' }}>Open affected devices</Link>}
        <button style={ghost} disabled={diagnose.isPending} onClick={() => diagnose.mutate()}>{diagnose.isPending ? 'Diagnosing…' : 'Retry Diagnose WinRM'}</button>
        <button style={ghost} disabled={rescanning} onClick={onRescan}>{rescanning ? 'Re-scanning…' : 'Re-scan after collector deployment'}</button>
        {collectorConfigured && <button style={ghost} disabled={testCollector.isPending} onClick={() => testCollector.mutate()}>{testCollector.isPending ? 'Testing…' : 'Test collector'}</button>}
        {collectorConfigured && <button style={ghost} disabled={retryCollect.isPending || !devIds.length} onClick={() => retryCollect.mutate()}>{retryCollect.isPending ? 'Collecting…' : 'Retry collection for affected devices'}</button>}
      </div>

      {showGuide && (
        <div style={{ fontSize: 11, marginTop: 8 }}>
          <div className="muted">Deploy the read-only PowerShell helper (<code>deploy/windows-native-collector.ps1</code>) on a Windows/domain box, then point HIMS at it. It runs native Invoke-Command (which already works on these hosts) and returns inventory to HIMS.</div>
          <code style={codeBox}>{deployCmds}</code>
          <div className="muted">Alternative: a WMI/DCOM fallback collector for the legacy fleet (the classic management path for Windows 7 / 2008 R2).</div>
        </div>
      )}
    </div>
  )
}

// ---------- Jobs ----------
function JobsTab({ jobs, jobID, setJobID, detail, setMsg, qc }: { jobs: DiscoveryJob[]; jobID: string | null; setJobID: (s: string | null) => void; detail?: { job: DiscoveryJob; results: DiscoveryResult[] }; setMsg: (s: string) => void; qc: ReturnType<typeof useQueryClient> }) {
  const refresh = () => qc.invalidateQueries({ queryKey: ['discovery-jobs'] })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/discovery/jobs/${id}`), onSuccess: () => { setJobID(null); setMsg('Job deleted.'); refresh() }, onError: (e) => setMsg((e as Error).message) })
  const rerun = useMutation({ mutationFn: (id: string) => api.post(`/discovery/jobs/${id}/rerun`, {}), onSuccess: () => { setMsg('Re-run launched.'); refresh() }, onError: (e) => setMsg((e as Error).message) })

  return (
    <>
      <div className="card">
        <h3>Scan jobs</h3>
        {jobs.length === 0 && <div className="muted">No jobs yet.</div>}
        {jobs.length > 0 && (
          <table>
            <thead><tr><th>Scope</th><th>Status</th><th>Hosts</th><th>Found</th><th>Duration</th><th>Started</th><th></th></tr></thead>
            <tbody>
              {jobs.map((j) => (
                <tr key={j.id} style={jobID === j.id ? { background: '#1a2733' } : {}}>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{j.scope_cidr ?? '—'}</td>
                  <td><span className={`badge badge-${jobBadge(j.status)}`}>{j.status}</span></td>
                  <td>{j.host_count}</td>
                  <td>{j.found_count}</td>
                  <td>{duration(j.started_at, j.finished_at)}</td>
                  <td>{j.started_at ? new Date(j.started_at).toLocaleTimeString() : '—'}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <button style={ghost} onClick={() => setJobID(j.id)}>Results</button>{' '}
                    {j.scope_cidr && <button style={ghost} disabled={rerun.isPending} onClick={() => rerun.mutate(j.id)}>Rerun</button>}{' '}
                    <button style={danger} onClick={() => { if (confirm('Delete this job and its results?')) del.mutate(j.id) }}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {jobID && detail && detail.results.length > 0 && (
        <OnboardingActions results={detail.results} qc={qc} setMsg={setMsg} onRescan={() => rerun.mutate(jobID)} rescanning={rerun.isPending} />
      )}

      {jobID && detail && (
        <div className="card">
          <h3>Results — {detail.job.scope_cidr ?? 'import'} <span className={`badge badge-${jobBadge(detail.job.status)}`}>{detail.job.status}</span></h3>
          {detail.job.error && <div className="error-msg" style={{ fontSize: 12 }}>{detail.job.error}</div>}
          {detail.results.length === 0 && <div className="muted">No host results recorded{detail.job.status === 'running' ? ' yet (scanning…)' : ''}.</div>}
          {detail.results.length > 0 && (
            <table>
              <thead><tr>
                <th>IP</th><th>Outcome</th><th>Classification</th><th>Ports</th>
                <th>Credentials tried</th><th>Bound</th><th>Vendor profile</th><th>Enrichment</th><th>Next action</th>
              </tr></thead>
              <tbody>
                {detail.results.map((r) => {
                  const d = r.probe_data ?? {}
                  return (
                    <tr key={r.id}>
                      <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{r.ip}</td>
                      <td><span className={`badge badge-${outcomeBadge(r.outcome)}`}>{r.outcome}</span></td>
                      <td>
                        {(r.category ?? d.classification ?? 'unknown')}
                        {typeof d.confidence === 'number' && d.confidence > 0 && <span className="muted" style={{ fontSize: 11 }}> · {d.confidence}%</span>}
                        {d.candidate && <div className="muted" style={{ fontSize: 11 }}>candidate: {d.candidate}</div>}
                        {(d.expected_protocols ?? []).length > 0 && <div style={{ fontSize: 11 }}>expected: <strong>{(d.expected_protocols ?? []).join(' / ').toUpperCase()}</strong></div>}
                        {(d.evidence ?? []).length > 0 && <div className="muted" style={{ fontSize: 11 }}>{(d.evidence ?? []).join(' · ')}</div>}
                      </td>
                      <td className="muted" style={{ fontSize: 11 }}>{(d.open_ports ?? []).join(', ') || '—'}</td>
                      <td style={{ fontSize: 11 }}>
                        {(d.cred_attempts ?? []).length === 0 ? <span className="muted">none relevant</span> : (d.cred_attempts ?? []).map((a, i) => (
                          <div key={i}>
                            <span className={`badge badge-${a.success ? 'up' : a.category === 'auth_failed' ? 'down' : 'unknown'}`}>{a.kind}</span>
                            <span className="muted"> {a.success ? 'ok' : a.category}{a.relevant ? '' : ' (other)'}</span>
                          </div>
                        ))}
                        {(d.skipped_protocols ?? []).length > 0 && (
                          <div className="muted" style={{ fontSize: 10, marginTop: 2 }}>n/a: {(d.skipped_protocols ?? []).join(', ')}</div>
                        )}
                      </td>
                      <td>{d.bound_cred ? <span className="badge badge-up">{d.bound_cred}</span> : <span className="muted">—</span>}</td>
                      <td style={{ fontSize: 11, minWidth: 160 }}><ProfileCell r={r} qc={qc} jobID={jobID} /></td>
                      <td className="muted" style={{ fontSize: 11 }}>{d.enrichment || '—'}</td>
                      <td style={{ fontSize: 12 }}>{r.error ? <span className="error-msg">{r.error}</span> : (d.next_action ?? '—')}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      )}
    </>
  )
}
