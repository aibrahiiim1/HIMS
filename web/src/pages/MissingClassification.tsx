import { useMemo, useState } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { HelpCircle, Pencil, RefreshCw, Lock, Boxes } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager, colorFor } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { EditDevice } from '../components/EditDevice'
import { needsClassification } from '../lib/classify'

// Missing Classification = HIMS does not yet KNOW WHAT the device is (category,
// vendor, model, weak/low-confidence evidence). This is a classification-cleanup
// queue — explicitly NOT the same as Unmanaged (can't access). A device can be
// Missing Classification but fully Managed, and vice-versa. The needsClassification
// predicate is shared with the sidebar badge (lib/classify) so they always agree.

export function MissingClassification() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [q, setQ] = useState('')

  const reclassify = useMutation({
    mutationFn: (id: string) => api.post(`/devices/${id}/reclassify`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['devices'] }),
  })

  const rows = useMemo(() => {
    const all = (data ?? []).map((d) => ({ d, m: needsClassification(d) })).filter((x) => x.m)
    const t = q.trim().toLowerCase()
    const f = t ? all.filter((x) => x.d.name.toLowerCase().includes(t) || (x.d.primary_ip ?? '').includes(t)) : all
    return f
  }, [data, q])
  const paged = usePaged(rows, { pageSize: 15 })

  return (
    <div>
      <PageHeader title="Missing Classification" subtitle="Devices HIMS cannot fully identify yet — category / vendor / model / weak evidence. This is classification cleanup, not a management problem." icon={HelpCircle} />
      <div className="kpi-grid">
        <Kpi label="Need classification" value={rows.length} icon={HelpCircle} tone={rows.length > 0 ? 'warn' : 'ok'} sub="category / vendor / confidence" />
        <Kpi label="Total devices" value={(data ?? []).length} icon={Boxes} tone="info" />
      </div>
      <Panel title="Classification queue" subtitle="A device here can still be Managed — these are identity gaps, not access gaps." pad={false}>
        <div style={{ padding: '8px 10px' }}>
          <input placeholder="Filter by name / IP…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }} style={{ padding: '6px 10px', fontSize: 13, width: 300, maxWidth: '100%' }} />
        </div>
        {isLoading && <div className="loading">Loading…</div>}
        {data && rows.length === 0 && <EmptyState icon={HelpCircle} title="Everything is classified" message="No devices currently need classification cleanup." />}
        {rows.length > 0 && (
          <>
          <table className="data-table">
            <thead><tr>
              <th>Device</th><th>IP</th><th>Reachability</th><th>Management</th><th>Category</th><th>Vendor / Model</th><th>Confidence</th><th>Why</th><th></th>
            </tr></thead>
            <tbody>
              {paged.slice.map(({ d, m }) => (
                <tr key={d.id}>
                  <td><div className="dev-cell"><span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || '?').charAt(0).toUpperCase()}</span>
                    <div className="dev-meta"><Link className="cell-name" to={`/devices/${d.id}`}>{d.name}</Link>{d.hostname && <small>{d.hostname}</small>}</div></div></td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  <td><ReachabilityBadge value={d.reachability} /></td>
                  <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                  <td style={{ textTransform: 'capitalize' }}>{(d.category || 'unknown').replace(/_/g, ' ')}{d.classification_locked && <Lock size={11} style={{ marginLeft: 4, verticalAlign: -1 }} />}</td>
                  <td>{d.vendor || <span className="muted">no vendor</span>}{d.model ? ` / ${d.model}` : ''}</td>
                  <td>{typeof d.confidence_score === 'number' ? `${d.confidence_score}%` : '—'}</td>
                  <td className="muted" style={{ fontSize: 11 }}>{m!.why.join(', ')}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit / classify"><Pencil size={12} /></button>{' '}
                    <button className="btn btn-ghost btn-xs" disabled={!d.primary_ip || reclassify.isPending} onClick={() => reclassify.mutate(d.id)} title="Re-derive classification from probe evidence"><RefreshCw size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
          </>
        )}
      </Panel>
      {editDev && <EditDevice device={editDev} onClose={() => setEditDev(null)} />}
    </div>
  )
}
