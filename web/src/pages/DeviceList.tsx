import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Boxes, Wifi, WifiOff, Server, Radar, Pencil } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, Kpi, StatusPill, EmptyState, colorFor, usePaged, Pager } from '../components/ui'
import { DeleteAllToggle } from '../components/DeleteAllToggle'
import { EditDevice } from '../components/EditDevice'

interface Props {
  category: string
  title: string
  detailBase: string
}

const isOffline = (s: string) => ['down', 'offline', 'needs_attention'].includes((s || '').toLowerCase())

export function DeviceList({ category, title, detailBase }: Props) {
  const qc = useQueryClient()
  const [msg, setMsg] = useState('')
  const [editDev, setEditDev] = useState<Device | null>(null)
  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', category],
    queryFn: () => api.get<Device[]>(`/devices?category=${category}`),
  })
  const del = useMutation({
    mutationFn: (ids: string[]) => api.post<{ deleted: number }>('/devices/bulk-delete', { ids }),
    onSuccess: (r) => { setMsg(`Deleted ${(r as { deleted: number }).deleted} ${title.toLowerCase()}.`); qc.invalidateQueries({ queryKey: ['devices'] }) },
    onError: (e) => setMsg((e as Error).message),
  })

  const all = data ?? []
  const online = all.filter((d) => (d.status || '').toLowerCase() === 'up').length
  const offline = all.filter((d) => isOffline(d.status)).length
  const vendors = useMemo(() => new Set(all.map((d) => d.vendor || 'Unknown')).size, [data])

  const [q, setQ] = useState('')
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    if (!t) return all
    return all.filter((d) => d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
      (d.vendor ?? '').toLowerCase().includes(t) || (d.model ?? '').toLowerCase().includes(t) || (d.hostname ?? '').toLowerCase().includes(t))
  }, [data, q])
  const paged = usePaged(filtered, { pageSize: 10 })

  return (
    <div>
      <PageHeader title={title} subtitle={`Managed ${title.toLowerCase()} across the fleet`} icon={Boxes}
        actions={
          <DeleteAllToggle ids={filtered.map((d) => d.id)} fullInventory={false}
            scope={q.trim() ? `filtered ${title.toLowerCase()}` : `all ${title.toLowerCase()}`}
            onDelete={(ids) => del.mutate(ids)} busy={del.isPending} />
        }
      />
      {msg && <div className="banner" style={{ marginBottom: 12, fontSize: 13 }}>{msg}</div>}

      <div className="kpi-grid">
        <Kpi label={title} value={all.length} icon={Boxes} tone="info" />
        <Kpi label="Online" value={online} icon={Wifi} tone="ok" sub={all.length ? `${Math.round((online / Math.max(1, all.length)) * 100)}%` : '—'} />
        <Kpi label="Offline / Attention" value={offline} icon={WifiOff} tone={offline > 0 ? 'crit' : 'default'} />
        <Kpi label="Vendors" value={vendors} icon={Server} tone="default" sub="distinct" />
      </div>

      <Panel title={title} subtitle={`${all.length} device(s)`} pad={false}>
        {isLoading && <div className="loading">Loading {title.toLowerCase()}…</div>}
        {error && <div style={{ padding: 'var(--space-5)' }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && data.length === 0 && (
          <EmptyState
            icon={Radar}
            title={`No ${title.toLowerCase()} yet`}
            message="Run a discovery scan to populate this category, or add a device manually."
            action={<Link className="btn btn-primary btn-sm" to="/discovery">Start Discovery</Link>}
          />
        )}
        {data && data.length > 0 && (
          <>
          <div style={{ padding: '8px 10px' }}>
            <input placeholder="Filter by name / IP / vendor / model…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }}
              style={{ padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13, width: 320, maxWidth: '100%' }} />
          </div>
          <table className="data-table">
            <thead>
              <tr><th>Device</th><th>IP</th><th>Vendor</th><th>Model</th><th>OS</th><th>Driver</th><th>Status</th><th></th></tr>
            </thead>
            <tbody>
              {paged.slice.map((d) => (
                <tr key={d.id}>
                  <td>
                    <div className="dev-cell">
                      <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || d.category).charAt(0).toUpperCase()}</span>
                      <div className="dev-meta">
                        <Link className="cell-name" to={`${detailBase}/${d.id}`}>{d.name}</Link>
                        {d.hostname && <small>{d.hostname}</small>}
                      </div>
                    </div>
                  </td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  <td>{d.vendor ?? '—'}</td>
                  <td>{d.model ?? '—'}</td>
                  <td>{d.os_version ?? '—'}</td>
                  <td>{d.driver ?? '—'}</td>
                  <td><StatusPill status={d.status} /></td>
                  <td><button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit device"><Pencil size={12} /></button></td>
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
