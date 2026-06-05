import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FileClock, Download, Search, X } from 'lucide-react'
import { api, type AuditEntry, type AuditFacets } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo, usePaged, Pager } from '../components/ui'

const API_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'
const catCls = (c: string) => ({ user: 'badge-access', discovery: 'badge-lldp', inventory: 'badge-up', credential: 'badge-warning', config: 'badge-trunk', security: 'badge-down', work_order: 'badge-info', monitoring: 'badge-info', topology: 'badge-lldp' }[c] ?? 'badge-unknown')

type Filters = { category: string; actor: string; entity_type: string; action: string; q: string; from: string; to: string }
const empty: Filters = { category: '', actor: '', entity_type: '', action: '', q: '', from: '', to: '' }

function qs(f: Filters, extra?: Record<string, string>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries({ ...f, ...extra })) if (v) p.set(k, v)
  return p.toString()
}

export function AuditLog() {
  const [f, setF] = useState<Filters>(empty)
  const facets = useQuery({ queryKey: ['audit-facets'], queryFn: () => api.get<AuditFacets>('/audit-log/facets') })
  const q = useQuery({
    queryKey: ['audit-log', f],
    queryFn: () => api.get<AuditEntry[]>('/audit-log?limit=500' + (qs(f) ? '&' + qs(f) : '')),
    refetchInterval: 30_000,
  })
  const rows = q.data ?? []
  const paged = usePaged(rows, { pageSize: 10 })
  const active = useMemo(() => Object.values(f).some(Boolean), [f])
  const set = (k: keyof Filters, v: string) => setF((p) => ({ ...p, [k]: v }))
  const fc = facets.data

  return (
    <div>
      <PageHeader title="Audit Log" icon={FileClock}
        subtitle="Every operator action — filter by category, actor, entity, action, text or time range; export for compliance"
        actions={<a className="btn btn-sm" href={`${API_BASE}/audit-log/export${qs(f) ? '?' + qs(f) : ''}`}><Download size={14} /> Export CSV</a>} />

      <div className="kpi-grid">
        <Kpi label="Categories" value={fc?.category.length ?? '—'} icon={FileClock} tone="info" />
        <Kpi label="Actors" value={fc?.actor.length ?? '—'} />
        <Kpi label="Entity Types" value={fc?.entity_type.length ?? '—'} />
        <Kpi label="Shown" value={rows.length} sub={active ? 'filtered' : 'recent 500'} />
      </div>

      <Panel title="Filters" icon={Search}
        actions={active ? <button className="btn btn-ghost btn-sm" onClick={() => setF(empty)}><X size={13} /> Clear</button> : undefined}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(170px,1fr))', gap: 10 }}>
          <label className="form-field">Category
            <select className="field" value={f.category} onChange={(e) => set('category', e.target.value)}>
              <option value="">any</option>
              {(fc?.category ?? []).map((c) => <option key={c.value} value={c.value}>{c.value} ({c.count})</option>)}
            </select>
          </label>
          <label className="form-field">Actor
            <select className="field" value={f.actor} onChange={(e) => set('actor', e.target.value)}>
              <option value="">any</option>
              {(fc?.actor ?? []).map((c) => <option key={c.value} value={c.value}>{c.value} ({c.count})</option>)}
            </select>
          </label>
          <label className="form-field">Entity type
            <select className="field" value={f.entity_type} onChange={(e) => set('entity_type', e.target.value)}>
              <option value="">any</option>
              {(fc?.entity_type ?? []).map((c) => <option key={c.value} value={c.value}>{c.value} ({c.count})</option>)}
            </select>
          </label>
          <label className="form-field">Action<input className="field mono" value={f.action} onChange={(e) => set('action', e.target.value)} placeholder="device.delete" /></label>
          <label className="form-field">Text search<input className="field" value={f.q} onChange={(e) => set('q', e.target.value)} placeholder="in summary…" /></label>
          <label className="form-field">From<input className="field" type="date" value={f.from} onChange={(e) => set('from', e.target.value)} /></label>
          <label className="form-field">To<input className="field" type="date" value={f.to} onChange={(e) => set('to', e.target.value)} /></label>
        </div>
      </Panel>

      <Panel title="Activity" icon={FileClock} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading audit log…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={FileClock} title="No matching audit entries" message={active ? 'No entries match the current filters.' : 'Actions are recorded here as operators make changes.'} />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>When</th><th>Category</th><th>Action</th><th>Summary</th><th>Actor</th><th>Entity</th></tr></thead>
            <tbody>
              {paged.slice.map((a) => (
                <tr key={a.id}>
                  <td className="muted" title={a.at}>{timeAgo(a.at)}</td>
                  <td><span className={`badge ${catCls(a.category)}`}>{a.category}</span></td>
                  <td className="mono">{a.action}</td>
                  <td>{a.summary || '—'}</td>
                  <td>{a.actor}</td>
                  <td className="muted">{a.entity_type}{a.entity_id ? ` · ${a.entity_id.slice(0, 8)}` : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {rows.length > 0 && <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />}
      </Panel>
    </div>
  )
}
