import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  api, locationPaths,
  type DiscoveryJob, type DiscoveryResult, type Location, type Credential,
} from '../api'

// ---------- shared styles ----------
const btn: React.CSSProperties = { padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600 }
const ghost: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: '#ef9a9a', border: '1px solid #ef9a9a', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const input: React.CSSProperties = { padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13 }

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

  return (
    <div>
      <div className="card">
        <h2>Discovery</h2>
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {TABS.map((t) => (
            <button key={t} onClick={() => setTab(t)} style={{ ...ghost, ...(tab === t ? { background: '#1565c0', color: '#fff', borderColor: '#1565c0' } : {}) }}>
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
        <div style={{ position: 'absolute', zIndex: 20, top: '100%', left: 0, right: 0, marginTop: 4, background: '#1b1b1b', border: '1px solid #444', borderRadius: 8, boxShadow: '0 6px 20px rgba(0,0,0,.4)', padding: 8 }}>
          <div style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
            <input autoFocus style={{ ...input, flex: 1 }} placeholder="search credentials…" value={q} onChange={(e) => setQ(e.target.value)} />
            <button style={ghost} onClick={() => onChange(shown.map((c) => c.id))}>all</button>
            <button style={ghost} onClick={() => onChange([])}>none</button>
          </div>
          <div style={{ maxHeight: 220, overflow: 'auto' }}>
            {shown.length === 0 && <div className="muted" style={{ fontSize: 12, padding: 4 }}>No match.</div>}
            {shown.map((c) => (
              <label key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 6px', borderRadius: 6, cursor: 'pointer', background: sel.has(c.id) ? '#223' : 'transparent' }}>
                <input type="checkbox" checked={sel.has(c.id)} onChange={() => toggle(c.id)} />
                <span style={{ flex: 1 }}>{c.name}</span>
                <span className="muted" style={{ fontSize: 11 }}>{c.kind}{c.weak ? ' ⚠' : ''}</span>
              </label>
            ))}
          </div>
          <div className="muted" style={{ fontSize: 11, marginTop: 6 }}>Leave empty to auto-try all. Selected creds are tried first.</div>
          <div style={{ textAlign: 'right', marginTop: 4 }}><button style={ghost} onClick={() => setOpen(false)}>Done</button></div>
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
  const toggle = (dn: string) => setSel((s) => { const n = new Set(s); n.has(dn) ? n.delete(dn) : n.add(dn); return n })

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

      {jobID && detail && (
        <div className="card">
          <h3>Results — {detail.job.scope_cidr ?? 'import'} <span className={`badge badge-${jobBadge(detail.job.status)}`}>{detail.job.status}</span></h3>
          {detail.job.error && <div className="error-msg" style={{ fontSize: 12 }}>{detail.job.error}</div>}
          {detail.results.length === 0 && <div className="muted">No host results recorded{detail.job.status === 'running' ? ' yet (scanning…)' : ''}.</div>}
          {detail.results.length > 0 && (
            <table>
              <thead><tr><th>IP</th><th>Outcome</th><th>Driver</th><th>Category</th><th>Error</th></tr></thead>
              <tbody>
                {detail.results.map((r) => (
                  <tr key={r.id}>
                    <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{r.ip}</td>
                    <td><span className={`badge badge-${outcomeBadge(r.outcome)}`}>{r.outcome}</span></td>
                    <td>{r.driver ?? '—'}</td>
                    <td>{r.category ?? '—'}</td>
                    <td className="muted" style={{ fontSize: 12 }}>{r.error ?? ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </>
  )
}
