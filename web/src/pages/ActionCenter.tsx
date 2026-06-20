import { useMemo, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Wrench, RefreshCw, KeyRound, ShieldAlert, Server, Globe, Clock, BellOff, ChevronDown, ChevronRight, CircleCheck, Radar } from 'lucide-react'
import { api, type ActionCenterReport, type AcRow, type AcQueue, type BulkCollectOSResult, type TrustAuditReport } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'
import { ACTIONABLE_PATTERNS } from '../trustPatterns'

// TrustGapsPanel surfaces the ACTIONABLE Discovery Trust Audit findings (a stronger collector
// exists but was skipped) inside the Action Center. Fetched lazily — the audit actively re-probes
// weak hosts and is slow — so it never blocks the main remediation queues.
function TrustGapsPanel() {
  const q = useQuery({ queryKey: ['trust-audit'], queryFn: () => api.get<TrustAuditReport>('/discovery/trust-audit'), staleTime: 120_000 })
  const actionable = (q.data?.patterns ?? []).filter((p) => ACTIONABLE_PATTERNS.has(p.pattern))
  const total = actionable.reduce((n, p) => n + p.count, 0)
  return (
    <Panel title="Discovery trust gaps" icon={Radar} subtitle={q.data ? `${total} actionable` : undefined}
      actions={<Link to="/trust-audit" className="btn btn-ghost btn-xs">Open Trust Audit →</Link>}>
      {q.isLoading && <div className="loading">Auditing the fleet (active probe)…</div>}
      {q.data && actionable.length === 0 && <EmptyState icon={CircleCheck} title="No skipped collectors" message="Every device with stronger-collector evidence has had it attempted." />}
      {actionable.length > 0 && (
        <table className="data-table">
          <thead><tr><th>Skipped-collector pattern</th><th>Count</th><th>Device types</th><th>Examples</th></tr></thead>
          <tbody>
            {actionable.map((p) => (
              <tr key={p.pattern}>
                <td className="cell-name">{p.pattern.replace(/_/g, ' ')}</td>
                <td>{p.count}</td>
                <td className="muted">{p.device_types.join(', ')}</td>
                <td className="muted" style={{ fontSize: 12 }}>{p.examples.slice(0, 4).join(', ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

const QUEUE_ICON: Record<string, typeof Wrench> = {
  credential_failed: KeyRound, not_authorized: ShieldAlert, needs_agent: Server,
  relay_job_failed: RefreshCw, stale_collection: Clock, web_authenticated: Globe,
}
const KIND_BADGE: Record<string, { label: string; cls: string }> = {
  recollect: { label: 'Auto: re-collect', cls: 'badge-up' },
  credential: { label: 'Needs credential', cls: 'badge-warn' },
  host_fix: { label: 'Host-side fix', cls: 'badge-crit' },
  agent: { label: 'Needs agent', cls: 'badge-warn' },
  info: { label: 'Review', cls: 'badge-unknown' },
}
const WMI_CHECKLIST = [
  'Add the service account to local Administrators (or grant DCOM + WMI namespace rights).',
  'dcomcnfg → Component Services → My Computer → COM Security: grant Remote Activation.',
  'WMI Control → Security → Root/CIMV2: Enable Account + Remote Enable for the account.',
  'Allow "Windows Management Instrumentation (WMI-In)" through the firewall.',
  'Then re-scan / re-collect from the Action Center.',
]

// Action Center — turns the live operational backlog into safe, controlled remediation queues.
// Active (current-state) issues only; historical/resolved are reported separately. Actions reuse
// the real collect/test endpoints — no fake fixes, one-shot operator-triggered (no retry storm).
export function ActionCenter() {
  const q = useQuery({ queryKey: ['action-center'], queryFn: () => api.get<ActionCenterReport>('/action-center') })
  const [queueF, setQueueF] = useState('')
  const [catF, setCatF] = useState('')
  const [roleF, setRoleF] = useState('')
  const [search, setSearch] = useState('')
  const [ageF, setAgeF] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['credential_failed', 'not_authorized']))
  const [sel, setSel] = useState<Set<string>>(new Set()) // keyed queue|device
  const [showWmi, setShowWmi] = useState(false)
  const [toast, setToast] = useState<string>('')

  const recollect = useMutation({
    mutationFn: (ids: string[]) => api.post<BulkCollectOSResult>('/data-quality/collect-os', { device_ids: ids }),
    onSuccess: (r) => { setToast(`Re-collect queued: ${r.collected} started, ${r.failed} failed to enqueue.`); setSel(new Set()); q.refetch() },
    onError: (e: unknown) => setToast(`Re-collect failed: ${e instanceof Error ? e.message : String(e)}`),
  })
  const snooze = useMutation({
    mutationFn: (v: { device_id: string; issue_key: string; days: number; reason: string }) => api.post('/action-center/snooze', v),
    onSuccess: () => { setToast('Snoozed.'); q.refetch() },
  })

  const data = q.data
  const [now] = useState(() => Date.now())
  const staleAge = (r: AcRow) => (r.last_success ? now - Date.parse(r.last_success) : Infinity)
  const rowMatches = (r: AcRow) => {
    if (catF && r.category !== catF) return false
    if (roleF && r.server_role !== roleF) return false
    if (search) { const s = search.toLowerCase(); if (!(`${r.name} ${r.ip ?? ''}`.toLowerCase().includes(s))) return false }
    if (ageF === 'week' && staleAge(r) < 7 * 86400_000) return false
    if (ageF === 'day' && staleAge(r) < 86400_000) return false
    return true
  }
  const visibleQueues = useMemo(() => (data?.queues ?? [])
    .filter((qq) => !queueF || qq.key === queueF)
    .map((qq) => ({ ...qq, filtered: (qq.rows ?? []).filter(rowMatches) })), [data, queueF, catF, roleF, search, ageF])

  const allCats = useMemo(() => [...new Set((data?.queues ?? []).flatMap((qq) => (qq.rows ?? []).map((r) => r.category).filter(Boolean)))] as string[], [data])
  const allRoles = useMemo(() => [...new Set((data?.queues ?? []).flatMap((qq) => (qq.rows ?? []).map((r) => r.server_role).filter(Boolean)))] as string[], [data])

  const keyOf = (qk: string, id: string) => `${qk}|${id}`
  const toggle = (k: string) => setSel((s) => { const n = new Set(s); if (n.has(k)) n.delete(k); else n.add(k); return n })
  const selectedIds = (qk: string, rows: AcRow[]) => rows.filter((r) => sel.has(keyOf(qk, r.device_id))).map((r) => r.device_id)

  const doRecollect = (ids: string[], confirmLabel?: string) => {
    if (!ids.length) return
    if (confirmLabel && !window.confirm(`Re-collect all ${ids.length} device(s) in "${confirmLabel}"? This enqueues one collection job each.`)) return
    recollect.mutate(ids)
  }
  const doSnooze = (r: AcRow, issueKey: string) => {
    if (!window.confirm(`Snooze "${r.name}" from this queue for 7 days? (e.g. intentionally offline / unsupported)`)) return
    snooze.mutate({ device_id: r.device_id, issue_key: issueKey, days: 7, reason: 'operator snooze' })
  }
  const fieldStyle = { padding: '5px 8px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 12 }

  return (
    <div>
      <PageHeader title="Action Center" icon={Wrench} subtitle="Active remediation backlog — what needs action, why, the recommended fix, and whether it is auto-actionable" />
      {q.isLoading && <div className="loading">Loading remediation queues…</div>}
      {toast && <div style={{ marginBottom: 12, padding: '8px 12px', borderRadius: 6, background: 'var(--surface-2)', border: '1px solid #2a3a47', fontSize: 13 }}>{toast} <button className="btn btn-ghost btn-xs" onClick={() => setToast('')}>dismiss</button></div>}

      {data && (
        <>
          <div className="kpi-grid">
            {data.queues.map((qq) => {
              const Icon = QUEUE_ICON[qq.key] ?? Wrench
              return <Kpi key={qq.key} label={qq.label} value={qq.count} icon={Icon}
                tone={qq.count === 0 ? 'default' : qq.severity === 'critical' ? 'crit' : qq.action_kind === 'recollect' ? 'info' : 'warn'}
                sub={KIND_BADGE[qq.action_kind]?.label} onClick={() => setQueueF(queueF === qq.key ? '' : qq.key)} />
            })}
          </div>

          <Panel title="Historical vs virtualization context" subtitle="resolved / non-queue findings" >
            <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', fontSize: 13 }}>
              <span><b>{data.historical['credential_failed_resolved'] ?? 0}</b> devices had a credential rejection in history but are <b>now managed</b> (no action needed).</span>
              <span><b>{data.virtualization.vms_unlinked}</b>/{data.virtualization.vms_total} VMs not linked to a device.</span>
              <span><b>{data.virtualization.datastores_warn}</b> datastores below 20% free{data.virtualization.datastores_crit > 0 ? `, ${data.virtualization.datastores_crit} critical` : ''}.</span>
              <Link to="/coverage">Open Coverage report →</Link>
            </div>
          </Panel>

          <TrustGapsPanel />

          <Panel title="Filters" pad>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <input style={{ ...fieldStyle, width: 200 }} placeholder="Search name / IP / subnet" value={search} onChange={(e) => setSearch(e.target.value)} />
              <select style={fieldStyle} value={queueF} onChange={(e) => setQueueF(e.target.value)}><option value="">All queues</option>{data.queues.map((qq) => <option key={qq.key} value={qq.key}>{qq.label}</option>)}</select>
              <select style={fieldStyle} value={catF} onChange={(e) => setCatF(e.target.value)}><option value="">All categories</option>{allCats.map((c) => <option key={c} value={c}>{c}</option>)}</select>
              <select style={fieldStyle} value={roleF} onChange={(e) => setRoleF(e.target.value)}><option value="">All roles</option>{allRoles.map((r) => <option key={r} value={r}>{r.replace(/_/g, ' ')}</option>)}</select>
              <select style={fieldStyle} value={ageF} onChange={(e) => setAgeF(e.target.value)}><option value="">Any age</option><option value="day">Stale &gt;1d</option><option value="week">Stale &gt;7d</option></select>
              {(queueF || catF || roleF || ageF || search) && <button className="btn btn-ghost btn-xs" onClick={() => { setQueueF(''); setCatF(''); setRoleF(''); setAgeF(''); setSearch('') }}>Clear</button>}
            </div>
          </Panel>

          {visibleQueues.every((qq) => qq.filtered.length === 0) && <EmptyState icon={CircleCheck} title="No active remediation items" message="Nothing matches these filters — the live backlog is clear here." />}

          {visibleQueues.map((qq) => qq.filtered.length > 0 && (
            <QueuePanel key={qq.key} qq={qq} rows={qq.filtered} expanded={expanded.has(qq.key)}
              onToggleExpand={() => setExpanded((s) => { const n = new Set(s); if (n.has(qq.key)) n.delete(qq.key); else n.add(qq.key); return n })}
              sel={sel} keyOf={keyOf} toggle={toggle} selectedIds={() => selectedIds(qq.key, qq.filtered)}
              busy={recollect.isPending || snooze.isPending}
              onRecollect={doRecollect} onSnooze={doSnooze} onShowWmi={() => setShowWmi(true)} />
          ))}

          {showWmi && (
            <div className="modal-scrim" onClick={() => setShowWmi(false)}>
              <div className="modal" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 560 }}>
                <div className="modal-head"><ShieldAlert size={16} /> Host-side WMI/DCOM remediation</div>
                <div className="modal-body">
                  <p className="muted" style={{ fontSize: 13 }}>The credential authenticates but the host denies WMI/DCOM. Grant access on the host, then re-collect:</p>
                  <ol style={{ fontSize: 13, lineHeight: 1.7 }}>{WMI_CHECKLIST.map((l, i) => <li key={i}>{l}</li>)}</ol>
                  <div style={{ textAlign: 'right' }}><button className="btn btn-sm" onClick={() => setShowWmi(false)}>Close</button></div>
                </div>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function QueuePanel({ qq, rows, expanded, onToggleExpand, sel, keyOf, toggle, selectedIds, busy, onRecollect, onSnooze, onShowWmi }: {
  qq: AcQueue; rows: AcRow[]; expanded: boolean; onToggleExpand: () => void
  sel: Set<string>; keyOf: (qk: string, id: string) => string; toggle: (k: string) => void; selectedIds: () => string[]
  busy: boolean; onRecollect: (ids: string[], confirmLabel?: string) => void; onSnooze: (r: AcRow, issueKey: string) => void; onShowWmi: () => void
}) {
  const kb = KIND_BADGE[qq.action_kind] ?? KIND_BADGE.info
  const selIds = selectedIds()
  const canCollect = qq.action_kind === 'recollect' || qq.action_kind === 'credential' || qq.action_kind === 'agent'
  return (
    <Panel pad={false} title={
      <span style={{ cursor: 'pointer' }} onClick={onToggleExpand}>{expanded ? <ChevronDown size={15} /> : <ChevronRight size={15} />} {qq.label} <span className={'badge ' + kb.cls} style={{ marginLeft: 6 }}>{kb.label}</span></span>
    } subtitle={`${rows.length}`} actions={
      <div style={{ display: 'flex', gap: 6 }}>
        {canCollect && <button className="btn btn-sm" disabled={busy || selIds.length === 0} onClick={() => onRecollect(selIds)}>Re-collect selected ({selIds.length})</button>}
        {canCollect && <button className="btn btn-sm btn-ghost" disabled={busy} onClick={() => onRecollect(rows.map((r) => r.device_id), qq.label)}>Re-collect group</button>}
        {qq.action_kind === 'host_fix' && <button className="btn btn-sm" onClick={onShowWmi}>Host-side checklist</button>}
      </div>
    }>
      <p className="muted" style={{ fontSize: 12, padding: '4px 12px 0' }}>{qq.description}</p>
      {expanded && (
        <table className="data-table">
          <thead><tr><th style={{ width: 24 }}></th><th>Device</th><th>Role / Category</th><th>Reason</th><th>Last success</th><th>Recommended action</th><th></th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.device_id}>
                <td><input type="checkbox" checked={sel.has(keyOf(qq.key, r.device_id))} onChange={() => toggle(keyOf(qq.key, r.device_id))} /></td>
                <td className="cell-name"><Link to={`/devices/${r.device_id}`}>{r.name}</Link><br /><small className="mono muted">{r.ip}</small></td>
                <td className="muted" style={{ fontSize: 12 }}>{(r.server_role ?? '').replace(/_/g, ' ') || '—'}<br /><small>{r.category}</small></td>
                <td style={{ fontSize: 12 }}>{r.reason}{r.last_error && <><br /><small className="muted" title={r.last_error}>err: {r.last_error.slice(0, 80)}</small></>}</td>
                <td className="muted" style={{ fontSize: 12 }}>{r.last_success ? timeAgo(r.last_success) : '—'}</td>
                <td className="muted" style={{ fontSize: 12 }}>{r.recommended_action}</td>
                <td className="cell-actions">
                  {canCollect && <button className="btn btn-ghost btn-xs" disabled={busy} onClick={() => onRecollect([r.device_id])}>Re-collect</button>}
                  {qq.action_kind === 'host_fix' && <button className="btn btn-ghost btn-xs" onClick={onShowWmi}>Checklist</button>}
                  <button className="btn btn-ghost btn-xs" disabled={busy} onClick={() => onSnooze(r, qq.key)}><BellOff size={12} /> Snooze</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
