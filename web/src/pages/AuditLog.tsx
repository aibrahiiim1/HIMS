import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FileClock } from 'lucide-react'
import { api, type AuditEntry } from '../api'
import { PageHeader, Panel, EmptyState, timeAgo } from '../components/ui'

const CATS = ['all', 'user', 'discovery', 'inventory', 'credential', 'config', 'security', 'general']
const catCls = (c: string) => ({ user: 'badge-access', discovery: 'badge-lldp', inventory: 'badge-up', credential: 'badge-warning', config: 'badge-trunk', security: 'badge-down' }[c] ?? 'badge-unknown')

export function AuditLog() {
  const [cat, setCat] = useState('all')
  const q = useQuery({
    queryKey: ['audit-log', cat],
    queryFn: () => api.get<AuditEntry[]>('/audit-log?limit=500' + (cat !== 'all' ? `&category=${cat}` : '')),
    refetchInterval: 30_000,
  })
  const rows = q.data ?? []
  return (
    <div>
      <PageHeader title="Audit Log" icon={FileClock} subtitle="User, discovery, inventory, credential and configuration actions" />
      <Panel title="Activity" icon={FileClock} subtitle={`${rows.length}`} pad={false}>
        <div className="row" style={{ padding: 'var(--space-4) var(--space-5)', borderBottom: '1px solid var(--border)' }}>
          <div className="seg">
            {CATS.map((c) => (
              <button key={c} className={'seg-chip' + (cat === c ? ' active' : '')} onClick={() => setCat(c)}>{c}</button>
            ))}
          </div>
        </div>
        {q.isLoading && <div className="loading">Loading audit log…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={FileClock} title="No audit entries" message="Actions are recorded here as operators make changes (device/credential/discovery/config/user changes)." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>When</th><th>Category</th><th>Action</th><th>Summary</th><th>Actor</th><th>Entity</th></tr></thead>
            <tbody>
              {rows.map((a) => (
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
      </Panel>
    </div>
  )
}
