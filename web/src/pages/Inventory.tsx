import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { Boxes, RefreshCw, Trash2, Search, Wifi, WifiOff, Server, TriangleAlert, Package } from 'lucide-react'
import { api, type Device, type Lookup, type Location, locationPaths } from '../api'
import { PageHeader, Panel, Kpi, BarList, EmptyState, colorFor, usePaged, Pager } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'

const DETAIL_BASE: Record<string, string> = {
  switch: '/devices', server: '/servers', virtual_host: '/virtual-hosts', firewall: '/firewalls',
  camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx',
  endpoint: '/workstations',
}
const CATEGORIES = [
  'unknown', 'switch', 'router', 'firewall', 'access_point', 'wireless_controller', 'server',
  'virtual_host', 'virtual_machine', 'storage', 'nvr', 'camera', 'printer', 'ip_phone', 'pbx',
  'voice_gateway', 'database', 'directory', 'dns', 'dhcp', 'fingerprint', 'endpoint', 'ups',
  'isp_router', 'application',
]
const isOffline = (s: string) => ['down', 'offline', 'needs_attention'].includes((s || '').toLowerCase())

// Display labels for the Management Access Coverage drill-down filters.
const ACCESS_PROTOCOL_LABEL: Record<string, string> = {
  snmp_v2c: 'SNMP v2c', snmp_v3: 'SNMP v3', ssh: 'SSH', winrm: 'WinRM', wmi: 'WMI / CIM',
  smb: 'SMB', http_basic: 'HTTP Basic', api_token: 'API Token', onvif: 'ONVIF', rtsp: 'RTSP',
  vendor_api: 'Vendor API', vmware: 'VMware', fortigate_api: 'FortiGate API', cucm_axl: 'CUCM AXL', ldap: 'LDAP',
}
const ACCESS_ISSUE_LABEL: Record<string, string> = {
  no_credential_bound: 'No credential bound', credential_failed: 'Credential failed', not_tested: 'Not tested',
  stale: 'Stale test (re-verify)', missing_expected_protocol: 'Missing expected protocol',
}

export function Inventory() {
  const qc = useQueryClient()
  const [sp, setSp] = useSearchParams()
  // Management-access drill-down filters (from the Dashboard card). Applied
  // server-side so the filtered URL is shareable/bookmarkable.
  const access = sp.get('access') ?? ''
  const accessProtocol = sp.get('accessProtocol') ?? ''
  const accessIssue = sp.get('accessIssue') ?? ''
  // Reachability / Management drill-down filters (Dashboard + Coverage cards).
  // Server-side + bookmarkable: /inventory?reachability=online&management=unmanaged
  const reachF = sp.get('reachability') ?? ''
  const mgmtF = sp.get('management') ?? ''
  const accessQS = [
    access && `access=${encodeURIComponent(access)}`,
    accessProtocol && `accessProtocol=${encodeURIComponent(accessProtocol)}`,
    accessIssue && `accessIssue=${encodeURIComponent(accessIssue)}`,
    reachF && `reachability=${encodeURIComponent(reachF)}`,
    mgmtF && `management=${encodeURIComponent(mgmtF)}`,
  ].filter(Boolean).join('&')
  const accessActive = !!accessQS
  const accessChip = access === 'managed' ? 'Managed (any working access)'
    : access === 'unmanaged' ? 'Unmanaged (no usable credential)'
    : accessProtocol ? `Access: ${ACCESS_PROTOCOL_LABEL[accessProtocol] ?? accessProtocol}`
    : accessIssue ? `Issue: ${ACCESS_ISSUE_LABEL[accessIssue] ?? accessIssue}`
    : (reachF || mgmtF)
      ? [reachF && `Reachability: ${reachF}`, mgmtF && `Management: ${mgmtF === 'not_managed' ? 'not managed (any state)' : mgmtF.replace(/_/g, ' ')}`].filter(Boolean).join(' · ')
    : ''
  const clearAccess = () => {
    const next = new URLSearchParams(sp)
    next.delete('access'); next.delete('accessProtocol'); next.delete('accessIssue')
    next.delete('reachability'); next.delete('management')
    setSp(next, { replace: true })
  }
  const [cat, setCat] = useState('all')
  const [classF, setClassF] = useState('all')
  const [locF, setLocF] = useState('all')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [editing, setEditing] = useState<Device | null>(null)
  const [msg, setMsg] = useState('')
  const [asg, setAsg] = useState({ vlan: '', class: '', location_id: '' })

  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', 'all', accessQS],
    queryFn: () => api.get<Device[]>(`/devices?category=all${accessQS ? '&' + accessQS : ''}`),
  })
  const classOpts = useQuery({ queryKey: ['lookups', 'class'], queryFn: () => api.get<Lookup[]>('/lookups?kind=class') })
  const vlanOpts = useQuery({ queryKey: ['lookups', 'vlan'], queryFn: () => api.get<Lookup[]>('/lookups?kind=vlan') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])
  const locName = (id?: string | null) => (id ? locPath[id] ?? '—' : '—')

  const counts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const d of data ?? []) m[d.category] = (m[d.category] ?? 0) + 1
    return m
  }, [data])
  const cats = useMemo(() => Object.keys(counts).sort((a, b) => counts[b] - counts[a]), [counts])
  const classes = useMemo(() => [...new Set((data ?? []).map((d) => d.device_class).filter(Boolean) as string[])].sort(), [data])
  const usedLocIDs = useMemo(() => [...new Set((data ?? []).map((d) => d.location_id).filter(Boolean) as string[])]
    .sort((a, b) => (locPath[a] ?? '').localeCompare(locPath[b] ?? '')), [data, locPath])

  // ---- summary metrics ----
  const all = data ?? []
  const online = all.filter((d) => (d.status || '').toLowerCase() === 'up').length
  const offline = all.filter((d) => isOffline(d.status)).length
  const vendorRows = useMemo(() => Object.entries(all.reduce<Record<string, number>>((m, d) => { const v = d.vendor || 'Unknown'; m[v] = (m[v] ?? 0) + 1; return m }, {}))
    .sort((a, b) => b[1] - a[1]).slice(0, 6).map(([label, value]) => ({ label, value, color: colorFor(label) })), [data])
  const statusRows = useMemo(() => {
    const order = ['up', 'warning', 'needs_attention', 'down', 'unknown']
    const m = all.reduce<Record<string, number>>((acc, d) => { const s = (d.status || 'unknown').toLowerCase(); acc[s] = (acc[s] ?? 0) + 1; return acc }, {})
    const palette: Record<string, string> = { up: '#16a34a', warning: '#d97706', needs_attention: '#d97706', down: '#dc2626', unknown: '#94a3b8' }
    return Object.entries(m).sort((a, b) => (order.indexOf(a[0]) - order.indexOf(b[0])))
      .map(([label, value]) => ({ label: label.replace(/_/g, ' '), value, color: palette[label] ?? '#94a3b8' }))
  }, [data])

  const rows = useMemo(() => {
    let r = all
    if (cat !== 'all') r = r.filter((d) => d.category === cat)
    if (classF !== 'all') r = r.filter((d) => (d.device_class ?? '') === classF)
    if (locF !== 'all') r = r.filter((d) => (d.location_id ?? '') === locF)
    if (q.trim()) {
      const t = q.toLowerCase()
      r = r.filter((d) =>
        d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t) ||
        (d.vendor ?? '').toLowerCase().includes(t) || (d.vlan ?? '').toLowerCase().includes(t) ||
        (d.device_class ?? '').toLowerCase().includes(t) || locName(d.location_id).toLowerCase().includes(t))
    }
    return r
  }, [data, cat, classF, locF, q, locPath])

  // Paginate the (already-filtered) rows so a 600+ device table stays snappy.
  const paged = usePaged(rows, { pageSize: 10 })
  const pageRows = paged.slice

  const refresh = () => qc.invalidateQueries({ queryKey: ['devices'] })
  const toggle = (id: string) => setSel((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const allShownSelected = pageRows.length > 0 && pageRows.every((d) => sel.has(d.id))
  const toggleAll = () => setSel((s) => {
    const n = new Set(s)
    if (allShownSelected) pageRows.forEach((d) => n.delete(d.id))
    else pageRows.forEach((d) => n.add(d.id))
    return n
  })
  const selRows = useMemo(() => all.filter((d) => sel.has(d.id)), [data, sel])

  const del = useMutation({
    mutationFn: (ids: string[]) => api.post<{ deleted: number }>('/devices/bulk-delete', { ids }),
    onSuccess: (r) => { setMsg(`Deleted ${(r as { deleted: number }).deleted} device(s).`); setSel(new Set()); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const rescan = useMutation({
    mutationFn: (targets: string) => api.post('/discovery/scan', { mode: 'targets', targets }),
    onSuccess: () => setMsg('Re-scan launched for selected targets — watch Discovery → Scan jobs.'),
    onError: (e) => setMsg((e as Error).message),
  })
  const save = useMutation({
    mutationFn: (d: Device) => api.patch(`/devices/${d.id}`, {
      name: d.name, category: d.category, vendor: d.vendor ?? '', model: d.model ?? '',
      serial: d.serial ?? '', os_version: d.os_version ?? '', hostname: d.hostname ?? '',
      vlan: d.vlan ?? '', class: d.device_class ?? '', location_id: d.location_id ?? null,
    }),
    onSuccess: () => { setEditing(null); setMsg('Saved.'); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const assign = useMutation({
    mutationFn: (body: { ids: string[]; vlan?: string; class?: string; location_id?: string }) => api.post<{ updated: number }>('/devices/bulk-assign', body),
    onSuccess: (r) => { setMsg(`Updated ${(r as { updated: number }).updated} device(s).`); setAsg({ vlan: '', class: '', location_id: '' }); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const assignField = (field: 'vlan' | 'class' | 'location_id', value: string) => {
    if (sel.size === 0) return
    if (!value) { setMsg(`Choose a ${field === 'location_id' ? 'location' : field} value to assign.`); return }
    assign.mutate({ ids: [...sel], [field]: value })
  }
  const doDeleteSelected = () => {
    if (sel.size === 0) return
    if (confirm(`Delete ${sel.size} device(s)? This also removes their collected inventory and cannot be undone.`)) del.mutate([...sel])
  }
  const doRescanSelected = () => {
    const ips = selRows.map((d) => d.primary_ip).filter(Boolean) as string[]
    if (ips.length === 0) { setMsg('None of the selected devices have an IP to re-scan.'); return }
    rescan.mutate(ips.join(','))
  }

  // category filter chips: All + present categories (capped) with counts
  const chipCats = cats.slice(0, 10)

  return (
    <div>
      <PageHeader
        title="Inventory"
        subtitle="Every managed device across all categories and sites"
        icon={Boxes}
        actions={
          <>
            <button className="btn btn-sm" disabled={sel.size === 0 || rescan.isPending} onClick={doRescanSelected}>
              <RefreshCw size={14} /> Re-scan{sel.size > 0 ? ` (${sel.size})` : ''}
            </button>
            <button className="btn btn-danger btn-sm" disabled={sel.size === 0 || del.isPending} onClick={doDeleteSelected}>
              <Trash2 size={14} /> Delete{sel.size > 0 ? ` (${sel.size})` : ''}
            </button>
          </>
        }
      />

      {/* Summary KPIs */}
      <div className="kpi-grid">
        <Kpi label="Total Devices" value={all.length} icon={Boxes} tone="info" sub={`${cats.length} categories`} />
        <Kpi label="Online" value={online} icon={Wifi} tone="ok" sub={all.length ? `${Math.round((online / all.length) * 100)}%` : '—'} />
        <Kpi label="Offline / Attention" value={offline} icon={WifiOff} tone={offline > 0 ? 'crit' : 'default'} sub={offline > 0 ? 'needs review' : 'all clear'} />
        <Kpi label="Vendors" value={new Set(all.map((d) => d.vendor || 'Unknown')).size} icon={Server} tone="default" sub="distinct" />
      </div>

      <div className="grid-2">
        <Panel title="By Status"><BarList rows={statusRows} /></Panel>
        <Panel title="Top Vendors"><BarList rows={vendorRows} /></Panel>
      </div>

      <Panel title="Devices" icon={Package} subtitle={`${rows.length} shown`} pad={false}>
        {/* Toolbar */}
        <div style={{ padding: 'var(--space-4) var(--space-5)', borderBottom: '1px solid var(--border)' }}>
          {accessActive && (
            <div className="row" style={{ alignItems: 'center', gap: 8, marginBottom: 12 }}>
              <span className="badge badge-info" style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                {accessChip}
              </span>
              <button className="btn btn-ghost btn-xs" onClick={clearAccess}>✕ Clear access filter</button>
              <span className="muted" style={{ fontSize: 12 }}>from Management Access Coverage</span>
            </div>
          )}
          <div className="seg" style={{ marginBottom: 12 }}>
            <button className={'seg-chip' + (cat === 'all' ? ' active' : '')} onClick={() => setCat('all')}>All <span className="seg-count">{all.length}</span></button>
            {chipCats.map((c) => (
              <button key={c} className={'seg-chip' + (cat === c ? ' active' : '')} onClick={() => setCat(c)}>
                {c.replace(/_/g, ' ')} <span className="seg-count">{counts[c]}</span>
              </button>
            ))}
          </div>
          <div className="row">
            <span className="topbar-search" style={{ width: 240 }}>
              <Search size={15} />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search name / IP / vendor / VLAN…" />
            </span>
            <select className="field" value={classF} onChange={(e) => setClassF(e.target.value)}>
              <option value="all">All classes</option>
              {classes.map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
            <select className="field" value={locF} onChange={(e) => setLocF(e.target.value)}>
              <option value="all">All locations</option>
              {usedLocIDs.map((id) => <option key={id} value={id}>{locPath[id]}</option>)}
            </select>
            {msg && <span className="muted" style={{ marginLeft: 'auto', fontSize: 12 }}>{msg}</span>}
          </div>

          {/* Bulk-assign — each classification has its own independent action */}
          {sel.size > 0 && (
            <div className="row" style={{ marginTop: 12, paddingTop: 12, borderTop: '1px dashed var(--border)' }}>
              <span className="muted" style={{ fontSize: 12 }}>Assign to {sel.size} selected →</span>
              <select className="field" value={asg.vlan} onChange={(e) => setAsg({ ...asg, vlan: e.target.value })}>
                <option value="">VLAN…</option>
                {(vlanOpts.data ?? []).map((o) => <option key={o.id} value={o.value}>{o.value}</option>)}
              </select>
              <button className="btn btn-sm" disabled={assign.isPending} onClick={() => assignField('vlan', asg.vlan)}>Set VLAN</button>
              <select className="field" value={asg.class} onChange={(e) => setAsg({ ...asg, class: e.target.value })}>
                <option value="">Class…</option>
                {(classOpts.data ?? []).map((o) => <option key={o.id} value={o.value}>{o.value}</option>)}
              </select>
              <button className="btn btn-sm" disabled={assign.isPending} onClick={() => assignField('class', asg.class)}>Set Class</button>
              <select className="field" value={asg.location_id} onChange={(e) => setAsg({ ...asg, location_id: e.target.value })}>
                <option value="">Location…</option>
                {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id]}</option>)}
              </select>
              <button className="btn btn-sm" disabled={assign.isPending} onClick={() => assignField('location_id', asg.location_id)}>Set Location</button>
            </div>
          )}
        </div>

        {isLoading && <div className="loading">Loading inventory…</div>}
        {error && <div style={{ padding: 'var(--space-5)' }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && rows.length === 0 && (
          <EmptyState icon={TriangleAlert} title="No devices match" message="Try clearing the category, class, location, or search filters." />
        )}
        {rows.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 28 }}><input type="checkbox" checked={allShownSelected} onChange={toggleAll} /></th>
                <th>Device</th><th>IP</th><th>Category</th><th>VLAN</th><th>Class</th><th>Location</th><th>Vendor</th><th>Reachability</th><th>Management</th><th></th>
              </tr>
            </thead>
            <tbody>
              {pageRows.map((d) => {
                // Categories without a dedicated list base fall back to /devices,
                // which dispatches to the right template by category (unknown /
                // unclassified → a neutral generic page, never the switch page).
                const base = DETAIL_BASE[d.category] ?? '/devices'
                const isEd = editing?.id === d.id
                if (isEd && editing) {
                  return (
                    <tr key={d.id} style={{ background: 'var(--surface-2)' }}>
                      <td></td>
                      <td><input className="field" style={{ width: 130 }} value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} /></td>
                      <td className="mono">{d.primary_ip ?? '—'}</td>
                      <td>
                        <select className="field" value={editing.category} onChange={(e) => setEditing({ ...editing, category: e.target.value })}>
                          {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
                        </select>
                      </td>
                      <td>
                        <select className="field" style={{ width: 90 }} value={editing.vlan ?? ''} onChange={(e) => setEditing({ ...editing, vlan: e.target.value })}>
                          <option value="">—</option>
                          {(vlanOpts.data ?? []).map((o) => <option key={o.id} value={o.value}>{o.value}</option>)}
                          {editing.vlan && !(vlanOpts.data ?? []).some((o) => o.value === editing.vlan) && <option value={editing.vlan}>{editing.vlan}</option>}
                        </select>
                      </td>
                      <td>
                        <select className="field" style={{ width: 110 }} value={editing.device_class ?? ''} onChange={(e) => setEditing({ ...editing, device_class: e.target.value })}>
                          <option value="">—</option>
                          {(classOpts.data ?? []).map((o) => <option key={o.id} value={o.value}>{o.value}</option>)}
                          {editing.device_class && !(classOpts.data ?? []).some((o) => o.value === editing.device_class) && <option value={editing.device_class}>{editing.device_class}</option>}
                        </select>
                      </td>
                      <td>
                        <select className="field" style={{ width: 150 }} value={editing.location_id ?? ''} onChange={(e) => setEditing({ ...editing, location_id: e.target.value || null })}>
                          <option value="">—</option>
                          {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id]}</option>)}
                        </select>
                      </td>
                      <td><input className="field" style={{ width: 90 }} value={editing.vendor ?? ''} onChange={(e) => setEditing({ ...editing, vendor: e.target.value })} /></td>
                      <td><ReachabilityBadge value={d.reachability} /></td>
                      <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                      <td className="cell-actions">
                        <button className="btn btn-primary btn-xs" disabled={save.isPending} onClick={() => save.mutate(editing)}>Save</button>
                        <button className="btn btn-ghost btn-xs" onClick={() => setEditing(null)}>Cancel</button>
                      </td>
                    </tr>
                  )
                }
                return (
                  <tr key={d.id}>
                    <td><input type="checkbox" checked={sel.has(d.id)} onChange={() => toggle(d.id)} /></td>
                    <td>
                      <div className="dev-cell">
                        <span className="dev-avatar" style={{ background: colorFor(d.category) }}>{d.category.charAt(0).toUpperCase()}</span>
                        <div className="dev-meta">
                          {base ? <Link className="cell-name" to={`${base}/${d.id}`}>{d.name}</Link> : <span className="cell-name">{d.name}</span>}
                          {d.model && <small>{d.model}</small>}
                        </div>
                      </div>
                    </td>
                    <td className="mono">{d.primary_ip ?? '—'}</td>
                    <td>{d.category.replace(/_/g, ' ')}</td>
                    <td>{d.vlan ?? '—'}</td>
                    <td>{d.device_class ?? '—'}</td>
                    <td style={{ fontSize: 12 }}>{locName(d.location_id)}</td>
                    <td>{d.vendor ?? '—'}</td>
                    <td><ReachabilityBadge value={d.reachability} /></td>
                    <td><ManagementBadge value={d.management} managedBy={d.managed_by} /></td>
                    <td className="cell-actions">
                      <button className="btn btn-ghost btn-xs" onClick={() => setEditing(d)}>Edit</button>
                      <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => { if (confirm(`Delete ${d.name}?`)) del.mutate([d.id]) }}>Delete</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
        {rows.length > 0 && <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />}
      </Panel>
    </div>
  )
}
