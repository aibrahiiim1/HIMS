import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { Radar, Boxes, CircleX, Clock, ListChecks, RefreshCw, Trash2 } from 'lucide-react'
import { api, locationPaths, type DiscoveryJob, type Location } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'
import { jobBadge, duration } from './Discovery'

// ScanResultsRedirect powers the "Scan Results" nav item: it opens the newest
// job's full Results page directly, or falls back to the jobs list if none exist.
export function ScanResultsRedirect() {
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs') })
  if (jobs.isLoading) return <div className="loading">Loading latest scan…</div>
  const latest = [...(jobs.data ?? [])].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0]
  return <Navigate to={latest ? `/discovery/jobs/${latest.id}/results` : '/discovery/jobs'} replace />
}

// Standalone Scan Jobs page — a full operational view, not a Discovery tab.
// Per-device managed/unmanaged/missing breakdowns live on the Job Results page
// (they need each job's result set); the list stays fast on the job rows.
export function ScanJobs() {
  const qc = useQueryClient()
  const [msg, setMsg] = useState('')
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'), refetchInterval: 5000 })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = locationPaths(locs.data ?? [])

  const rerun = useMutation({ mutationFn: (id: string) => api.post(`/discovery/jobs/${id}/rerun`, {}), onSuccess: () => { setMsg('Re-run launched.'); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) }, onError: (e) => setMsg((e as Error).message) })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/discovery/jobs/${id}`), onSuccess: () => { setMsg('Job deleted.'); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) }, onError: (e) => setMsg((e as Error).message) })

  const list = [...(jobs.data ?? [])].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  const running = list.filter((j) => j.status === 'running').length
  const failed = list.filter((j) => j.status === 'failed').length
  const found = list.reduce((a, j) => a + j.found_count, 0)

  return (
    <div>
      <PageHeader title="Scan Jobs" subtitle="Every discovery scan — status, scope, and outcomes. Open a job for the full operator results view." icon={Radar}
        actions={<Link className="btn btn-primary btn-sm" to="/discovery"><Radar size={14} /> New scan</Link>} />
      <div className="kpi-grid">
        <Kpi label="Total jobs" value={list.length} icon={ListChecks} tone="info" />
        <Kpi label="Running" value={running} icon={Radar} tone={running > 0 ? 'warn' : 'default'} />
        <Kpi label="Failed" value={failed} icon={CircleX} tone={failed > 0 ? 'crit' : 'default'} />
        <Kpi label="Devices found" value={found} icon={Boxes} tone="default" sub="all jobs" />
      </div>
      {msg && <div className="banner" style={{ margin: '0 0 12px', fontSize: 13 }}>{msg}</div>}

      <Panel title="Jobs" subtitle={`${list.length} scan job(s)`} pad={false}>
        {jobs.isLoading && <div className="loading">Loading…</div>}
        {jobs.data && list.length === 0 && <EmptyState icon={Radar} title="No scan jobs yet" message="Start a scan from Discovery Center." action={<Link className="btn btn-primary btn-sm" to="/discovery">Start Discovery</Link>} />}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr>
              <th>Scope / range</th><th>Status</th><th>Site</th><th>Started</th><th>Finished</th><th>Duration</th><th>Probed</th><th>Found</th><th></th>
            </tr></thead>
            <tbody>
              {list.map((j) => (
                <tr key={j.id}>
                  <td className="mono" style={{ fontSize: 12, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={j.scope ?? j.targets ?? j.scope_cidr ?? ''}><Link className="cell-name" to={`/discovery/jobs/${j.id}/results`}>{j.scope ?? j.scope_cidr ?? 'import / manual'}</Link></td>
                  <td><span className={`badge badge-${jobBadge(j.status)}`}>{j.status}</span></td>
                  <td>{j.location_id ? (locPath[j.location_id] ?? '—') : '—'}</td>
                  <td>{j.started_at ? new Date(j.started_at).toLocaleString() : '—'}</td>
                  <td>{j.finished_at ? new Date(j.finished_at).toLocaleTimeString() : (j.status === 'running' ? <span className="muted">running…</span> : '—')}</td>
                  <td>{duration(j.started_at, j.finished_at)}</td>
                  <td>{j.host_count}</td>
                  <td>{j.found_count}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <Link className="btn btn-ghost btn-xs" to={`/discovery/jobs/${j.id}/results`}>Open Results</Link>{' '}
                    <Link className="btn btn-ghost btn-xs" to={`/discovery/jobs/${j.id}/live`} title="Live visual board">Live</Link>{' '}
                    {(j.scope_cidr || j.targets || j.mode === 'site_subnets') && j.status !== 'running' && <button className="btn btn-ghost btn-xs" disabled={rerun.isPending} onClick={() => rerun.mutate(j.id)} title="Re-run this scan"><RefreshCw size={12} /></button>}{' '}
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => { if (confirm('Delete this job and its results?')) del.mutate(j.id) }} title="Delete job"><Trash2 size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      <p className="muted" style={{ fontSize: 12, marginTop: 8 }}><Clock size={12} style={{ verticalAlign: -1 }} /> Auto-refreshes every 5s. Managed / Unmanaged / Missing-classification breakdowns are on each job's Results page.</p>
    </div>
  )
}
