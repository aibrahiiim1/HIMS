import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type DiscoveryJob, type DiscoveryResult } from '../api'

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
  const [cidr, setCidr] = useState('')
  const [location, setLocation] = useState('')
  const [jobID, setJobID] = useState<string | null>(null)

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

  const scan = useMutation({
    mutationFn: () => api.post<DiscoveryJob>('/discovery/scan', { cidr, location_id: location || null }),
    onSuccess: (j) => { setCidr(''); setJobID((j as DiscoveryJob).id); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) },
  })

  return (
    <div>
      <div className="card">
        <h2>Discovery</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Scan a subnet → each reachable host is fingerprinted, authenticated against scoped
          credentials, collected, and persisted to the CMDB. Runs in the background; this page
          polls for progress.
        </p>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <input style={{ ...input, width: 200 }} placeholder="CIDR (e.g. 172.21.96.0/24)" value={cidr} onChange={(e) => setCidr(e.target.value)} />
          <input style={{ ...input, width: 260 }} placeholder="location UUID (optional)" value={location} onChange={(e) => setLocation(e.target.value)} />
          <button style={btn} disabled={!cidr || scan.isPending} onClick={() => scan.mutate()}>
            {scan.isPending ? 'Launching…' : 'Start scan'}
          </button>
          {scan.error && <span className="error-msg">{(scan.error as Error).message}</span>}
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
