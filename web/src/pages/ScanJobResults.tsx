import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Radar, Boxes, Wifi, ShieldCheck, ShieldOff, HelpCircle, KeyRound, Bot, CircleX, RefreshCw, ArrowLeft, Sparkles, History, LifeBuoy, EyeOff } from 'lucide-react'
import { Pencil } from 'lucide-react'
import { api, locationPaths, type Device, type DiscoveryJob, type DiscoveryResult, type Location, type ScanJobCounts } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { EditDevice } from '../components/EditDevice'
import { OnboardingActions, CollectedViaCell, outcomeBadge, duration } from './Discovery'

type JobDetail = { job: DiscoveryJob; results: DiscoveryResult[]; counts?: ScanJobCounts }

// Known-Device-Retry disposition → short badge label + tone. A known device that
// the sweep missed never disappears: it shows as "Missed this run".
const DISPOSITION: Record<string, { label: string; tone: string }> = {
  newly_discovered: { label: 'New', tone: 'info' },
  known_seen: { label: 'Known — seen again', tone: 'up' },
  known_recovered: { label: 'Recovered by retry', tone: 'warning' },
  known_missed: { label: 'Missed this run', tone: 'down' },
  known_unreachable: { label: 'Missed this run', tone: 'down' },
}

const FILTERS = [
  'all', 'newly_discovered', 'known_seen', 'known_recovered', 'known_missed',
  'managed', 'unmanaged', 'online_unmanaged', 'missing_classification',
  'credential_failed', 'needs_agent', 'agent_offline', 'collection_failed',
  'collected_relay', 'collected_direct',
] as const
type Filter = typeof FILTERS[number]
const FILTER_LABEL: Record<Filter, string> = {
  all: 'All', newly_discovered: 'Newly discovered', known_seen: 'Known — seen again',
  known_recovered: 'Recovered by retry', known_missed: 'Known missed this run',
  managed: 'Managed', unmanaged: 'Unmanaged', online_unmanaged: 'Online but unmanaged',
  missing_classification: 'Missing classification', credential_failed: 'Credential failed',
  needs_agent: 'Needs agent', agent_offline: 'Agent offline', collection_failed: 'Collection failed',
  collected_relay: 'Via relay agent', collected_direct: 'Direct collection',
}

function isMissingClassification(d?: Device): boolean {
  if (!d) return false
  return !d.category || d.category === 'unknown' || !d.vendor
}

// Progress stages — highlighted from the job status + what the results show.
function Timeline({ job, results }: { job: DiscoveryJob; results: DiscoveryResult[] }) {
  const enrolled = results.filter((r) => r.outcome === 'enrolled').length
  const bound = results.filter((r) => r.probe_data?.bound_cred).length
  const collected = results.filter((r) => r.probe_data?.collected_via === 'direct' || r.probe_data?.collected_via === 'relay_agent').length
  const failed = results.filter((r) => r.outcome === 'failed' || r.error).length
  const done = job.status === 'completed'
  const running = job.status === 'running'
  const stages = [
    { key: 'queued', label: 'Queued', done: true },
    { key: 'probing', label: 'Probing', done: results.length > 0 || done },
    { key: 'classifying', label: 'Classifying', done: results.some((r) => r.category) || done },
    { key: 'testing', label: 'Testing credentials', done: results.some((r) => (r.probe_data?.cred_attempts ?? []).length > 0) || done },
    { key: 'binding', label: `Binding (${bound})`, done: bound > 0 || done },
    { key: 'collecting', label: `Collecting (${collected})`, done: collected > 0 || done },
    { key: 'completed', label: running ? 'Running…' : done ? 'Completed' : job.status, done },
  ]
  return (
    <Panel title="Progress" subtitle={`${enrolled} enrolled · ${failed} need action`}>
      <div style={{ display: 'flex', gap: 0, flexWrap: 'wrap', alignItems: 'center' }}>
        {stages.map((s, i) => (
          <span key={s.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <span className={`badge ${s.done ? 'badge-up' : running ? 'badge-warning' : 'badge-unknown'}`}>{s.label}</span>
            {i < stages.length - 1 && <span className="muted" style={{ margin: '0 6px' }}>→</span>}
          </span>
        ))}
        {failed > 0 && <span className="badge badge-down" style={{ marginLeft: 10 }}>{failed} failed / needs action</span>}
      </div>
    </Panel>
  )
}

export function ScanJobResults() {
  const { jobId } = useParams()
  const qc = useQueryClient()
  const [filter, setFilter] = useState<Filter>('all')
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [msg, setMsg] = useState('')

  const detail = useQuery({
    queryKey: ['discovery-job', jobId],
    queryFn: () => api.get<JobDetail>(`/discovery/jobs/${jobId}`),
    enabled: !!jobId, refetchInterval: 4000,
  })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = locationPaths(locs.data ?? [])
  const devMap = useMemo(() => new Map((devices.data ?? []).map((d) => [d.id, d])), [devices.data])

  const rerun = useMutation({ mutationFn: () => api.post(`/discovery/jobs/${jobId}/rerun`, {}), onSuccess: () => { setMsg('Re-run launched.'); qc.invalidateQueries({ queryKey: ['discovery-jobs'] }) }, onError: (e) => setMsg((e as Error).message) })
  const rescanIP = useMutation({ mutationFn: (ip: string) => api.post('/discovery/scan', { mode: 'targets', targets: ip }), onSuccess: () => setMsg('Device re-scan launched.'), onError: (e) => setMsg((e as Error).message) })
  const reclassify = useMutation({ mutationFn: (id: string) => api.post(`/devices/${id}/reclassify`, {}), onSuccess: () => { setMsg('Reclassified.'); qc.invalidateQueries({ queryKey: ['devices'] }) }, onError: (e) => setMsg((e as Error).message) })

  const job = detail.data?.job
  const results = detail.data?.results ?? []
  const counts = detail.data?.counts
  const dev = (r: DiscoveryResult) => (r.device_id ? devMap.get(r.device_id) : undefined)

  // KPI rollup (joined to the live device for reachability/management).
  const k = useMemo(() => {
    let online = 0, managed = 0, unmanaged = 0, missing = 0, credFail = 0, needsAgent = 0
    for (const r of results) {
      const d = r.device_id ? devMap.get(r.device_id) : undefined
      if (!d) continue
      if (d.reachability === 'online') online++
      if (d.management === 'managed') managed++
      else unmanaged++
      if (isMissingClassification(d)) missing++
      if (d.management === 'credential_failed') credFail++
      if (d.management === 'needs_agent' || d.management === 'agent_offline') needsAgent++
    }
    const failed = results.filter((r) => r.outcome === 'failed' || r.error).length
    return { online, managed, unmanaged, missing, credFail, needsAgent, failed }
  }, [results, devMap])

  const filtered = useMemo(() => results.filter((r) => {
    const d = dev(r)
    switch (filter) {
      case 'all': return true
      case 'newly_discovered': return r.disposition === 'newly_discovered'
      case 'known_seen': return r.disposition === 'known_seen'
      case 'known_recovered': return r.disposition === 'known_recovered'
      case 'known_missed': return r.disposition === 'known_missed' || r.disposition === 'known_unreachable'
      case 'managed': return d?.management === 'managed'
      case 'unmanaged': return d ? d.management !== 'managed' : false
      case 'online_unmanaged': return d?.reachability === 'online' && d?.management !== 'managed'
      case 'missing_classification': return isMissingClassification(d)
      case 'credential_failed': return d?.management === 'credential_failed'
      case 'needs_agent': return d?.management === 'needs_agent'
      case 'agent_offline': return d?.management === 'agent_offline'
      case 'collection_failed': return d?.management === 'collection_failed'
      case 'collected_relay': return r.probe_data?.collected_via === 'relay_agent'
      case 'collected_direct': return r.probe_data?.collected_via === 'direct'
      default: return true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [results, filter, devMap])

  if (!jobId) return null
  return (
    <div>
      <PageHeader title="Scan Job Results" icon={Radar}
        subtitle={job ? `${job.scope_cidr ?? 'import'} · ${job.status}${job.location_id ? ' · ' + (locPath[job.location_id] ?? '') : ''}` : 'Loading…'}
        actions={<>
          <Link className="btn btn-ghost btn-sm" to="/discovery/jobs"><ArrowLeft size={14} /> All jobs</Link>
          <Link className="btn btn-ghost btn-sm" to={`/discovery/jobs/${jobId}/live`}><Radar size={14} /> Visual View</Link>
          {job?.scope_cidr && <button className="btn btn-sm" disabled={rerun.isPending} onClick={() => rerun.mutate()}><RefreshCw size={14} /> Re-run scan</button>}
        </>} />

      {detail.isLoading && <div className="loading">Loading…</div>}
      {job && (
        <>
          {/* A. Scan stability — separated, honest counts (NOT a stable inventory total). */}
          <div className="kpi-grid">
            <Kpi label="Targets probed" value={counts?.targets_probed ?? job.host_count} icon={Boxes} tone="info" sub={`duration ${duration(job.started_at, job.finished_at)}`} />
            <Kpi label="Newly discovered" value={counts?.newly_discovered ?? 0} icon={Sparkles} tone="info" />
            <Kpi label="Known — seen again" value={counts?.known_seen_again ?? 0} icon={History} tone="ok" />
            <Kpi label="Recovered by retry" value={counts?.known_recovered_by_retry ?? 0} icon={LifeBuoy} tone={(counts?.known_recovered_by_retry ?? 0) > 0 ? 'warn' : 'default'} sub="missed sweep, found on retry" />
            <Kpi label="Known missed this run" value={counts?.known_missed_this_run ?? 0} icon={EyeOff} tone={(counts?.known_missed_this_run ?? 0) > 0 ? 'crit' : 'default'} sub="still in inventory" />
            <Kpi label="Enrolled / updated" value={counts?.enrolled_updated ?? job.found_count} icon={Radar} tone="default" />
          </div>
          {/* Live device state across this job's results. */}
          <div className="kpi-grid">
            <Kpi label="Online" value={k.online} icon={Wifi} tone="ok" />
            <Kpi label="Managed" value={k.managed} icon={ShieldCheck} tone="ok" />
            <Kpi label="Unmanaged" value={k.unmanaged} icon={ShieldOff} tone={k.unmanaged > 0 ? 'warn' : 'default'} />
            <Kpi label="Missing classification" value={k.missing} icon={HelpCircle} tone={k.missing > 0 ? 'warn' : 'default'} />
            <Kpi label="Credential failed" value={k.credFail} icon={KeyRound} tone={k.credFail > 0 ? 'crit' : 'default'} />
            <Kpi label="Needs / offline agent" value={k.needsAgent} icon={Bot} tone={k.needsAgent > 0 ? 'warn' : 'default'} />
            <Kpi label="Failed actions" value={k.failed} icon={CircleX} tone={k.failed > 0 ? 'crit' : 'default'} />
          </div>
          {job.error && <div className="error-msg" style={{ fontSize: 12, margin: '0 0 12px' }}>{job.error}</div>}
          {msg && <div className="banner" style={{ margin: '0 0 12px', fontSize: 13 }}>{msg}</div>}

          {/* B. Progress / Timeline */}
          <Timeline job={job} results={results} />

          {/* D. Onboarding Actions */}
          {results.length > 0 && <OnboardingActions results={results} qc={qc} setMsg={setMsg} onRescan={() => rerun.mutate()} rescanning={rerun.isPending} />}

          {/* E. Filters + C. Results table */}
          <Panel title="Results" subtitle={`${filtered.length} of ${results.length} device(s)`} pad={false}>
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', padding: '8px 10px', position: 'sticky', top: 0, background: 'var(--surface)', zIndex: 1 }}>
              {FILTERS.map((f) => (
                <button key={f} className={'seg-chip' + (filter === f ? ' active' : '')} onClick={() => setFilter(f)}>{FILTER_LABEL[f]}</button>
              ))}
            </div>
            {results.length === 0 && <EmptyState icon={Radar} title="No results yet" message={job.status === 'running' ? 'Scanning…' : 'No host results recorded.'} />}
            {filtered.length > 0 && (
              <div style={{ overflowX: 'auto' }}>
              <table className="data-table">
                <thead><tr>
                  <th>IP</th><th>Name</th><th>Reach</th><th>Mgmt</th><th>Category</th><th>Vendor / Model</th>
                  <th>Expected</th><th>Opportunistic</th><th>Skipped</th><th>Cred attempts</th><th>Bound</th><th>Collected via</th><th>Next action</th><th></th>
                </tr></thead>
                <tbody>
                  {filtered.map((r) => {
                    const d = dev(r)
                    const p = r.probe_data ?? {}
                    return (
                      <tr key={r.id}>
                        <td className="mono" style={{ fontSize: 12 }}>{r.ip} <span className={`badge badge-${outcomeBadge(r.outcome)}`}>{r.outcome}</span></td>
                        <td>{d ? <Link className="cell-name" to={`/devices/${d.id}`}>{d.name}</Link> : <span className="muted">not enrolled</span>}{d?.hostname && <small style={{ display: 'block' }}>{d.hostname}</small>}
                          {r.disposition && DISPOSITION[r.disposition] && (
                            <span className={`badge badge-${DISPOSITION[r.disposition].tone}`} style={{ fontSize: 10, marginTop: 2, display: 'inline-block' }}>
                              {DISPOSITION[r.disposition].label}{(r.retry_count ?? 0) > 0 ? ` ·${r.retry_count}×` : ''}
                            </span>
                          )}
                        </td>
                        <td>{d ? <ReachabilityBadge value={d.reachability} /> : '—'}</td>
                        <td>{d ? <ManagementBadge value={d.management} managedBy={d.managed_by} /> : '—'}</td>
                        <td style={{ textTransform: 'capitalize' }}>{(r.category ?? p.classification ?? d?.category ?? 'unknown').replace(/_/g, ' ')}{typeof p.confidence === 'number' && p.confidence > 0 ? <span className="muted" style={{ fontSize: 11 }}> · {p.confidence}%</span> : null}{p.class_note && <span className="badge badge-warning" style={{ fontSize: 9, marginLeft: 4, textTransform: 'none' }} title={p.class_note}>classification preserved</span>}</td>
                        <td>{d?.vendor || '—'}{d?.model ? ` / ${d.model}` : ''}</td>
                        <td style={{ fontSize: 11 }}>{(p.expected_protocols ?? []).join(', ').toUpperCase() || '—'}</td>
                        <td style={{ fontSize: 11 }}>{(p.opportunistic_protocols ?? []).join(', ').toUpperCase() || '—'}</td>
                        <td className="muted" style={{ fontSize: 11 }}>{(p.skipped_protocols ?? []).join(', ') || '—'}</td>
                        <td style={{ fontSize: 11 }}>{(p.cred_attempts ?? []).length === 0 ? <span className="muted">none</span> : (p.cred_attempts ?? []).map((a, i) => (
                          <div key={i}><span className={`badge badge-${a.success ? 'up' : a.category === 'auth_failed' ? 'down' : 'unknown'}`}>{a.kind}</span> <span className="muted">{a.success ? 'ok' : a.category}</span></div>
                        ))}</td>
                        <td>{p.bound_cred ? <span className="badge badge-up">{p.bound_cred}</span> : <span className="muted">—</span>}</td>
                        <td style={{ fontSize: 11 }}>
                          <CollectedViaCell via={p.collected_via} agent={p.agent_name} />
                          {p.ssh && (
                            <div style={{ marginTop: 4, fontSize: 10, lineHeight: 1.5 }}>
                              <span className={`badge badge-${p.ssh.status === 'complete' ? 'up' : p.ssh.status === 'failed' ? 'down' : 'warning'}`}>SSH CLI {p.ssh.status}</span>
                              <div className="muted">{p.ssh.supported} supported · {p.ssh.unsupported} unsupported cmds</div>
                              <div className="muted">{p.ssh.ap_rows} AP rows · {p.ssh.client_rows} client rows{p.ssh.warnings ? ` · ${p.ssh.warnings} warn` : ''}</div>
                              {(p.ssh.ap_rows < p.ssh.ap_total || p.ssh.client_rows < p.ssh.client_total) && (
                                <div style={{ color: '#ffb74d' }}>reported {p.ssh.ap_total} APs / {p.ssh.client_total} clients</div>
                              )}
                            </div>
                          )}
                        </td>
                        <td style={{ fontSize: 12 }}>{r.error ? <span className="error-msg">{r.error}</span> : (p.next_action ?? '—')}</td>
                        <td style={{ whiteSpace: 'nowrap' }}>
                          {d && <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`} title="Open device (test credential / bind / repair)">Open</Link>}{' '}
                          {d && <button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit / Lock classification"><Pencil size={12} /></button>}{' '}
                          {d && <button className="btn btn-ghost btn-xs" disabled={reclassify.isPending} onClick={() => reclassify.mutate(d.id)} title="Reclassify from evidence">RC</button>}{' '}
                          <button className="btn btn-ghost btn-xs" disabled={rescanIP.isPending} onClick={() => rescanIP.mutate(r.ip)} title="Re-scan this device"><RefreshCw size={12} /></button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
              </div>
            )}
          </Panel>
          <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>{timeAgo(job.created_at)} · <Link to="/inventory/missing-classification">Missing Classification</Link> · <Link to="/inventory/unmanaged">Unmanaged Devices</Link></p>
        </>
      )}
      {editDev && <EditDevice device={editDev} onClose={() => setEditDev(null)} />}
    </div>
  )
}
