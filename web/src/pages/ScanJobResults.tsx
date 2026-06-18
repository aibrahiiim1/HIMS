import { Fragment, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Radar, Boxes, Wifi, ShieldCheck, ShieldOff, HelpCircle, KeyRound, Bot, CircleX, RefreshCw, ArrowLeft, Sparkles, History, LifeBuoy, EyeOff, Search } from 'lucide-react'
import { Pencil } from 'lucide-react'
import { api, locationPaths, type Device, type DiscoveryJob, type DiscoveryResult, type Location, type ScanJobCounts } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, ProgressBar, timeAgo } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { ClassificationEvidence } from '../components/ClassificationEvidence'
import { EditDevice } from '../components/EditDevice'
import { OnboardingActions, CollectedViaCell, CollectNowPanel, outcomeBadge, phaseMeta, duration } from './Discovery'

type CollectionProgress = { queued: number; retry_waiting: number; running: number; done: number; failed: number; pending: number; settled: boolean; self_healing?: number }
type JobDetail = { job: DiscoveryJob; results: DiscoveryResult[]; counts?: ScanJobCounts; collection?: CollectionProgress; phase?: string }

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

// Management buckets — the Discovery Reliability vocabulary. Every reachable
// result lands in exactly ONE bucket so the operator can answer "why isn't this
// managed?" at a glance and click through to the exact devices + next action.
// Derived from the live device management state + the (precise) scan next_action,
// so the bucket and the per-row guidance never disagree.
const BUCKETS = ['managed', 'needs_agent', 'auth_failed', 'needs_credential', 'transport_blocked', 'unsupported', 'unknown_evidence', 'identified_only', 'offline'] as const
type Bucket = typeof BUCKETS[number]
const BUCKET_META: Record<Bucket, { label: string; tone: string }> = {
  managed: { label: 'Managed', tone: 'up' },
  needs_agent: { label: 'Needs / offline agent', tone: 'warning' },
  auth_failed: { label: 'Auth failed', tone: 'down' },
  needs_credential: { label: 'Needs credential', tone: 'warning' },
  transport_blocked: { label: 'Transport blocked', tone: 'down' },
  unsupported: { label: 'Unsupported / Telnet-only', tone: 'unknown' },
  unknown_evidence: { label: 'Unknown (has evidence)', tone: 'info' },
  identified_only: { label: 'Identified only', tone: 'info' },
  offline: { label: 'Offline', tone: 'unknown' },
}

// Precise, operator-actionable remediation text per failure category — so a host is
// never left with a raw token or the misleading "fix the rejected credential" when the
// real cause is host policy. transport_policy_blocked is the terminal "host refuses all
// supported transports" wall (.106/.119): reachable, but WinRM negotiation refused +
// WMI/DCOM access-denied — never a wrong password.
const CATEGORY_HINT: Record<string, string> = {
  transport_policy_blocked:
    'Host reachable but refuses every supported transport: WinRM rejected the session (needs encrypted WinRM — enable HTTPS/5986 or the required encryption mode) AND WMI/DCOM access was denied (UAC — set LocalAccountTokenFilterPolicy for remote local-admin WMI). Or supply a domain/host credential the host policy allows. Not a wrong password.',
  winrm_negotiate_error: 'WinRM listener rejected the session negotiation (often AllowUnencrypted=false). Retried automatically; if persistent, enable encrypted WinRM or use WMI/DCOM.',
  winrm_connect_timeout: 'WinRM/5985 did not respond (transient or WinRM disabled). Retried automatically; if persistent, enable WinRM or rely on the WMI/DCOM fallback.',
}
// catLabel renders a failure category as readable text (underscores → spaces).
const catLabel = (c?: string) => (c ?? '').replace(/_/g, ' ')

function bucketOf(r: DiscoveryResult, d?: Device): Bucket {
  const p = r.probe_data ?? {}
  const na = (p.next_action ?? '').toLowerCase()
  const via = p.collected_via
  if (via === 'direct' || via === 'relay_agent' || d?.management === 'managed' || na.startsWith('managed via')) return 'managed'
  if (r.outcome === 'failed' || r.outcome === 'missed' || d?.reachability === 'offline') return 'offline'
  if (d?.management === 'needs_agent' || d?.management === 'agent_offline' || via === 'agent_offline' || via === 'agent_missing' || na.includes('relay agent')) return 'needs_agent'
  if (d?.management === 'credential_failed' || na.includes('auth_failed') || na.includes('authentication rejected') || na.includes('auth failed')) return 'auth_failed'
  if (na.includes('telnet-only') || na.includes('unsupported')) return 'unsupported'
  if (na.includes('http-only') || na.includes('open its web ui') || na.includes('classify it') || na.includes('classify the device') || na.includes('classify manually')) return 'unknown_evidence'
  if (na.includes('add a') || na.includes('add an') || na.includes('needs ') || na.includes('onboard')) return 'needs_credential'
  if (na.includes('unreachable') || na.includes('enable ') || na.includes('open 5985') || na.includes('open port') || na.includes('not responding') || na.includes('timed out')) return 'transport_blocked'
  if (d?.category && d.category !== 'unknown') return 'identified_only'
  return 'unknown_evidence'
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
  const [filter, setFilter] = useState<Filter | Bucket>('all')
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [msg, setMsg] = useState('')
  const [whyOpen, setWhyOpen] = useState<Set<string>>(new Set()) // result ids with the evidence panel expanded
  const toggleWhy = (id: string) => setWhyOpen((prev) => { const n = new Set(prev); if (n.has(id)) n.delete(id); else n.add(id); return n })

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
  const collection = detail.data?.collection
  const phase = detail.data?.phase ?? (job ? job.status : undefined)
  const dev = (r: DiscoveryResult) => (r.device_id ? devMap.get(r.device_id) : undefined)

  // KPI rollup (joined to the live device for reachability/management).
  const k = useMemo(() => {
    let online = 0, managed = 0, unmanaged = 0, missing = 0, credFail = 0, needsAgent = 0, pingable = 0
    for (const r of results) {
      // Pingable = the host answered the scan this run (a result row exists with an
      // alive outcome). This is the denominator for "managed of pingable".
      if (r.outcome === 'alive' || r.outcome === 'classified' || r.outcome === 'enrolled') pingable++
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
    return { online, managed, unmanaged, missing, credFail, needsAgent, failed, pingable }
  }, [results, devMap])

  // Scan progress: hosts processed of total. A finished job reads 100%.
  const total = job?.host_count ?? 0
  const scanned = job?.scanned_count ?? 0
  const done = job ? job.status !== 'running' && job.status !== 'pending' : false
  const progressPct = done ? 100 : total > 0 ? (scanned / total) * 100 : 0
  const managedPct = k.pingable > 0 ? (k.managed / k.pingable) * 100 : 0

  // Per-result management bucket (computed once) + counts for the summary strip.
  const bucketCounts = useMemo(() => {
    const c = {} as Record<Bucket, number>
    for (const b of BUCKETS) c[b] = 0
    for (const r of results) { if (r.outcome === 'skipped') continue; c[bucketOf(r, dev(r))]++ }
    return c
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [results, devMap])

  const filtered = useMemo(() => results.filter((r) => {
    const d = dev(r)
    if ((BUCKETS as readonly string[]).includes(filter)) return bucketOf(r, d) === filter
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
        subtitle={job ? `${job.scope_cidr ?? 'import'} · ${phaseMeta(phase).label}${phase === 'collecting' && collection ? ` ${collection.pending}` : ''}${job.location_id ? ' · ' + (locPath[job.location_id] ?? '') : ''}` : 'Loading…'}
        actions={<>
          <Link className="btn btn-ghost btn-sm" to="/discovery/jobs"><ArrowLeft size={14} /> All jobs</Link>
          <Link className="btn btn-ghost btn-sm" to={`/discovery/jobs/${jobId}/live`}><Radar size={14} /> Visual View</Link>
          {job?.scope_cidr && <button className="btn btn-sm" disabled={rerun.isPending} onClick={() => rerun.mutate()}><RefreshCw size={14} /> Re-run scan</button>}
        </>} />

      {detail.isLoading && <div className="loading">Loading…</div>}
      {job && (
        <>
          {/* Left: reachable-device discovery (sweeps the whole subnet 0→100%, headline =
              reachable count). Right: how many of those reachable devices are managed. */}
          <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 8, padding: '12px 14px', marginBottom: 12, display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))' }}>
            <ProgressBar value={progressPct} tone={done ? '#16a34a' : 'var(--brand)'} pulse={!done}
              label={done ? `${k.pingable} reachable found` : `Discovering… ${k.pingable} reachable`}
              sublabel={`${Math.min(scanned, total)} of ${total} hosts scanned`} />
            <ProgressBar value={managedPct} tone="#16a34a"
              label="Managed of reachable" sublabel={`${k.managed} of ${k.pingable} reachable managed`} />
          </div>
          {/* Collection progress — deep OS collection runs ASYNC after discovery, so the
              job can be "completed" while devices are still being collected. Show it so the
              operator never reads the result as fully settled while the queue drains. */}
          {collection && (phase === 'collecting' || phase === 'self_healing') && (
            <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderLeft: '3px solid #d97706', borderRadius: 8, padding: '10px 14px', marginBottom: 12 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontWeight: 600 }}>
                <RefreshCw size={14} /> {phase === 'self_healing'
                  ? `Discovery complete · self-heal pending ${collection.self_healing ?? 0}`
                  : `${job.status === 'completed' ? 'Discovery complete · collecting' : 'Collecting'} ${collection.pending}`}
              </div>
              <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', marginTop: 6, fontSize: 13, color: 'var(--text-muted)' }}>
                <span>Queued {collection.queued}</span>
                <span>Running {collection.running}</span>
                <span>Retry-waiting {collection.retry_waiting}</span>
                <span>Done {collection.done}</span>
                <span>Failed {collection.failed}</span>
                {(collection.self_healing ?? 0) > 0 && <span>Self-heal eligible {collection.self_healing}</span>}
              </div>
              <div style={{ marginTop: 6, fontSize: 12, color: 'var(--text-muted)' }}>
                {phase === 'self_healing'
                  ? 'Transient collection failures are awaiting automatic self-heal (re-collected after a short cooldown) — not yet settled.'
                  : 'Deep OS collection runs after discovery via the site relay agent — the managed count may still be increasing.'}
              </div>
            </div>
          )}
          {collection && phase === 'complete' && collection.done + collection.failed > 0 && (
            <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 12 }}>✓ Collection settled — {collection.done} collected{collection.failed > 0 ? `, ${collection.failed} failed` : ''}.</div>
          )}
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
            <Kpi label="Reachable" value={k.pingable} icon={Wifi} tone="info" sub="responded to scan" />
            <Kpi label="Online" value={k.online} icon={Wifi} tone="ok" />
            <Kpi label="Managed" value={k.managed} icon={ShieldCheck} tone="ok" sub={`of ${k.pingable} reachable`} />
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

          {/* Management buckets — every reachable device in exactly one bucket, with
              its precise reason. Click a bucket to filter the table to those devices. */}
          {results.length > 0 && (
            <Panel title="Management buckets" subtitle="Why each reachable device is or isn't managed — click to filter">
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {BUCKETS.filter((b) => bucketCounts[b] > 0).map((b) => (
                  <button key={b} onClick={() => setFilter(filter === b ? 'all' : b)}
                    className={`badge badge-${BUCKET_META[b].tone}`}
                    style={{ cursor: 'pointer', fontSize: 12, padding: '6px 10px', border: filter === b ? '2px solid var(--brand)' : '1px solid var(--border)', display: 'inline-flex', gap: 6, alignItems: 'center' }}>
                    <strong style={{ fontSize: 14 }}>{bucketCounts[b]}</strong> {BUCKET_META[b].label}
                  </button>
                ))}
              </div>
            </Panel>
          )}

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
                  <th>Expected</th><th>Opportunistic</th><th title="Protocols intentionally not tried for this device type — e.g. SNMP/SSH/WinRM are not applicable to a camera. This is by design, not a failure: it keeps scans fast and avoids needless auth_failed noise / lockouts.">Not applicable</th><th>Cred attempts</th><th>Bound</th><th>Collected via</th><th>Next action</th><th></th>
                </tr></thead>
                <tbody>
                  {filtered.map((r) => {
                    const d = dev(r)
                    const p = r.probe_data ?? {}
                    const open = whyOpen.has(r.id)
                    return (
                      <Fragment key={r.id}>
                      <tr>
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
                        <td className="muted" style={{ fontSize: 11 }} title="Not applicable to this device type — intentionally not tried (by design, not a failure).">{(p.skipped_protocols ?? []).join(', ') || '—'}</td>
                        <td style={{ fontSize: 11 }}>{(p.cred_attempts ?? []).length === 0 ? <span className="muted">none</span> : (p.cred_attempts ?? []).map((a, i) => (
                          <div key={i} title={!a.success && a.category ? (CATEGORY_HINT[a.category] ?? '') : ''}><span className={`badge badge-${a.success ? 'up' : a.category === 'auth_failed' ? 'down' : 'unknown'}`}>{a.kind}</span> <span className="muted">{a.success ? 'ok' : catLabel(a.category)}</span></div>
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
                        <td style={{ fontSize: 12 }}>{(() => {
                          // A terminal transport-policy-blocked attempt gets the precise host-config
                          // remediation (never "fix the rejected credential").
                          const blocked = (p.cred_attempts ?? []).find((a) => !a.success && a.category && CATEGORY_HINT[a.category])
                          if (blocked) return <span style={{ color: '#d97706' }}>{CATEGORY_HINT[blocked.category!]}</span>
                          return r.error ? <span className="error-msg">{r.error}</span> : (p.next_action ?? '—')
                        })()}</td>
                        <td style={{ whiteSpace: 'nowrap' }}>
                          {d && <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`} title="Open device (test credential / bind / repair)">Open</Link>}{' '}
                          {d && <button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit / Lock classification"><Pencil size={12} /></button>}{' '}
                          {d && <button className="btn btn-ghost btn-xs" disabled={reclassify.isPending} onClick={() => reclassify.mutate(d.id)} title="Reclassify from evidence">RC</button>}{' '}
                          <button className="btn btn-ghost btn-xs" disabled={rescanIP.isPending} onClick={() => rescanIP.mutate(r.ip)} title="Re-scan this device"><RefreshCw size={12} /></button>
                          <button className={'btn btn-ghost btn-xs' + (open ? ' active' : '')} onClick={() => toggleWhy(r.id)} title="Why this classification? Show evidence, matched + rejected fingerprints"><Search size={12} /> Why</button>
                          {d && <div style={{ marginTop: 4 }}><CollectNowPanel deviceID={d.id} qc={qc} jobID={job?.id ?? null} /></div>}
                        </td>
                      </tr>
                      {open && (
                        <tr className="evidence-row">
                          <td colSpan={14} style={{ background: 'var(--surface-2, rgba(255,255,255,0.02))', borderTop: '2px solid var(--accent, #4a7dff)' }}>
                            <ClassificationEvidence detail={p} />
                          </td>
                        </tr>
                      )}
                      </Fragment>
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
