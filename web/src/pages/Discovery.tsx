import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type DiscoveryJob, type DiscoveryResult, type Location, type CredentialGroup } from '../api'

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
  const [groupIDs, setGroupIDs] = useState<string[]>([])
  const [jobID, setJobID] = useState<string | null>(null)

  const locations = useQuery({ queryKey: ['locations'], queryFn: () => api.get<Location[]>('/locations') })
  const groups = useQuery({ queryKey: ['credential-groups'], queryFn: () => api.get<CredentialGroup[]>('/credential-groups') })

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
        credential_group_ids: groupIDs,
      }),
    onSuccess: (j) => { setTargets(''); setJobID((j as DiscoveryJob).id); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) },
  })

  const toggleGroup = (id: string) =>
    setGroupIDs((prev) => (prev.includes(id) ? prev.filter((g) => g !== id) : [...prev, id]))

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

        {/* Optional credential-group multi-select */}
        <div style={{ marginTop: 12 }}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
            Credential groups to use (optional — default: site/subnet auto-resolution)
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {(groups.data ?? []).length === 0 && <span className="muted" style={{ fontSize: 12 }}>No credential groups defined.</span>}
            {(groups.data ?? []).map((g) => (
              <button
                key={g.id}
                onClick={() => toggleGroup(g.id)}
                title={`${g.member_count} credential(s), ${g.binding_count} binding(s)`}
                style={{
                  ...ghost,
                  ...(groupIDs.includes(g.id) ? { background: '#2e7d32', color: '#fff', borderColor: '#2e7d32' } : {}),
                }}
              >
                {g.name} <span style={{ opacity: 0.7 }}>({g.member_count})</span>
              </button>
            ))}
          </div>
        </div>
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
