import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type DiscoveryJob, type DiscoveryResult, type Location, type Credential } from '../api'

type ScanMode = 'single' | 'range' | 'cidr' | 'site_subnets'

const MODE_LABEL: Record<ScanMode, string> = {
  single: 'Single IP',
  range: 'IP Range',
  cidr: 'Subnet / CIDR',
  site_subnets: 'Hotel Site Subnets',
}
const MODE_PLACEHOLDER: Record<ScanMode, string> = {
  single: '10.20.0.10',
  range: '172.21.96.1-172.21.96.254  (or 172.21.96.1-254)',
  cidr: '172.21.96.0/24',
  site_subnets: '(uses every subnet bound to the selected site)',
}
// Mirrors the devices.category CHECK constraint (manual/CSV must use a valid one).
const CATEGORIES = [
  'unknown', 'switch', 'router', 'firewall', 'access_point', 'wireless_controller',
  'server', 'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer',
  'ip_phone', 'pbx', 'voice_gateway', 'database', 'directory', 'dns', 'dhcp',
  'fingerprint', 'endpoint', 'ups', 'isp_router', 'application',
]

const jobBadge = (s: string) =>
  s === 'running' ? 'warning' : s === 'completed' ? 'up' : s === 'failed' || s === 'cancelled' ? 'down' : 'unknown'
const outcomeBadge = (o: string) =>
  o === 'enrolled' ? 'up' : o === 'failed' ? 'down' : o === 'classified' ? 'access' : 'unknown'

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 10px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13,
}

export function Discovery() {
  const qc = useQueryClient()
  const [mode, setMode] = useState<ScanMode>('cidr')
  const [targets, setTargets] = useState('')
  const [location, setLocation] = useState('')
  const [credIDs, setCredIDs] = useState<string[]>([])
  const [jobID, setJobID] = useState<string | null>(null)

  const locations = useQuery({ queryKey: ['locations'], queryFn: () => api.get<Location[]>('/locations') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  const jobs = useQuery({
    queryKey: ['discovery-jobs'],
    queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'),
    refetchInterval: 5000, // poll so running scans update live
  })
  const detail = useQuery({
    queryKey: ['discovery-job', jobID],
    queryFn: () => api.get<{ job: DiscoveryJob; results: DiscoveryResult[] }>(`/discovery/jobs/${jobID}`),
    enabled: !!jobID,
    refetchInterval: 5000,
  })

  const siteMode = mode === 'site_subnets'
  const canScan = siteMode ? !!location : !!targets.trim()

  const scan = useMutation({
    mutationFn: () =>
      api.post<DiscoveryJob>('/discovery/scan', {
        mode: siteMode ? 'site_subnets' : 'targets',
        targets: siteMode ? '' : targets.trim(),
        location_id: location || null,
        credential_ids: credIDs,
      }),
    onSuccess: (j) => { setTargets(''); setJobID((j as DiscoveryJob).id); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) },
  })

  const toggleCred = (id: string) =>
    setCredIDs((prev) => (prev.includes(id) ? prev.filter((g) => g !== id) : [...prev, id]))

  // --- Manual Add + CSV Import (non-discoverable / bulk assets) ---
  const [man, setMan] = useState({ name: '', category: 'unknown', primary_ip: '', vendor: '', model: '', serial: '', vlan: '', class: '', location: '' })
  const [csv, setCsv] = useState('')
  const addManual = useMutation({
    mutationFn: () => api.post('/devices', { ...man, location_id: location || null }),
    onSuccess: () => { setMan({ name: '', category: 'unknown', primary_ip: '', vendor: '', model: '', serial: '', vlan: '', class: '', location: '' }); qc.invalidateQueries({ queryKey: ['devices'] }) },
  })
  const importCsv = useMutation({
    mutationFn: () => api.postText<{ created: number; failed: number; errors?: string[] }>('/devices/import-csv', csv),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['devices'] }),
  })

  // --- Controller import (UniFi/Ruckus/Omada/Extreme/vSphere/Hyper-V/Redfish/ONVIF/CUCM) ---
  const [ctrl, setCtrl] = useState({ kind: 'unifi', ip: '', omada_cid: '', cucm_version: '12.5', extreme_base: '' })
  const importCtrl = useMutation({
    mutationFn: () => api.post<DiscoveryJob>('/discovery/controller-import', { ...ctrl, location_id: location || null }),
    onSuccess: (j) => { setJobID((j as DiscoveryJob).id); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) },
  })

  // --- AD import (computers from a selected OU subtree) ---
  const [ad, setAd] = useState({ dc_host: '', base_dn: '' })
  const importAd = useMutation({
    mutationFn: () => api.post<DiscoveryJob>('/discovery/ad-import', { ...ad, location_id: location || null }),
    onSuccess: (j) => { setJobID((j as DiscoveryJob).id); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) },
  })

  return (
    <div>
      <div className="card">
        <h2>Discovery</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Pick an input mode → each reachable host is fingerprinted, authenticated against scoped
          credentials, collected, and persisted to the CMDB. Runs in the background; this page
          polls for progress.
        </p>

        {/* Input-mode selector */}
        <div style={{ display: 'flex', gap: 6, marginBottom: 10, flexWrap: 'wrap' }}>
          {(Object.keys(MODE_LABEL) as ScanMode[]).map((m) => (
            <button
              key={m}
              onClick={() => setMode(m)}
              style={{
                ...ghost,
                ...(mode === m ? { background: '#1565c0', color: '#fff', borderColor: '#1565c0' } : {}),
              }}
            >
              {MODE_LABEL[m]}
            </button>
          ))}
        </div>

        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          {!siteMode && (
            <input
              style={{ ...input, width: 360 }}
              placeholder={MODE_PLACEHOLDER[mode]}
              value={targets}
              onChange={(e) => setTargets(e.target.value)}
            />
          )}
          {/* Site selector — optional for IP modes (credential scope), required for site_subnets */}
          <select style={{ ...input, width: 220 }} value={location} onChange={(e) => setLocation(e.target.value)}>
            <option value="">{siteMode ? 'Select a hotel site…' : 'Site scope (optional)'}</option>
            {(locations.data ?? []).map((l) => (
              <option key={l.id} value={l.id}>{l.name}</option>
            ))}
          </select>
          <button style={btn} disabled={!canScan || scan.isPending} onClick={() => scan.mutate()}>
            {scan.isPending ? 'Launching…' : 'Start scan'}
          </button>
          {scan.error && <span className="error-msg">{(scan.error as Error).message}</span>}
        </div>
        {siteMode && <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>{MODE_PLACEHOLDER.site_subnets}</div>}

        {/* Optional per-credential multi-select. None selected = try ALL. */}
        <div style={{ marginTop: 12 }}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
            Credentials to use ({credIDs.length > 0 ? `${credIDs.length} selected` : 'none selected — auto-tries ALL stored credentials'})
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
            {(creds.data ?? []).length === 0 && <span className="muted" style={{ fontSize: 12 }}>No credentials stored — add some on the Credentials page.</span>}
            {(creds.data ?? []).map((c) => (
              <button
                key={c.id}
                onClick={() => toggleCred(c.id)}
                title={c.weak ? 'weak / default secret' : c.kind}
                style={{
                  ...ghost,
                  ...(credIDs.includes(c.id) ? { background: '#2e7d32', color: '#fff', borderColor: '#2e7d32' } : {}),
                }}
              >
                {c.name} <span style={{ opacity: 0.7 }}>({c.kind}{c.weak ? ' ⚠' : ''})</span>
              </button>
            ))}
            {credIDs.length > 0 && (
              <button onClick={() => setCredIDs([])} style={{ ...ghost, borderColor: '#888', color: '#aaa' }}>clear</button>
            )}
          </div>
        </div>
      </div>

      <div className="card">
        <h3>Manual add <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— a device that can't be auto-discovered</span></h3>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <input style={{ ...input, width: 180 }} placeholder="name *" value={man.name} onChange={(e) => setMan({ ...man, name: e.target.value })} />
          <select style={{ ...input, width: 150 }} value={man.category} onChange={(e) => setMan({ ...man, category: e.target.value })}>
            {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
          <input style={{ ...input, width: 130 }} placeholder="primary IP (opt)" value={man.primary_ip} onChange={(e) => setMan({ ...man, primary_ip: e.target.value })} />
          <input style={{ ...input, width: 120 }} placeholder="vendor" value={man.vendor} onChange={(e) => setMan({ ...man, vendor: e.target.value })} />
          <input style={{ ...input, width: 120 }} placeholder="model" value={man.model} onChange={(e) => setMan({ ...man, model: e.target.value })} />
          <input style={{ ...input, width: 120 }} placeholder="serial" value={man.serial} onChange={(e) => setMan({ ...man, serial: e.target.value })} />
          <input style={{ ...input, width: 80 }} placeholder="vlan" value={man.vlan} onChange={(e) => setMan({ ...man, vlan: e.target.value })} />
          <input style={{ ...input, width: 110 }} placeholder="class" value={man.class} onChange={(e) => setMan({ ...man, class: e.target.value })} />
          <input style={{ ...input, width: 130 }} placeholder="location" value={man.location} onChange={(e) => setMan({ ...man, location: e.target.value })} />
          <button style={btn} disabled={!man.name.trim() || addManual.isPending} onClick={() => addManual.mutate()}>
            {addManual.isPending ? 'Adding…' : 'Add device'}
          </button>
          {addManual.error && <span className="error-msg">{(addManual.error as Error).message}</span>}
          {addManual.isSuccess && <span className="badge badge-up">added</span>}
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>Uses the Site dropdown above for location scope. Stamped <code>source=manual</code>.</div>
      </div>

      <div className="card">
        <h3>CSV import <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— bulk manual assets</span></h3>
        <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
          Paste CSV with a header row. Columns (any subset): <code>name</code> (required), category, primary_ip, hostname, vendor, model, serial, os_version, location_id.
        </div>
        <textarea
          style={{ ...input, width: '100%', minHeight: 90, fontFamily: 'monospace', fontSize: 12 }}
          placeholder={'name,category,primary_ip,vendor\nPatch Panel A,patch_panel,,Generic\nUPS-Lobby,ups,10.0.0.30,APC'}
          value={csv}
          onChange={(e) => setCsv(e.target.value)}
        />
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 6 }}>
          <button style={btn} disabled={!csv.trim() || importCsv.isPending} onClick={() => importCsv.mutate()}>
            {importCsv.isPending ? 'Importing…' : 'Import CSV'}
          </button>
          {importCsv.error && <span className="error-msg">{(importCsv.error as Error).message}</span>}
          {importCsv.data && (
            <span className={`badge badge-${importCsv.data.failed ? 'warning' : 'up'}`}>
              {importCsv.data.created} created{importCsv.data.failed ? `, ${importCsv.data.failed} failed` : ''}
            </span>
          )}
        </div>
        {importCsv.data?.errors && importCsv.data.errors.length > 0 && (
          <ul className="muted" style={{ fontSize: 12, marginTop: 6 }}>
            {importCsv.data.errors.slice(0, 8).map((e, i) => <li key={i}>{e}</li>)}
          </ul>
        )}
      </div>

      <div className="card">
        <h3>Controller import <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— UniFi / Ruckus / Omada / Extreme / vSphere / Hyper-V / Redfish / ONVIF / CUCM</span></h3>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <select style={{ ...input, width: 150 }} value={ctrl.kind} onChange={(e) => setCtrl({ ...ctrl, kind: e.target.value })}>
            {['unifi', 'ruckus', 'omada', 'extreme', 'vsphere', 'hyperv', 'redfish', 'onvif', 'cucm'].map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
          <input style={{ ...input, width: 150 }} placeholder="controller / host IP" value={ctrl.ip} onChange={(e) => setCtrl({ ...ctrl, ip: e.target.value })} />
          {ctrl.kind === 'omada' && <input style={{ ...input, width: 160 }} placeholder="omada controller id" value={ctrl.omada_cid} onChange={(e) => setCtrl({ ...ctrl, omada_cid: e.target.value })} />}
          {ctrl.kind === 'cucm' && <input style={{ ...input, width: 110 }} placeholder="AXL ver (12.5)" value={ctrl.cucm_version} onChange={(e) => setCtrl({ ...ctrl, cucm_version: e.target.value })} />}
          {ctrl.kind === 'extreme' && <input style={{ ...input, width: 220 }} placeholder="XIQ base URL (optional)" value={ctrl.extreme_base} onChange={(e) => setCtrl({ ...ctrl, extreme_base: e.target.value })} />}
          <button style={btn} disabled={!ctrl.ip.trim() || importCtrl.isPending} onClick={() => importCtrl.mutate()}>
            {importCtrl.isPending ? 'Launching…' : 'Import controller'}
          </button>
          {importCtrl.error && <span className="error-msg">{(importCtrl.error as Error).message}</span>}
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>Credentials resolve from the scoped groups (Site dropdown above sets location scope). Runs as a background job below.</div>
      </div>

      <div className="card">
        <h3>AD import <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>— computers from a selected OU subtree (LDAP)</span></h3>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <input style={{ ...input, width: 200 }} placeholder="DC host (dc01.corp.local)" value={ad.dc_host} onChange={(e) => setAd({ ...ad, dc_host: e.target.value })} />
          <input style={{ ...input, width: 320 }} placeholder="base DN (OU=HotelA,DC=corp,DC=local)" value={ad.base_dn} onChange={(e) => setAd({ ...ad, base_dn: e.target.value })} />
          <button style={btn} disabled={!ad.dc_host.trim() || !ad.base_dn.trim() || importAd.isPending} onClick={() => importAd.mutate()}>
            {importAd.isPending ? 'Launching…' : 'Import from AD'}
          </button>
          {importAd.error && <span className="error-msg">{(importAd.error as Error).message}</span>}
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>Needs an <code>ldap</code> credential scoped to the DC. Runs as a background job; "hosts" = computers found, "found" = imported.</div>
      </div>

      <div className="card">
        <h3>Scan jobs</h3>
        {jobs.data && jobs.data.length === 0 && <div className="muted">No scans yet.</div>}
        {jobs.data && jobs.data.length > 0 && (
          <table>
            <thead><tr><th>Scope</th><th>Status</th><th>Hosts</th><th>Found</th><th>Started</th><th></th></tr></thead>
            <tbody>
              {jobs.data.map((j) => (
                <tr key={j.id}>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{j.scope_cidr ?? '—'}</td>
                  <td><span className={`badge badge-${jobBadge(j.status)}`}>{j.status}</span></td>
                  <td>{j.host_count}</td>
                  <td>{j.found_count}</td>
                  <td>{j.started_at ? new Date(j.started_at).toLocaleTimeString() : '—'}</td>
                  <td><button style={ghost} onClick={() => setJobID(j.id)}>Results</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {jobID && detail.data && (
        <div className="card">
          <h3>Results — {detail.data.job.scope_cidr} <span className={`badge badge-${jobBadge(detail.data.job.status)}`}>{detail.data.job.status}</span></h3>
          {detail.data.results.length === 0 && (
            <div className="muted">No reachable hosts recorded yet{detail.data.job.status === 'running' ? ' (scanning…)' : ''}.</div>
          )}
          {detail.data.results.length > 0 && (
            <table>
              <thead><tr><th>IP</th><th>Outcome</th><th>Driver</th><th>Category</th><th>Error</th></tr></thead>
              <tbody>
                {detail.data.results.map((r) => (
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
    </div>
  )
}
