import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Boxes, Wifi, WifiOff, Server, Radar, Pencil } from 'lucide-react'
import { api, type Device } from '../api'
import { PageHeader, Panel, Kpi, StatusPill, EmptyState, colorFor, usePaged, Pager } from '../components/ui'
import { useQueryParam, useQueryNum, useSetParams } from '../lib/urlState'
import { DeleteAllToggle } from '../components/DeleteAllToggle'
import { EditDevice } from '../components/EditDevice'
import { ExportDevicesButton } from '../components/ExportDevicesButton'

interface Props {
  category: string
  title: string
  detailBase: string
  headerExtra?: React.ReactNode // optional action(s) rendered before the delete control
  preContent?: React.ReactNode // optional content rendered above the KPI grid (e.g. CCTV fleet ops)
  showRole?: boolean // show the virtualization "Type" column (Physical / VM / ESXi / Hyper-V / Unknown)
}

const ROLE_BADGE: Record<string, { label: string; bg: string }> = {
  virtual_host_esxi: { label: 'ESXi Host', bg: '#166534' },
  virtual_host_hyperv: { label: 'Hyper-V Host', bg: '#1e3a8a' },
  virtual_machine: { label: 'Virtual Machine', bg: '#7c3aed' },
  physical_server: { label: 'Physical', bg: '#0e7490' },
  unknown_server: { label: 'Unknown', bg: '#6b7280' },
}
function RoleBadge({ role }: { role?: string }) {
  if (!role) return <span className="muted">—</span>
  const b = ROLE_BADGE[role]
  if (!b) return <span className="muted">{role}</span>
  return <span className="badge" style={{ background: b.bg, color: '#fff' }}>{b.label}</span>
}

const isOffline = (s: string) => ['down', 'offline', 'needs_attention'].includes((s || '').toLowerCase())

// The Device column should show the resolved NAME, not the IP. At discovery a device's `name`
// defaults to its IP when no hostname resolved; the real collected hostname lands in `hostname`.
// Prefer a real name (name or hostname that differs from the IP), and fall back to the IP only when
// nothing resolved — the IP column shows the address regardless.
function deviceDisplayName(d: Device): string {
  const ip = d.primary_ip ?? ''
  // A "real" name is one that isn't the bare IP and isn't a generic default (localhost*).
  const real = (v?: string | null) => !!v && v !== ip && !/^localhost(\.|$)/i.test(v)
  if (real(d.name)) return d.name
  if (real(d.hostname)) return d.hostname!
  return ip || d.name || '—'
}

export function DeviceList({ category, title, detailBase, headerExtra, preContent, showRole }: Props) {
  const hostDetail = (d: Device) => (d.server_role?.startsWith('virtual_host') ? `/virtual-hosts/${d.id}` : `${detailBase}/${d.id}`)
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

  // Search + page live in the URL so browser Back from a device restores them.
  // q + page are updated in ONE setParams call (two setSearchParams calls would drop q).
  const [q] = useQueryParam('q', '')
  const [pageNum, setPageNum] = useQueryNum('page', 1)
  const setParams = useSetParams()
  const setQ = (v: string) => setParams({ q: v, page: null })
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    if (!t) return all
    return all.filter((d) => d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
      (d.vendor ?? '').toLowerCase().includes(t) || (d.model ?? '').toLowerCase().includes(t) || (d.hostname ?? '').toLowerCase().includes(t))
  }, [data, q])
  const paged = usePaged(filtered, { pageSize: 10, page: pageNum - 1, onPage: (p) => setPageNum(p + 1) })

  return (
    <div>
      <PageHeader title={title} subtitle={`Managed ${title.toLowerCase()} across the fleet`} icon={Boxes}
        actions={
          <>
            <ExportDevicesButton devices={filtered} filename={title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')} />
            {headerExtra}
            <DeleteAllToggle ids={filtered.map((d) => d.id)} fullInventory={false}
              scope={q.trim() ? `filtered ${title.toLowerCase()}` : `all ${title.toLowerCase()}`}
              onDelete={(ids) => del.mutate(ids)} busy={del.isPending} />
          </>
        }
      />
      {msg && <div className="banner" style={{ marginBottom: 12, fontSize: 13 }}>{msg}</div>}
      {preContent}

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
            <input placeholder="Filter by name / IP / vendor / model…" value={q} onChange={(e) => setQ(e.target.value)}
              style={{ padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13, width: 320, maxWidth: '100%' }} />
          </div>
          <table className="data-table">
            <thead>
              <tr><th>Device</th><th>IP</th>{showRole && <th>Type</th>}<th>Vendor</th><th>Model</th><th>OS</th>{showRole ? <th>Hosted on</th> : <th>Driver</th>}<th>Status</th><th></th></tr>
            </thead>
            <tbody>
              {paged.slice.map((d) => (
                <tr key={d.id}>
                  <td>
                    <div className="dev-cell">
                      <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(deviceDisplayName(d) || d.category).charAt(0).toUpperCase()}</span>
                      <div className="dev-meta">
                        <Link className="cell-name" to={hostDetail(d)}>{deviceDisplayName(d)}</Link>
                        {d.hostname && d.hostname !== deviceDisplayName(d) && <small>{d.hostname}</small>}
                      </div>
                    </div>
                  </td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  {showRole && <td><RoleBadge role={d.server_role} /></td>}
                  <td>{d.vendor ?? '—'}</td>
                  <td>{d.model ?? '—'}</td>
                  <td>{d.os_version ?? '—'}</td>
                  {showRole
                    ? <td>{d.hosted_on
                        ? <Link to={`/virtual-hosts/${d.hosted_on.id}`} className="cell-name" title="Parent hypervisor">{d.hosted_on.ip || d.hosted_on.name}</Link>
                        : <span className="muted">—</span>}</td>
                    : <td>{d.driver ?? '—'}</td>}
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
