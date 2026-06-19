import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, TriangleAlert, CircleCheck, ListChecks, Play, Plus, Wrench, ArrowUpCircle, Clock, X, Trash2, ChevronDown, ChevronRight, Layers } from 'lucide-react'
import { api, type Alert, type AlertRule, type AlertEvent, type MaintenanceWindow, type Device, type Location, locationPaths } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, StatusPill, TabBar, timeAgo } from '../components/ui'

const sevCls = (s: string) => (s === 'critical' ? 'badge-down' : s === 'warning' ? 'badge-warning' : 'badge-unknown')
const SeverityBadge = ({ s }: { s: string }) => <span className={`badge ${sevCls(s)}`}>{s}</span>
type Tab = 'alerts' | 'rules' | 'maintenance'

export function Alerts() {
  const qc = useQueryClient()
  const [tab, setTab] = useState<Tab>('alerts')
  const [showRule, setShowRule] = useState(false)
  const [timelineFor, setTimelineFor] = useState<Alert | null>(null)
  const [now] = useState(() => Date.now())

  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.get<Alert[]>('/alerts'), refetchInterval: 15_000 })
  const rules = useQuery({ queryKey: ['alert-rules'], queryFn: () => api.get<AlertRule[]>('/alert-rules') })
  const windows = useQuery({ queryKey: ['maintenance-windows'], queryFn: () => api.get<MaintenanceWindow[]>('/maintenance-windows') })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['alerts'] })
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    qc.invalidateQueries({ queryKey: ['maintenance-windows'] })
  }
  const evaluate = useMutation({ mutationFn: () => api.post('/alerts/evaluate', {}), onSuccess: invalidate })
  const toggleRule = useMutation({ mutationFn: (r: AlertRule) => api.patch(`/alert-rules/${r.id}`, { enabled: !r.enabled }), onSuccess: invalidate })
  const delRule = useMutation({ mutationFn: (id: string) => api.del(`/alert-rules/${id}`), onSuccess: invalidate })

  const list = useMemo(() => alerts.data ?? [], [alerts.data])
  const open = list.filter((a) => a.status === 'open').length
  const critical = list.filter((a) => a.severity === 'critical' && a.status !== 'resolved').length
  const acked = list.filter((a) => a.status === 'acknowledged').length
  const escalated = list.filter((a) => a.escalated && a.status !== 'resolved').length
  const activeRules = (rules.data ?? []).filter((r) => r.enabled).length
  const activeWindows = (windows.data ?? []).filter((w) => Date.parse(w.ends_at) > now && Date.parse(w.starts_at) <= now).length

  return (
    <div>
      <PageHeader
        title="Alerts" icon={Bell}
        subtitle="Rule-driven alerting — dedup, auto work-orders, auto-resolve, escalation, maintenance suppression"
        actions={
          <>
            <button className="btn btn-sm" disabled={evaluate.isPending} onClick={() => evaluate.mutate()}>
              <Play size={14} /> {evaluate.isPending ? 'Evaluating…' : 'Evaluate now'}
            </button>
            {tab === 'rules' && <button className="btn btn-primary btn-sm" onClick={() => setShowRule((v) => !v)}><Plus size={14} /> {showRule ? 'Cancel' : 'New rule'}</button>}
          </>
        }
      />

      <div className="kpi-grid">
        <Kpi label="Open Alerts" value={open} icon={Bell} tone={open > 0 ? 'crit' : 'default'} sub="unresolved" />
        <Kpi label="Critical" value={critical} icon={TriangleAlert} tone={critical > 0 ? 'crit' : 'default'} sub="active" />
        <Kpi label="Escalated" value={escalated} icon={ArrowUpCircle} tone={escalated > 0 ? 'crit' : 'default'} sub="unacknowledged" />
        <Kpi label="Acknowledged" value={acked} icon={CircleCheck} tone={acked > 0 ? 'warn' : 'default'} sub="in handling" />
        <Kpi label="Maintenance" value={activeWindows} icon={Wrench} tone={activeWindows > 0 ? 'info' : 'default'} sub="active windows" />
      </div>

      <TabBar
        tabs={[
          { key: 'alerts', label: 'Alerts', icon: Bell, count: open || undefined },
          { key: 'rules', label: 'Rules', icon: ListChecks, count: activeRules || undefined },
          { key: 'maintenance', label: 'Maintenance', icon: Wrench, count: activeWindows || undefined },
        ]}
        active={tab} onChange={(k) => setTab(k as Tab)}
      />

      {tab === 'alerts' && <AlertsTab list={list} loading={alerts.isLoading} onTimeline={setTimelineFor} onChange={invalidate} />}

      {tab === 'rules' && (
        <>
          {showRule && <RuleForm onDone={() => { setShowRule(false); invalidate() }} />}
          <Panel title="Alert Rules" icon={ListChecks} subtitle={`${rules.data?.length ?? 0}`} pad={false}>
            {rules.data && rules.data.length === 0 && <EmptyState icon={ListChecks} title="No alert rules" message="Add a rule to start alerting on monitoring state." action={<button className="btn btn-primary btn-sm" onClick={() => setShowRule(true)}>New rule</button>} />}
            {rules.data && rules.data.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Name</th><th>Trigger</th><th>Min fails</th><th>Category</th><th>Severity</th><th>Auto WO</th><th>Escalate</th><th>Enabled</th><th></th></tr></thead>
                <tbody>
                  {rules.data.map((r) => (
                    <tr key={r.id}>
                      <td className="cell-name">{r.name}</td>
                      <td>{r.trigger_status}</td>
                      <td>{r.min_failures}</td>
                      <td>{r.device_category ?? 'any'}</td>
                      <td><SeverityBadge s={r.severity} /></td>
                      <td>{r.auto_work_order ? `yes (${r.work_order_priority})` : 'no'}</td>
                      <td>{r.escalate_after_minutes > 0 ? `${r.escalate_after_minutes}m` : '—'}</td>
                      <td>{r.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                      <td className="cell-actions">
                        <button className="btn btn-ghost btn-xs" onClick={() => toggleRule.mutate(r)}>{r.enabled ? 'Disable' : 'Enable'}</button>
                        <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => delRule.mutate(r.id)}>Delete</button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </>
      )}

      {tab === 'maintenance' && <MaintenanceTab windows={windows.data ?? []} onChange={invalidate} />}

      {timelineFor && <TimelineDrawer alert={timelineFor} onClose={() => setTimelineFor(null)} onChange={invalidate} />}
    </div>
  )
}

const CONDITION_LABEL: Record<string, string> = {
  check: 'Device down', collection_stale: 'Collection stale', datastore_low: 'Datastore low free',
  agent_offline: 'Relay agent offline', virt_collection: 'Virtualization collection',
}
const REMEDIATION: Record<string, string> = {
  check: 'Check device power / network / firewall, then re-test reachability.',
  collection_stale: 'Re-run collection; verify the bound credential and host reachability.',
  datastore_low: 'Free space or expand the datastore.',
  agent_offline: 'Restart the site Relay Agent service so its devices can be collected.',
  virt_collection: 'Re-run vSphere/Hyper-V collection; check the collector credentials.',
}
// urgent-first ordering: critical down → critical state → other critical → warning (non-stale) → stale.
function alertRank(a: Alert): number {
  if (a.condition === 'check' && a.severity === 'critical') return 0
  if (a.kind === 'state' && a.severity === 'critical') return 1
  if (a.severity === 'critical') return 2
  if (a.severity === 'warning' && a.condition !== 'collection_stale') return 3
  return 4
}
const TypeBadge = ({ a }: { a: Alert }) => (a.kind === 'check' || a.check_id
  ? <span className="badge" style={{ background: '#334155', color: '#fff' }} title="Monitoring-check alert">Check</span>
  : <span className="badge" style={{ background: '#7c3aed', color: '#fff' }} title="Device/system state alert">State</span>)

function AlertsTab({ list, loading, onTimeline, onChange }: { list: Alert[]; loading: boolean; onTimeline: (a: Alert) => void; onChange: () => void }) {
  const [sevF, setSevF] = useState('')
  const [statusF, setStatusF] = useState('active')
  const [typeF, setTypeF] = useState('')
  const [condF, setCondF] = useState('')
  const [roleF, setRoleF] = useState('')
  const [ageF, setAgeF] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const post = (action: 'ack' | 'resolve', ids: string[]) => api.post('/alerts/bulk', { action, ids })
  const bulk = useMutation({ mutationFn: ({ action, ids }: { action: 'ack' | 'resolve'; ids: string[] }) => post(action, ids), onSuccess: () => { setSel(new Set()); onChange() } })
  const ackOne = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/ack`, {}), onSuccess: onChange })
  const resolveOne = useMutation({ mutationFn: (id: string) => api.post(`/alerts/${id}/resolve`, {}), onSuccess: onChange })

  // Summary cards from all non-resolved alerts (stable counts, independent of filters).
  const openAll = list.filter((a) => a.status !== 'resolved')
  const sum = {
    critical: openAll.filter((a) => a.severity === 'critical').length,
    warning: openAll.filter((a) => a.severity === 'warning').length,
    acked: openAll.filter((a) => a.status === 'acknowledged').length,
    check: openAll.filter((a) => (a.kind ?? (a.check_id ? 'check' : 'state')) === 'check').length,
    state: openAll.filter((a) => (a.kind ?? (a.check_id ? 'check' : 'state')) === 'state').length,
    stale: openAll.filter((a) => a.condition === 'collection_stale').length,
    down: openAll.filter((a) => a.condition === 'check').length,
    datastore: openAll.filter((a) => a.condition === 'datastore_low').length,
  }

  const [nowMs] = useState(() => Date.now())
  const ageOf = (a: Alert) => nowMs - Date.parse(a.opened_at)
  const filtered = useMemo(() => list.filter((a) => {
    if (statusF === 'active' ? a.status === 'resolved' : statusF !== 'all' && a.status !== statusF) return false
    if (sevF && a.severity !== sevF) return false
    if (typeF && (a.kind ?? (a.check_id ? 'check' : 'state')) !== typeF) return false
    if (condF && a.condition !== condF) return false
    if (roleF && a.server_role !== roleF) return false
    if (ageF === 'hour' && ageOf(a) > 3600_000) return false
    if (ageF === 'today' && ageOf(a) > 86400_000) return false
    if (ageF === 'old' && ageOf(a) <= 86400_000) return false
    return true
  }), [list, statusF, sevF, typeF, condF, roleF, ageF])

  // Group by condition+severity; collapse collection_stale or any large (>8) group by default.
  const groups = useMemo(() => {
    const m = new Map<string, Alert[]>()
    for (const a of filtered) {
      const k = `${a.condition}|${a.severity}`
      ;(m.get(k) ?? m.set(k, []).get(k)!).push(a)
    }
    return m
  }, [filtered])
  const isCollapsed = (cond: string, n: number) => cond === 'collection_stale' || n > 8
  const individual: Alert[] = []
  const collapsed: { key: string; cond: string; sev: string; members: Alert[] }[] = []
  for (const [k, members] of groups) {
    const [cond, sev] = k.split('|')
    if (isCollapsed(cond, members.length)) collapsed.push({ key: k, cond, sev, members })
    else individual.push(...members)
  }
  individual.sort((a, b) => alertRank(a) - alertRank(b) || Date.parse(b.opened_at) - Date.parse(a.opened_at))
  collapsed.sort((a, b) => (a.sev === 'critical' ? -1 : 1) - (b.sev === 'critical' ? -1 : 1) || b.members.length - a.members.length)

  const toggle = (id: string) => setSel((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const thresholdText = (a: Alert) => {
    if (a.condition === 'datastore_low') return `warn <${a.warn_threshold ?? 20}% / crit <${a.crit_threshold ?? 10}%`
    if (a.condition === 'collection_stale') return `warn >${a.warn_threshold ?? 24}h / crit >${a.crit_threshold ?? 72}h`
    return ''
  }
  const Row = ({ a }: { a: Alert }) => (
    <tr>
      <td><input type="checkbox" checked={sel.has(a.id)} onChange={() => toggle(a.id)} disabled={a.status === 'resolved'} /></td>
      <td><SeverityBadge s={a.severity} />{a.escalated && <span className="badge badge-down" style={{ marginLeft: 6 }}><ArrowUpCircle size={11} /> esc</span>}</td>
      <td><TypeBadge a={a} /></td>
      <td><StatusPill status={a.status === 'open' ? 'down' : a.status === 'acknowledged' ? 'warning' : 'up'} label={a.status} /></td>
      <td className="cell-name">{a.device_name || a.device_ip || '—'}{a.server_role && <small className="muted"> · {a.server_role.replace(/_/g, ' ')}</small>}</td>
      <td className="muted" style={{ fontSize: 12 }}>{a.message}{a.acknowledged_by && <small className="muted"> · ack by {a.acknowledged_by}</small>}{thresholdText(a) && <small className="muted"> · {thresholdText(a)}</small>}</td>
      <td className="muted">{timeAgo(a.opened_at)}</td>
      <td className="cell-actions">
        <button className="btn btn-ghost btn-xs" onClick={() => onTimeline(a)}><Clock size={12} /></button>
        {a.status === 'open' && <button className="btn btn-ghost btn-xs" onClick={() => ackOne.mutate(a.id)}>Ack</button>}
        {a.status !== 'resolved' && <button className="btn btn-ghost btn-xs" onClick={() => resolveOne.mutate(a.id)}>Resolve</button>}
      </td>
    </tr>
  )
  const fieldStyle = { padding: '5px 8px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 12 }

  return (
    <>
      <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(auto-fill,minmax(150px,1fr))' }}>
        <Kpi label="Critical open" value={sum.critical} icon={TriangleAlert} tone={sum.critical ? 'crit' : 'default'} />
        <Kpi label="Warning open" value={sum.warning} icon={Bell} tone={sum.warning ? 'warn' : 'default'} />
        <Kpi label="Acknowledged" value={sum.acked} icon={CircleCheck} tone={sum.acked ? 'warn' : 'default'} sub="unresolved" />
        <Kpi label="Down devices" value={sum.down} icon={TriangleAlert} tone={sum.down ? 'crit' : 'default'} />
        <Kpi label="Datastore low" value={sum.datastore} icon={Bell} tone={sum.datastore ? 'warn' : 'default'} />
        <Kpi label="Collection stale" value={sum.stale} icon={Clock} tone="default" />
        <Kpi label="Check-based" value={sum.check} icon={Bell} tone="default" />
        <Kpi label="State-based" value={sum.state} icon={Bell} tone="default" />
      </div>

      <Panel title="Alerts" icon={Bell} subtitle={`${filtered.filter((a) => a.status !== 'resolved').length} shown`} pad={false}
        actions={
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <select style={fieldStyle} value={statusF} onChange={(e) => setStatusF(e.target.value)}><option value="active">Active</option><option value="open">Open</option><option value="acknowledged">Ack</option><option value="resolved">Resolved</option><option value="all">All</option></select>
            <select style={fieldStyle} value={sevF} onChange={(e) => setSevF(e.target.value)}><option value="">All sev</option><option value="critical">Critical</option><option value="warning">Warning</option><option value="info">Info</option></select>
            <select style={fieldStyle} value={typeF} onChange={(e) => setTypeF(e.target.value)}><option value="">All types</option><option value="check">Check</option><option value="state">State</option></select>
            <select style={fieldStyle} value={condF} onChange={(e) => setCondF(e.target.value)}><option value="">All rules</option><option value="check">Device down</option><option value="collection_stale">Collection stale</option><option value="datastore_low">Datastore low</option><option value="agent_offline">Agent offline</option><option value="virt_collection">Virtualization</option></select>
            <select style={fieldStyle} value={roleF} onChange={(e) => setRoleF(e.target.value)}><option value="">All roles</option><option value="virtual_host_esxi">ESXi Host</option><option value="virtual_host_hyperv">Hyper-V Host</option><option value="virtual_machine">VM</option><option value="physical_server">Physical</option><option value="unknown_server">Unknown</option></select>
            <select style={fieldStyle} value={ageF} onChange={(e) => setAgeF(e.target.value)}><option value="">Any age</option><option value="hour">Last hour</option><option value="today">Today</option><option value="old">&gt;24h</option></select>
          </div>
        }>
        {sel.size > 0 && (
          <div style={{ padding: '8px 10px', background: 'var(--surface-2)', display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ fontSize: 13 }}>{sel.size} selected</span>
            <button className="btn btn-sm" disabled={bulk.isPending} onClick={() => bulk.mutate({ action: 'ack', ids: [...sel] })}>Acknowledge selected</button>
            <button className="btn btn-sm" disabled={bulk.isPending} onClick={() => bulk.mutate({ action: 'resolve', ids: [...sel] })}>Resolve selected</button>
            <button className="btn btn-ghost btn-sm" onClick={() => setSel(new Set())}>Clear</button>
          </div>
        )}
        {loading && <div className="loading">Loading…</div>}
        {!loading && individual.length === 0 && collapsed.length === 0 && <EmptyState icon={CircleCheck} title="No alerts" message="Nothing matches these filters." />}
        {(individual.length > 0 || collapsed.length > 0) && (
          <table className="data-table">
            <thead><tr><th style={{ width: 24 }}></th><th>Severity</th><th>Type</th><th>Status</th><th>Device</th><th>Reason</th><th>Age</th><th></th></tr></thead>
            <tbody>
              {individual.map((a) => <Row key={a.id} a={a} />)}
              {collapsed.map((g) => {
                const openMembers = g.members.filter((m) => m.status !== 'resolved')
                const exp = expanded.has(g.key)
                return (
                  <>
                    <tr key={g.key} style={{ background: 'var(--surface-2)', cursor: 'pointer' }} onClick={() => setExpanded((s) => { const n = new Set(s); if (n.has(g.key)) n.delete(g.key); else n.add(g.key); return n })}>
                      <td>{exp ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</td>
                      <td><SeverityBadge s={g.sev} /></td>
                      <td><span className="badge" style={{ background: '#7c3aed', color: '#fff' }}>State</span></td>
                      <td colSpan={2} className="cell-name"><Layers size={13} /> {g.members.length} {g.members.length === 1 ? 'device' : 'devices'} — {CONDITION_LABEL[g.cond] ?? g.cond}</td>
                      <td className="muted" style={{ fontSize: 12 }}>{REMEDIATION[g.cond] ?? ''}</td>
                      <td></td>
                      <td className="cell-actions" onClick={(e) => e.stopPropagation()}>
                        <button className="btn btn-ghost btn-xs" disabled={bulk.isPending || openMembers.length === 0} onClick={() => bulk.mutate({ action: 'ack', ids: openMembers.map((m) => m.id) })}>Ack group</button>
                      </td>
                    </tr>
                    {exp && g.members.map((a) => <Row key={a.id} a={a} />)}
                  </>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  )
}

function TimelineDrawer({ alert, onClose, onChange }: { alert: Alert; onClose: () => void; onChange: () => void }) {
  const qc = useQueryClient()
  const [note, setNote] = useState('')
  const events = useQuery({ queryKey: ['alert-timeline', alert.id], queryFn: () => api.get<AlertEvent[]>(`/alerts/${alert.id}/timeline`) })
  const addNote = useMutation({
    mutationFn: () => api.post(`/alerts/${alert.id}/note`, { note }),
    onSuccess: () => { setNote(''); qc.invalidateQueries({ queryKey: ['alert-timeline', alert.id] }); onChange() },
  })
  const kindColor: Record<string, string> = { opened: 'var(--crit)', acknowledged: 'var(--warn)', resolved: 'var(--ok)', escalated: 'var(--crit)', note: 'var(--text-muted)', suppressed: 'var(--text-muted)' }
  return (
    <div className="drawer-scrim" onClick={onClose}>
      <div className="drawer" onClick={(e) => e.stopPropagation()} style={{ position: 'fixed', top: 0, right: 0, height: '100vh', width: 'min(460px,92vw)', background: 'var(--surface)', borderLeft: '1px solid var(--border)', boxShadow: '-8px 0 24px rgba(0,0,0,.18)', padding: 20, overflowY: 'auto', zIndex: 60 }}>
        <div className="row" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <div><h3 style={{ margin: 0, fontSize: 15 }}>Alert Timeline</h3><div className="muted" style={{ fontSize: 12, marginTop: 4 }}>{alert.message}</div></div>
          <button className="btn btn-ghost btn-xs" onClick={onClose}><X size={15} /></button>
        </div>
        <div className="stack" style={{ gap: 0, marginTop: 16 }}>
          {events.isLoading && <div className="loading">Loading…</div>}
          {(events.data ?? []).map((ev) => (
            <div key={ev.id} style={{ display: 'flex', gap: 10, padding: '8px 0', borderBottom: '1px solid var(--surface-3)' }}>
              <span style={{ width: 8, height: 8, borderRadius: '50%', background: kindColor[ev.kind] ?? 'var(--text-muted)', marginTop: 5, flex: '0 0 auto' }} />
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 13, fontWeight: 600, textTransform: 'capitalize' }}>{ev.kind} <span className="muted" style={{ fontWeight: 400 }}>· {ev.actor}</span></div>
                {ev.note && <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>{ev.note}</div>}
                <div className="muted" style={{ fontSize: 11, marginTop: 2 }}>{new Date(ev.at).toLocaleString()} · {timeAgo(ev.at)}</div>
              </div>
            </div>
          ))}
          {events.data && events.data.length === 0 && <div className="muted" style={{ fontSize: 13 }}>No timeline events yet.</div>}
        </div>
        <div style={{ marginTop: 16 }}>
          <textarea className="field" rows={2} style={{ width: '100%', resize: 'vertical' }} value={note} onChange={(e) => setNote(e.target.value)} placeholder="Add a note…" />
          <button className="btn btn-primary btn-sm" style={{ marginTop: 8 }} disabled={!note.trim() || addNote.isPending} onClick={() => addNote.mutate()}>Add note</button>
        </div>
      </div>
    </div>
  )
}

function MaintenanceTab({ windows, onChange }: { windows: MaintenanceWindow[]; onChange: () => void }) {
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])
  const devName = useMemo(() => new Map((devices.data ?? []).map((d) => [d.id, d.name])), [devices.data])

  const [scope, setScope] = useState('device')
  const [deviceId, setDeviceId] = useState('')
  const [locationId, setLocationId] = useState('')
  const [reason, setReason] = useState('')
  const [hours, setHours] = useState('2')
  const [now] = useState(() => Date.now())

  const create = useMutation({
    mutationFn: () => {
      const now = new Date()
      const end = new Date(now.getTime() + (Number(hours) || 1) * 3600_000)
      return api.post('/maintenance-windows', {
        scope, reason,
        device_id: scope === 'device' ? deviceId : null,
        location_id: scope === 'site' ? locationId : null,
        starts_at: now.toISOString(), ends_at: end.toISOString(),
      })
    },
    onSuccess: () => { setReason(''); setDeviceId(''); onChange() },
  })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/maintenance-windows/${id}`), onSuccess: onChange })
  const active = (w: MaintenanceWindow) => Date.parse(w.ends_at) > now && Date.parse(w.starts_at) <= now

  return (
    <>
      <Panel title="Schedule Maintenance Window" icon={Wrench} subtitle="Suppresses new alerts for the scope until it ends">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12, alignItems: 'end' }}>
          <label className="form-field">Scope
            <select className="field" value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="device">Device</option><option value="site">Site</option><option value="global">Global (all)</option>
            </select>
          </label>
          {scope === 'device' && (
            <label className="form-field">Device
              <select className="field" value={deviceId} onChange={(e) => setDeviceId(e.target.value)}>
                <option value="">Select…</option>
                {(devices.data ?? []).map((d) => <option key={d.id} value={d.id}>{d.name} {d.primary_ip ? `(${d.primary_ip})` : ''}</option>)}
              </select>
            </label>
          )}
          {scope === 'site' && (
            <label className="form-field">Site / location
              <select className="field" value={locationId} onChange={(e) => setLocationId(e.target.value)}>
                <option value="">Select…</option>
                {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id] ?? l.name}</option>)}
              </select>
            </label>
          )}
          <label className="form-field">Duration (hours)<input className="field" type="number" min="1" value={hours} onChange={(e) => setHours(e.target.value)} /></label>
          <label className="form-field">Reason<input className="field" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. firmware upgrade" /></label>
        </div>
        <button className="btn btn-primary btn-sm" style={{ marginTop: 14 }}
          disabled={create.isPending || (scope === 'device' && !deviceId) || (scope === 'site' && !locationId)}
          onClick={() => create.mutate()}>
          <Plus size={14} /> {create.isPending ? 'Scheduling…' : 'Schedule window'}
        </button>
        {create.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(create.error as Error).message}</span>}
      </Panel>

      <Panel title="Maintenance Windows" icon={Wrench} subtitle={`${windows.length}`} pad={false}>
        {windows.length === 0 && <EmptyState icon={Wrench} title="No maintenance windows" message="Schedule one to suppress alerts during planned work." />}
        {windows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Scope</th><th>Target</th><th>Reason</th><th>Window</th><th>State</th><th></th></tr></thead>
            <tbody>
              {windows.map((w) => (
                <tr key={w.id}>
                  <td>{w.scope}</td>
                  <td className="cell-name">{w.scope === 'global' ? 'All devices' : w.scope === 'device' ? (devName.get(w.device_id ?? '') ?? w.device_id) : (locPath[w.location_id ?? ''] ?? w.location_id)}</td>
                  <td className="muted">{w.reason || '—'}</td>
                  <td className="muted">{new Date(w.starts_at).toLocaleString()} → {new Date(w.ends_at).toLocaleTimeString()}</td>
                  <td>{active(w) ? <span className="badge badge-warning">active</span> : Date.parse(w.starts_at) > now ? <span className="badge badge-unknown">scheduled</span> : <span className="badge badge-disabled">ended</span>}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(w.id)}><Trash2 size={12} /> Cancel</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  )
}

function RuleForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [triggerStatus, setTriggerStatus] = useState('down')
  const [minFailures, setMinFailures] = useState('2')
  const [category, setCategory] = useState('')
  const [severity, setSeverity] = useState('warning')
  const [autoWo, setAutoWo] = useState(false)
  const [woPriority, setWoPriority] = useState('high')
  const [escalate, setEscalate] = useState('0')
  const m = useMutation({
    mutationFn: () => api.post<AlertRule>('/alert-rules', {
      name, trigger_status: triggerStatus, min_failures: Number(minFailures) || 0,
      device_category: category || null, severity,
      auto_work_order: autoWo, work_order_priority: woPriority,
      escalate_after_minutes: Number(escalate) || 0,
    }),
    onSuccess: onDone,
  })
  return (
    <Panel title="New Alert Rule" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))', gap: 12 }}>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label className="form-field">Trigger status
          <select className="field" value={triggerStatus} onChange={(e) => setTriggerStatus(e.target.value)}>
            <option value="down">down</option><option value="warning">warning</option>
          </select>
        </label>
        <label className="form-field">Min failures<input className="field" type="number" value={minFailures} onChange={(e) => setMinFailures(e.target.value)} /></label>
        <label className="form-field">Category (blank = any)<input className="field" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="switch / firewall / …" /></label>
        <label className="form-field">Severity
          <select className="field" value={severity} onChange={(e) => setSeverity(e.target.value)}>
            <option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
          </select>
        </label>
        <label className="form-field">Escalate after (min, 0 = never)<input className="field" type="number" min="0" value={escalate} onChange={(e) => setEscalate(e.target.value)} /></label>
        <label className="form-field">Auto work-order
          <select className="field" value={autoWo ? 'yes' : 'no'} onChange={(e) => setAutoWo(e.target.value === 'yes')}>
            <option value="no">no</option><option value="yes">yes</option>
          </select>
        </label>
        {autoWo && (
          <label className="form-field">WO priority
            <select className="field" value={woPriority} onChange={(e) => setWoPriority(e.target.value)}>
              <option value="low">low</option><option value="medium">medium</option>
              <option value="high">high</option><option value="critical">critical</option>
            </select>
          </label>
        )}
      </div>
      <div style={{ marginTop: 14 }}>
        <button className="btn btn-primary" disabled={!name || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Creating…' : 'Create rule'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
