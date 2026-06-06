import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import {
  Radar, ArrowLeft, Table2, RefreshCw, Pencil, KeyRound, X,
  Network, Server, Laptop, Printer, Camera, Video, Flame, HardDrive, Phone, Wifi, Boxes, HelpCircle, Cpu,
} from 'lucide-react'
import { api, locationPaths, type Device, type DiscoveryJob, type DiscoveryResult, type Location, type ScanJobCounts } from '../api'
import { PageHeader, EmptyState } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { EditDevice } from '../components/EditDevice'
import { duration } from './Discovery'

type JobDetail = { job: DiscoveryJob; results: DiscoveryResult[]; counts?: ScanJobCounts }

// Derived live state for one device node — drives colour, label and ordering.
type NodeState = 'pending' | 'probing' | 'online' | 'managed' | 'unmanaged' | 'needs_agent' | 'missed' | 'failed'

const STATE_META: Record<NodeState, { label: string; color: string; bg: string }> = {
  pending: { label: 'Waiting', color: '#94a3b8', bg: 'rgba(148,163,184,0.08)' },
  probing: { label: 'Probing', color: '#0ea5e9', bg: 'rgba(14,165,233,0.10)' },
  online: { label: 'Online', color: '#2563eb', bg: 'rgba(37,99,235,0.10)' },
  managed: { label: 'Managed', color: '#16a34a', bg: 'rgba(22,163,74,0.12)' },
  unmanaged: { label: 'Online · Unmanaged', color: '#d97706', bg: 'rgba(217,119,6,0.12)' },
  needs_agent: { label: 'Needs Relay Agent', color: '#7c3aed', bg: 'rgba(124,58,237,0.12)' },
  missed: { label: 'Missed this run', color: '#b91c1c', bg: 'rgba(185,28,28,0.10)' },
  failed: { label: 'Failed', color: '#dc2626', bg: 'rgba(220,38,38,0.12)' },
}

const CAT_ICON: Record<string, typeof Server> = {
  switch: Network, router: Network, firewall: Flame, wireless_controller: Wifi, access_point: Wifi,
  server: Server, virtual_host: HardDrive, virtual_machine: HardDrive, storage: HardDrive,
  workstation: Laptop, endpoint: Laptop, printer: Printer, camera: Camera, nvr: Video,
  pbx: Phone, ip_phone: Phone, voice_gateway: Phone, ups: Cpu, database: Boxes, unknown: HelpCircle,
}

// IPv4 CIDR membership — used to pre-populate the known devices a running scan is
// expected to touch (so they show as Waiting/Probing before their result lands).
function ipToInt(ip: string): number | null {
  const p = ip.split('.').map(Number)
  if (p.length !== 4 || p.some((x) => isNaN(x) || x < 0 || x > 255)) return null
  return (p[0] * 16777216 + p[1] * 65536 + p[2] * 256 + p[3]) >>> 0
}
function ipInCidr(ip: string, cidr?: string | null): boolean {
  if (!cidr) return false
  const [base, bitsStr] = cidr.split('/')
  const bits = Number(bitsStr)
  const ipi = ipToInt(ip), basei = ipToInt(base)
  if (ipi == null || basei == null || isNaN(bits)) return false
  if (bits <= 0) return true
  const mask = bits >= 32 ? 0xffffffff : (~((1 << (32 - bits)) - 1)) >>> 0
  return ((ipi & mask) >>> 0) === ((basei & mask) >>> 0)
}

interface Node {
  ip: string
  r?: DiscoveryResult
  d?: Device
  state: NodeState
  recovered: boolean
  newly: boolean
  method?: string // managed-via method
  reason?: string // unmanaged reason
}

const MGMT_REASON: Record<string, string> = {
  credential_failed: 'credential failed', needs_credential: 'no credential', needs_agent: 'needs Relay Agent',
  agent_offline: 'Relay Agent offline', collection_failed: 'collection failed', partially_managed: 'partially managed',
  unmanaged: 'no proven access',
}

function deriveNode(ip: string, r: DiscoveryResult | undefined, d: Device | undefined, running: boolean): Node {
  const recovered = r?.disposition === 'known_recovered'
  const newly = r?.disposition === 'newly_discovered'
  if (!r) {
    return { ip, r, d, state: running ? 'probing' : 'pending', recovered: false, newly: false }
  }
  if (r.outcome === 'missed' || r.disposition === 'known_unreachable' || r.disposition === 'known_missed') {
    return { ip, r, d, state: 'missed', recovered: false, newly: false }
  }
  const p = r.probe_data ?? {}
  if (r.error || r.outcome === 'failed') return { ip, r, d, state: 'failed', recovered, newly }
  if (d?.management === 'managed') {
    const via = (d.managed_by && d.managed_by[0]) || p.bound_cred || ''
    return { ip, r, d, state: 'managed', recovered, newly, method: via }
  }
  if (d?.management === 'needs_agent' || d?.management === 'agent_offline') {
    return { ip, r, d, state: 'needs_agent', recovered, newly, reason: MGMT_REASON[d.management] }
  }
  const online = d?.reachability === 'online' || r.outcome === 'enrolled' || r.outcome === 'alive' || r.outcome === 'classified'
  if (online && d?.management !== 'managed') {
    return { ip, r, d, state: 'unmanaged', recovered, newly, reason: MGMT_REASON[d?.management ?? 'unmanaged'] ?? (p.next_action || 'not managed') }
  }
  return { ip, r, d, state: 'online', recovered, newly }
}

const FILTERS = ['all', 'probing', 'online', 'managed', 'unmanaged', 'credential_failed', 'needs_agent', 'missing_classification', 'known_missed', 'recovered', 'relay_agent'] as const
type Filter = typeof FILTERS[number]
const FILTER_LABEL: Record<Filter, string> = {
  all: 'All', probing: 'Probing', online: 'Online', managed: 'Managed', unmanaged: 'Unmanaged',
  credential_failed: 'Credential failed', needs_agent: 'Needs agent', missing_classification: 'Missing classification',
  known_missed: 'Known missed', recovered: 'Recovered by retry', relay_agent: 'Relay agent',
}

function isMissingClass(d?: Device): boolean {
  if (!d) return false
  return !d.category || d.category === 'unknown' || !d.vendor
}

export function LiveDiscovery() {
  const { jobId } = useParams()
  const qc = useQueryClient()
  const [filter, setFilter] = useState<Filter>('all')
  const [sel, setSel] = useState<string | null>(null)
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [msg, setMsg] = useState('')

  const detail = useQuery({
    queryKey: ['discovery-job-live', jobId],
    queryFn: () => api.get<JobDetail>(`/discovery/jobs/${jobId}`),
    enabled: !!jobId,
    // Fast poll while the job runs; settle to a slow refresh once it's done (playback).
    refetchInterval: (q) => ((q.state.data as JobDetail | undefined)?.job?.status === 'running' ? 1500 : 15000),
  })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all'), refetchInterval: 4000 })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = locationPaths(locs.data ?? [])
  const devMap = useMemo(() => new Map((devices.data ?? []).map((d) => [d.id, d])), [devices.data])
  const devByIP = useMemo(() => new Map((devices.data ?? []).filter((d) => d.primary_ip).map((d) => [d.primary_ip as string, d])), [devices.data])

  const rescanIP = useMutation({ mutationFn: (ip: string) => api.post('/discovery/scan', { mode: 'targets', targets: ip }), onSuccess: () => setMsg('Device re-scan launched.'), onError: (e) => setMsg((e as Error).message) })
  const reclassify = useMutation({ mutationFn: (id: string) => api.post(`/devices/${id}/reclassify`, {}), onSuccess: () => { setMsg('Reclassified.'); qc.invalidateQueries({ queryKey: ['devices'] }) }, onError: (e) => setMsg((e as Error).message) })
  const testCreds = useMutation({ mutationFn: (id: string) => api.post<{ successes: number; failures: number }>('/credentials/test', { device_ids: [id] }), onSuccess: (r) => setMsg(`Credential test: ${r.successes} ok / ${r.failures} failed.`), onError: (e) => setMsg((e as Error).message) })
  const rerun = useMutation({ mutationFn: () => api.post(`/discovery/jobs/${jobId}/rerun`, {}), onSuccess: () => setMsg('Re-run launched.'), onError: (e) => setMsg((e as Error).message) })

  const job = detail.data?.job
  const results = useMemo(() => detail.data?.results ?? [], [detail.data])
  const counts = detail.data?.counts
  const running = job?.status === 'running'

  // Build the node set: known devices expected in scope (pending/probing) UNION
  // every result row (the live truth). Result rows always win.
  const nodes = useMemo(() => {
    const byIP = new Map<string, Node>()
    // 1) pre-populate known devices in scope so they show before their result lands
    if (job?.scope_cidr) {
      for (const d of devices.data ?? []) {
        if (d.primary_ip && ipInCidr(d.primary_ip, job.scope_cidr)) {
          byIP.set(d.primary_ip, deriveNode(d.primary_ip, undefined, d, !!running))
        }
      }
    }
    // 2) overlay result rows
    for (const r of results) {
      const d = r.device_id ? devMap.get(r.device_id) : devByIP.get(r.ip)
      byIP.set(r.ip, deriveNode(r.ip, r, d, !!running))
    }
    // order: most-actionable first, then by IP
    const order: NodeState[] = ['missed', 'failed', 'unmanaged', 'needs_agent', 'managed', 'online', 'probing', 'pending']
    return [...byIP.values()].sort((a, b) => {
      const oa = order.indexOf(a.state), ob = order.indexOf(b.state)
      if (oa !== ob) return oa - ob
      return (ipToInt(a.ip) ?? 0) - (ipToInt(b.ip) ?? 0)
    })
  }, [results, devices.data, devMap, devByIP, job, running])

  const counters = useMemo(() => {
    const c: Record<string, number> = { total: nodes.length, probing: 0, online: 0, managed: 0, unmanaged: 0, credFail: 0, needsAgent: 0, missed: 0 }
    for (const n of nodes) {
      if (n.state === 'probing' || n.state === 'pending') c.probing++
      if (['online', 'managed', 'unmanaged', 'needs_agent'].includes(n.state)) c.online++
      if (n.state === 'managed') c.managed++
      if (n.state === 'unmanaged') c.unmanaged++
      if (n.state === 'needs_agent') c.needsAgent++
      if (n.d?.management === 'credential_failed') c.credFail++
      if (n.state === 'missed') c.missed++
    }
    return c
  }, [nodes])

  const filtered = useMemo(() => nodes.filter((n) => {
    switch (filter) {
      case 'all': return true
      case 'probing': return n.state === 'probing' || n.state === 'pending'
      case 'online': return ['online', 'managed', 'unmanaged', 'needs_agent'].includes(n.state)
      case 'managed': return n.state === 'managed'
      case 'unmanaged': return n.state === 'unmanaged'
      case 'credential_failed': return n.d?.management === 'credential_failed'
      case 'needs_agent': return n.state === 'needs_agent'
      case 'missing_classification': return isMissingClass(n.d)
      case 'known_missed': return n.state === 'missed'
      case 'recovered': return n.recovered
      case 'relay_agent': return n.r?.probe_data?.collected_via === 'relay_agent'
      default: return true
    }
  }), [nodes, filter])

  const selNode = sel ? nodes.find((n) => n.ip === sel) : undefined

  if (!jobId) return null
  return (
    <div>
      <PageHeader title="Live Discovery" icon={Radar}
        subtitle={job ? `${job.scope_cidr ?? 'import'} · ${job.status}${running ? ` · elapsed ${duration(job.started_at, null)}` : ` · ${duration(job.started_at, job.finished_at)}`}` : 'Loading…'}
        actions={<>
          <Link className="btn btn-ghost btn-sm" to="/discovery/jobs"><ArrowLeft size={14} /> All jobs</Link>
          <Link className="btn btn-ghost btn-sm" to={`/discovery/jobs/${jobId}/results`}><Table2 size={14} /> Table View</Link>
          {job?.scope_cidr && <button className="btn btn-sm" disabled={rerun.isPending} onClick={() => rerun.mutate()}><RefreshCw size={14} /> Re-run</button>}
        </>} />

      {/* Live counters */}
      <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))' }}>
        <MiniCounter label="Targets" value={counts?.targets_probed ?? job?.host_count ?? 0} color="#64748b" />
        <MiniCounter label="Probing" value={counters.probing} color={running ? '#0ea5e9' : '#94a3b8'} pulse={running && counters.probing > 0} />
        <MiniCounter label="Online" value={counters.online} color="#2563eb" />
        <MiniCounter label="Managed" value={counters.managed} color="#16a34a" />
        <MiniCounter label="Unmanaged" value={counters.unmanaged} color="#d97706" />
        <MiniCounter label="Cred failed" value={counters.credFail} color="#dc2626" />
        <MiniCounter label="Needs agent" value={counters.needsAgent} color="#7c3aed" />
        <MiniCounter label="Recovered" value={counts?.known_recovered_by_retry ?? 0} color="#16a34a" />
        <MiniCounter label="Known missed" value={counts?.known_missed_this_run ?? counters.missed} color="#b91c1c" />
        <MiniCounter label="Newly found" value={counts?.newly_discovered ?? 0} color="#0ea5e9" />
      </div>

      {msg && <div className="banner" style={{ margin: '0 0 12px', fontSize: 13 }}>{msg}</div>}

      {/* Filters + legend */}
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center', margin: '4px 0 12px' }}>
        {FILTERS.map((f) => <button key={f} className={'seg-chip' + (filter === f ? ' active' : '')} onClick={() => setFilter(f)}>{FILTER_LABEL[f]}</button>)}
        <span style={{ flex: 1 }} />
        <Legend />
      </div>

      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
        {/* Board */}
        <div style={{ flex: 1, minWidth: 0 }}>
          {detail.isLoading && <div className="loading">Loading…</div>}
          {job && nodes.length === 0 && <EmptyState icon={Radar} title="No devices yet" message={running ? 'Scanning…' : 'No host results recorded for this job.'} />}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: 10 }}>
            {filtered.map((n) => <DeviceCard key={n.ip} n={n} selected={sel === n.ip} onClick={() => setSel(n.ip)} />)}
          </div>
        </div>

        {/* Detail side panel */}
        {selNode && (
          <DetailPanel n={selNode} locPath={locPath} onClose={() => setSel(null)}
            onEdit={() => selNode.d && setEditDev(selNode.d)}
            onRescan={() => rescanIP.mutate(selNode.ip)}
            onReclassify={() => selNode.d && reclassify.mutate(selNode.d.id)}
            onTest={() => selNode.d && testCreds.mutate(selNode.d.id)}
            busy={rescanIP.isPending || reclassify.isPending || testCreds.isPending} />
        )}
      </div>

      <p className="muted" style={{ fontSize: 11, marginTop: 10 }}>
        Live board polls every 1.5s while the scan runs; it reads the same data as the Table View, so the two always match. Online and Managed are separate axes — open ports never imply Managed.
      </p>
      {editDev && <EditDevice device={editDev} onClose={() => setEditDev(null)} />}
    </div>
  )
}

function MiniCounter({ label, value, color, pulse }: { label: string; value: number; color: string; pulse?: boolean }) {
  return (
    <div className="card" style={{ padding: '10px 12px', borderLeft: `3px solid ${color}` }}>
      <div style={{ fontSize: 22, fontWeight: 700, color, display: 'flex', alignItems: 'center', gap: 6 }}>
        {value}{pulse && <span style={{ width: 8, height: 8, borderRadius: 8, background: color, animation: 'kdrpulse 1s ease-in-out infinite' }} />}
      </div>
      <div className="muted" style={{ fontSize: 11 }}>{label}</div>
    </div>
  )
}

function Legend() {
  const items: [string, string][] = [
    ['#94a3b8', 'Pending'], ['#0ea5e9', 'Probing'], ['#2563eb', 'Online'], ['#16a34a', 'Managed'],
    ['#d97706', 'Unmanaged'], ['#7c3aed', 'Needs agent'], ['#b91c1c', 'Missed'],
  ]
  return (
    <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', fontSize: 11 }}>
      {items.map(([c, l]) => <span key={l} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, borderRadius: 3, background: c }} />{l}</span>)}
    </div>
  )
}

function protoBadges(r?: DiscoveryResult) {
  const p = r?.probe_data ?? {}
  const out: { kind: string; tone: string; title: string }[] = []
  for (const a of p.cred_attempts ?? []) {
    const tone = a.success ? 'up' : a.category === 'auth_failed' ? 'down' : 'unknown'
    out.push({ kind: a.kind || a.protocol, tone, title: a.success ? 'success' : a.category })
  }
  for (const op of p.opportunistic_protocols ?? []) out.push({ kind: op, tone: 'info', title: 'opportunistic' })
  for (const sk of p.skipped_protocols ?? []) out.push({ kind: sk, tone: 'muted', title: 'skipped' })
  return out
}

function DeviceCard({ n, selected, onClick }: { n: Node; selected: boolean; onClick: () => void }) {
  const m = STATE_META[n.state]
  const Icon = CAT_ICON[n.d?.category ?? n.r?.category ?? 'unknown'] ?? HelpCircle
  const probing = n.state === 'probing'
  const badges = protoBadges(n.r).slice(0, 6)
  return (
    <button onClick={onClick} style={{
      textAlign: 'left', cursor: 'pointer', border: `1px solid ${m.color}${selected ? '' : '55'}`,
      borderLeft: `4px solid ${m.color}`, borderRadius: 8, padding: 10, background: m.bg,
      boxShadow: selected ? `0 0 0 2px ${m.color}66` : 'none', position: 'relative',
      opacity: n.state === 'pending' ? 0.6 : 1, animation: probing ? 'kdrpulse 1.4s ease-in-out infinite' : 'none',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ display: 'inline-flex', width: 26, height: 26, borderRadius: 6, background: `${m.color}22`, color: m.color, alignItems: 'center', justifyContent: 'center' }}><Icon size={15} /></span>
        <div style={{ minWidth: 0 }}>
          <div className="mono" style={{ fontSize: 13, fontWeight: 600 }}>{n.ip}</div>
          <div className="muted" style={{ fontSize: 11, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{n.d?.name || (n.state === 'missed' ? 'known device' : 'discovering…')}</div>
        </div>
      </div>
      <div style={{ marginTop: 6, fontSize: 11, fontWeight: 600, color: m.color }}>
        {m.label}{n.recovered ? ' · recovered' : ''}{n.newly ? ' · new' : ''}
      </div>
      {n.state === 'managed' && n.method && <div className="muted" style={{ fontSize: 10 }}>via {n.method}</div>}
      {(n.state === 'unmanaged' || n.state === 'needs_agent') && n.reason && <div style={{ fontSize: 10, color: m.color }}>{n.reason}</div>}
      {badges.length > 0 && (
        <div style={{ display: 'flex', gap: 3, flexWrap: 'wrap', marginTop: 6 }}>
          {badges.map((b, i) => <span key={i} className={`badge badge-${b.tone}`} title={b.title} style={{ fontSize: 9, padding: '1px 4px' }}>{b.kind}</span>)}
        </div>
      )}
    </button>
  )
}

function DetailPanel({ n, locPath, onClose, onEdit, onRescan, onReclassify, onTest, busy }: {
  n: Node; locPath: Record<string, string>; onClose: () => void
  onEdit: () => void; onRescan: () => void; onReclassify: () => void; onTest: () => void; busy: boolean
}) {
  const { r, d } = n
  const p = r?.probe_data ?? {}
  const Icon = CAT_ICON[d?.category ?? r?.category ?? 'unknown'] ?? HelpCircle
  const m = STATE_META[n.state]
  // plain render helper (NOT a component) so it isn't re-created as a component each render
  const row = (k: string, v: React.ReactNode) => (v ? <div style={{ display: 'flex', gap: 8, fontSize: 12, padding: '2px 0' }}><span className="muted" style={{ minWidth: 110 }}>{k}</span><span>{v}</span></div> : null)
  return (
    <div className="card" style={{ width: 340, flexShrink: 0, padding: 14, position: 'sticky', top: 12, alignSelf: 'flex-start', maxHeight: 'calc(100vh - 100px)', overflowY: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ display: 'inline-flex', width: 30, height: 30, borderRadius: 7, background: `${m.color}22`, color: m.color, alignItems: 'center', justifyContent: 'center' }}><Icon size={17} /></span>
        <div style={{ flex: 1 }}>
          <div className="mono" style={{ fontWeight: 700 }}>{n.ip}</div>
          <div style={{ fontSize: 11, color: m.color, fontWeight: 600 }}>{m.label}{n.recovered ? ' · recovered by retry' : ''}</div>
        </div>
        <button className="btn btn-ghost btn-xs" onClick={onClose}><X size={14} /></button>
      </div>
      <div style={{ marginTop: 10 }}>
        {row('Hostname', d?.hostname)}
        {row('Reachability', d ? <ReachabilityBadge value={d.reachability} /> : '—')}
        {row('Management', d ? <ManagementBadge value={d.management} managedBy={d.managed_by} /> : '—')}
        {row('Category', (d?.category ?? r?.category ?? 'unknown').replace(/_/g, ' ') + (typeof p.confidence === 'number' && p.confidence > 0 ? ` · ${p.confidence}%` : ''))}
        {row('Vendor / Model', d?.vendor ? `${d.vendor}${d.model ? ' / ' + d.model : ''}` : undefined)}
        {row('Location', d?.location_id ? locPath[d.location_id] : undefined)}
        {row('Open ports', (p.open_ports ?? []).join(', ') || undefined)}
        {row('Expected', (p.expected_protocols ?? []).join(', ').toUpperCase() || undefined)}
        {row('Opportunistic', (p.opportunistic_protocols ?? []).join(', ').toUpperCase() || undefined)}
        {row('Skipped', (p.skipped_protocols ?? []).join(', ') || undefined)}
        {row('Bound credential', p.bound_cred)}
        {row('Collected via', p.collected_via)}
        {n.r?.retry_count ? row('Retries', `${n.r.retry_count}×`) : null}
        {row('Next action', r?.error || p.next_action)}
        {(p.cred_attempts ?? []).length > 0 && (
          <div style={{ marginTop: 6 }}>
            <div className="muted" style={{ fontSize: 11 }}>Credential attempts</div>
            {(p.cred_attempts ?? []).map((a, i) => (
              <div key={i} style={{ fontSize: 11 }}><span className={`badge badge-${a.success ? 'up' : a.category === 'auth_failed' ? 'down' : 'unknown'}`}>{a.kind}</span> <span className="muted">{a.success ? 'ok' : a.category}</span></div>
            ))}
          </div>
        )}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 12 }}>
        {d && <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`}>Open Device</Link>}
        {d && <button className="btn btn-ghost btn-xs" onClick={onEdit}><Pencil size={12} /> Edit</button>}
        <button className="btn btn-ghost btn-xs" disabled={busy} onClick={onRescan}><RefreshCw size={12} /> Re-scan</button>
        {d && <button className="btn btn-ghost btn-xs" disabled={busy} onClick={onTest}><KeyRound size={12} /> Test creds</button>}
        {d && <button className="btn btn-ghost btn-xs" disabled={busy} onClick={onReclassify}>Reclassify</button>}
        {d && <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`}>Bind / Agent</Link>}
      </div>
    </div>
  )
}
