import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Wifi, Router, Users, Radio, ShieldCheck, Activity, Plug, FlaskConical, DownloadCloud, Terminal, Layers, FileSearch, Pencil, Settings, LayoutDashboard } from 'lucide-react'
import { api, type WirelessDetailResp, type MibWalkRow, type MibExplorerResp, type SSHCliSummary, type SSHCliRow, type AccessPoint, type WirelessClient, type WirelessSSID } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { RescanSplit } from '../components/RescanSplit'
import { Panel, Kpi, DefList, EmptyState, StatusPill, TabBar } from '../components/ui'
import { DataTable, type DataCol, type DataFilter } from '../components/DataTable'

// Wireless Controller — operator dashboard, organised into tabs:
//   Overview  → identity card + summary KPIs + honesty warnings
//   APs / Clients / SSIDs → searchable/filterable rosters with a summary strip
//   Manage    → every collection/diagnostic action + per-source status + raw MIB
// Honest about which source produced which data (REST/XML primary, SNMP/SSH
// secondary); never presents partial collection as complete and never invents
// AP status a source did not expose.
type Tab = 'overview' | 'aps' | 'clients' | 'ssids' | 'manage'

export function WirelessDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['wireless', id], queryFn: () => api.get<WirelessDetailResp>(`/devices/${id}/wireless`) })
  const d = q.data
  const c = d?.counts ?? {}
  const refetch = () => qc.invalidateQueries({ queryKey: ['wireless', id] })
  const [msg, setMsg] = useState('')
  const [tab, setTab] = useState<Tab>('overview')

  const pid = d?.collection.profile_id
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; detail: string }>(`/vendor-profiles/${pid}/test`, {}),
    onSuccess: (r) => { setMsg((r.ok ? '✓ ' : '✗ ') + r.detail); refetch() },
    onError: (e) => setMsg('✗ ' + (e as Error).message),
  })
  const runApi = useMutation({
    mutationFn: () => api.post<{ collected: boolean; detail: string }>(`/vendor-profiles/${pid}/run-collection`, { device_id: id }),
    onSuccess: (r) => { setMsg((r.collected ? '✓ ' : '⚠ ') + r.detail); refetch() },
    onError: (e) => setMsg('✗ ' + (e as Error).message),
  })
  const runMib = useMutation({
    mutationFn: () => api.post<{ collected: boolean; detail: string }>(`/devices/${id}/collect-wireless-mib`, {}),
    onSuccess: (r) => { setMsg((r.collected ? '✓ ' : '⚠ ') + 'SNMP MIB: ' + r.detail); refetch() },
    onError: (e) => setMsg('✗ ' + (e as Error).message),
  })
  const runSsh = useMutation({
    mutationFn: () => api.post<SSHCliSummary>(`/devices/${id}/collect-ssh-cli`, {}),
    onSuccess: (r) => { setMsg((r.ok ? '✓ ' : '⚠ ') + 'SSH CLI: ' + r.detail); refetch() },
    onError: (e) => setMsg('✗ ' + (e as Error).message),
  })
  const testSsh = useMutation({
    mutationFn: () => api.post<SSHCliSummary>(`/devices/${id}/test-ssh-cli`, {}),
    onSuccess: (r) => { setMsg((r.ok ? '✓ ' : '⚠ ') + 'SSH test: ' + r.detail); refetch() },
    onError: (e) => setMsg('✗ ' + (e as Error).message),
  })
  // Advanced sections (lazy-loaded; only fetch when the Manage tab opens them).
  const [showDiag, setShowDiag] = useState(false)
  const [showRaw, setShowRaw] = useState(false)
  const sshResults = useQuery({ queryKey: ['ssh-cli-results', id], queryFn: () => api.get<SSHCliRow[]>(`/devices/${id}/ssh-cli-results`), enabled: tab === 'manage' && showDiag })
  const explorer = useQuery({ queryKey: ['mib-explorer', id], queryFn: () => api.get<MibExplorerResp>(`/devices/${id}/mib-explorer`), enabled: tab === 'manage' && showRaw })
  const rawRows = useQuery({ queryKey: ['mib-rows', id], queryFn: () => api.get<MibWalkRow[]>(`/devices/${id}/mib-rows`), enabled: tab === 'manage' && showRaw })

  const configureHref = d
    ? `/vendor-profiles?create=1&vendor_type=extreme_xcc&device_id=${id}&target_url=${encodeURIComponent(`https://${d.identity.ip}:8443`)}`
    : '/vendor-profiles'

  const aps = d?.aps ?? []
  const clients = d?.clients ?? []
  const ssids = d?.ssids ?? []
  const sm = d?.summary

  // AP online/offline counts. Prefer the controller-summary split (SSH CLI path);
  // otherwise derive from the AP rows themselves, which carry per-AP status when a
  // source exposes it. So the Active/Non-active KPIs reflect reality for any vendor.
  const apRowOnline = aps.filter((a) => { const s = apState(a.status); return s === 'up' || s === 'warn' }).length
  const apRowOffline = aps.filter((a) => apState(a.status) === 'down').length
  const apStatusShown = (sm?.ap_status_exposed ?? false) || apRowOnline + apRowOffline > 0
  const apActive = sm?.ap_status_exposed ? sm.active_aps : apRowOnline
  const apNonActive = sm?.ap_status_exposed ? sm.non_active_aps : apRowOffline
  const apSites = uniq(aps.map((a) => a.site || '').filter(Boolean))
  const ssidEnabled = ssids.filter((s) => s.status === 'enabled' || s.status === 'active').length

  // Per-SSID rollups derived from the live client roster (honest: only what we saw).
  const ssidRollup = useMemo(() => {
    const m = new Map<string, { clients: number; aps: Set<string> }>()
    for (const cl of (d?.clients ?? [])) {
      if (!cl.ssid) continue
      const e = m.get(cl.ssid) ?? { clients: 0, aps: new Set() }
      e.clients++; if (cl.ap_name) e.aps.add(cl.ap_name)
      m.set(cl.ssid, e)
    }
    return m
  }, [d])

  // Client band breakdown for the Clients summary strip.
  const clientBands = useMemo(() => {
    const m = new Map<string, number>()
    for (const cl of clients) { const b = cl.band || 'unknown'; m.set(b, (m.get(b) ?? 0) + 1) }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1])
  }, [clients])

  const busy = test.isPending || runApi.isPending || runMib.isPending || runSsh.isPending || testSsh.isPending
  // managed_via from the backend (REST/XML leads when a controller profile is the
  // primary path; SNMP is the identity baseline). SSH appended when a CLI run happened.
  const managedVia = d
    ? [
        ...((d.identity.managed_via ?? []).map((m) => (m === 'rest_xml' ? 'REST/XML' : m === 'snmp' ? 'SNMP' : m.toUpperCase()))),
        ...(d.ssh.status === 'collected' || d.ssh.status === 'partial' ? ['SSH'] : []),
      ]
    : []
  const apiLabel = d?.collection.source === 'ruckus_zd_xml' ? 'Ruckus ZoneDirector (Web-XML)'
    : d?.collection.source === 'extreme_xcc_api' ? 'Extreme XCC API'
    : 'Controller API (REST/XML)'
  const apMissing = sm ? Math.max(0, sm.ap_total - (c.aps ?? 0)) : 0
  const cliMissing = sm ? Math.max(0, sm.clients_total - (c.clients ?? 0)) : 0

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'aps', label: 'Access Points', icon: Radio, count: aps.length },
    { key: 'clients', label: 'Clients', icon: Users, count: clients.length },
    { key: 'ssids', label: 'SSIDs', icon: ShieldCheck, count: ssids.length },
    { key: 'manage', label: 'Manage', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Wifi} />

      {msg && <div className={'enc-banner ' + (msg.startsWith('✗') ? 'crit' : 'info')} style={{ whiteSpace: 'pre-wrap', marginBottom: 12 }}>{msg}</div>}

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {!d && <div className="muted" style={{ padding: 16 }}>Loading…</div>}

      {/* ── OVERVIEW ──────────────────────────────────────────────────────── */}
      {tab === 'overview' && d && (
        <>
          <Panel title="Controller" icon={Router}>
            <DefList items={[
              { label: 'Vendor / Product', value: `${d.identity.vendor || '—'}${d.identity.product ? ' · ' + d.identity.product : ''}` },
              { label: 'Model / Firmware', value: `${d.identity.model || '—'}${d.identity.firmware ? ' · ' + d.identity.firmware : ''}` },
              { label: 'Serial', value: d.identity.serial || '—' },
              { label: 'IP address', value: d.identity.ip || '—' },
              { label: 'Status', value: <StatusPill status={statusTone(d.identity.status)} label={cap(d.identity.status)} /> },
              {
                label: 'Managed via', value: (
                  <span className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
                    {(managedVia.length ? managedVia : ['SNMP']).map((m) => <span key={m} className="badge badge-info">{m}</span>)}
                  </span>
                ),
              },
              { label: 'Primary source', value: srcLabel(d.collection.source) },
              { label: 'Last collection', value: d.summary.collected_at ? `${new Date(d.summary.collected_at).toLocaleString()} (${dataAge(d.summary.collected_at)} ago)` : (d.collection.collected_at ? new Date(d.collection.collected_at).toLocaleString() : '—') },
            ]} />
            {d.collection.next_action && <div className="enc-banner info" style={{ marginTop: 12 }}>{d.collection.next_action} <button className="btn btn-ghost btn-xs" style={{ marginLeft: 8 }} onClick={() => setTab('manage')}>Open Manage</button></div>}
          </Panel>

          {/* Summary KPIs — robust whether or not a controller summary exists. */}
          <div className="kpi-grid">
            <Kpi label="Access points" value={c.aps ?? 0} icon={Radio} tone={apMissing > 0 ? 'warn' : 'info'} sub={sm && sm.ap_total ? `controller reports ${sm.ap_total}${apMissing > 0 ? ` · missing ${apMissing}` : ''}` : undefined} />
            <Kpi label="Active APs" value={apStatusShown ? apActive : '—'} icon={Radio} tone="ok" sub={apStatusShown ? undefined : 'status not exposed'} />
            <Kpi label="Non-active APs" value={apStatusShown ? apNonActive : '—'} icon={Radio} tone={apNonActive > 0 ? 'warn' : 'default'} sub={apStatusShown ? undefined : 'status not exposed'} />
            <Kpi label="Clients" value={c.clients ?? 0} icon={Users} tone={cliMissing > 0 ? 'warn' : 'info'} sub={sm && sm.clients_total ? `controller reports ${sm.clients_total}` : undefined} />
            <Kpi label="SSIDs" value={c.ssids ?? 0} icon={ShieldCheck} sub={ssids.length ? `${ssidEnabled} enabled` : undefined} />
            <Kpi label="Sites" value={apSites.length} icon={Router} sub={apSites.length ? undefined : 'not exposed'} />
            <Kpi label="Collection status" value={sm ? collStatusLabelShort(sm.collection_status) : (d.collection.source && d.collection.source !== 'snmp_baseline' ? 'Collected' : 'Not run')} icon={Activity} tone={sm ? collStatusKpiTone(sm.collection_status) : 'default'} sub={sm?.collected_at ? dataAge(sm.collected_at) + ' ago' : undefined} />
            <Kpi label="Events" value={c.events ?? 0} icon={Activity} sub={c.events ? undefined : 'none / not exposed'} />
          </div>

          {/* Honesty warnings — never bury a partial/mismatch. */}
          {sm && (
            <>
              {sm.parsed_ap_rows < sm.ap_total && <div className="enc-banner warn">Controller reports {sm.ap_total} APs but {sm.parsed_ap_rows} AP rows were parsed — collection is partial.</div>}
              {sm.parsed_client_rows < sm.clients_total && <div className="enc-banner warn">Controller reports {sm.clients_total} clients but {sm.parsed_client_rows} client rows were parsed — client counts change live.</div>}
              {!apStatusShown && aps.length > 0 && <div className="enc-banner info">Per-AP active/non-active status is not exposed by the current source.</div>}
            </>
          )}
        </>
      )}

      {/* ── ACCESS POINTS ─────────────────────────────────────────────────── */}
      {tab === 'aps' && d && (
        <Panel title="Access Points" icon={Radio} subtitle={aps.length ? `${aps.length} in roster` : undefined}>
          {aps.length === 0 && <EmptyState icon={Radio} title="No AP inventory" message="Collect via the controller (Manage → Run Collection) to populate the AP roster." />}
          {aps.length > 0 && (
            <>
              <div className="row" style={{ gap: 8, marginBottom: 10, flexWrap: 'wrap' }}>
                {apStatusShown && <span className="badge badge-success">{apActive} active</span>}
                {apStatusShown && <span className="badge badge-warning">{apNonActive} non-active</span>}
                {apSites.length > 0 && <span className="badge badge-info">{apSites.length} site{apSites.length === 1 ? '' : 's'}</span>}
                <span className="badge badge-muted">{aps.length} total</span>
              </div>
              <DataTable<AccessPoint>
                rows={aps}
                getKey={(a) => a.id}
                searchText={(a) => `${a.serial || ''} ${a.name} ${a.ip || ''} ${a.mac || ''} ${a.model || ''} ${a.site || ''}`}
                searchPlaceholder="Search serial / name / IP / MAC / model / site…"
                filters={apFilters(aps)}
                cols={apCols(sm?.ap_status_exposed ?? false)}
              />
            </>
          )}
        </Panel>
      )}

      {/* ── CLIENTS ───────────────────────────────────────────────────────── */}
      {tab === 'clients' && d && (
        <Panel title="Clients" icon={Users} subtitle={sm && sm.clients_total ? `controller reports ${sm.clients_total}` : undefined}>
          {clients.length === 0 && <EmptyState icon={Users} title="No client roster" message="Collect via the controller to pull associated clients. Client counts change live." />}
          {clients.length > 0 && (
            <>
              <div className="row" style={{ gap: 8, marginBottom: 10, flexWrap: 'wrap' }}>
                <span className="badge badge-muted">{clients.length} associated</span>
                {clientBands.slice(0, 4).map(([b, n]) => <span key={b} className="badge badge-info">{b}: {n}</span>)}
              </div>
              <DataTable<WirelessClient>
                rows={clients}
                getKey={(cl) => cl.id}
                searchText={(cl) => `${cl.mac} ${cl.ip || ''} ${cl.hostname || ''} ${cl.ssid || ''} ${cl.ap_name || ''}`}
                searchPlaceholder="Search MAC / IP / hostname / SSID / AP…"
                filters={clientFilters(clients)}
                cols={clientCols}
              />
            </>
          )}
        </Panel>
      )}

      {/* ── SSIDs ─────────────────────────────────────────────────────────── */}
      {tab === 'ssids' && d && (
        <Panel title="SSIDs / WLANs" icon={ShieldCheck} subtitle={ssids.length ? `${ssids.length} configured` : undefined}>
          {ssids.length === 0 && <EmptyState icon={ShieldCheck} title="No SSIDs" message="SSIDs come from the controller's WLAN list or active client associations." />}
          {ssids.length > 0 && (
            <>
              <div className="row" style={{ gap: 8, marginBottom: 10, flexWrap: 'wrap' }}>
                <span className="badge badge-success">{ssidEnabled} enabled</span>
                {ssids.length - ssidEnabled > 0 && <span className="badge badge-muted">{ssids.length - ssidEnabled} disabled/other</span>}
              </div>
              <DataTable
                rows={ssids}
                getKey={(s) => s.id}
                searchText={(s) => `${s.name} ${s.security || ''} ${s.band || ''} ${s.vlan || ''}`}
                searchPlaceholder="Search SSID / security / band / VLAN…"
                pageSizeDefault={25}
                filters={ssidFilters(ssids)}
                cols={[
                  { key: 'name', label: 'SSID', render: (s) => <strong>{s.name}</strong>, sortVal: (s) => s.name },
                  { key: 'status', label: 'Status', render: (s) => <StatusPill status={s.status === 'active' || s.status === 'enabled' ? 'up' : s.status === 'disabled' ? 'down' : 'unknown'} label={s.status || 'unknown'} /> },
                  { key: 'security', label: 'Security', render: (s) => s.security || '—' },
                  { key: 'band', label: 'Band', render: (s) => s.band || '—' },
                  { key: 'net', label: 'Network/VLAN', render: (s) => s.vlan || '—' },
                  { key: 'aps', label: 'APs (seen)', render: (s) => ssidRollup.get(s.name)?.aps.size ?? 0, sortVal: (s) => ssidRollup.get(s.name)?.aps.size ?? 0 },
                  { key: 'clients', label: 'Clients (seen)', render: (s) => ssidRollup.get(s.name)?.clients ?? 0, sortVal: (s) => ssidRollup.get(s.name)?.clients ?? 0 },
                  { key: 'source', label: 'Source', render: (s) => srcLabel(s.source) },
                ]}
              />
            </>
          )}
        </Panel>
      )}

      {/* ── MANAGE ────────────────────────────────────────────────────────── */}
      {tab === 'manage' && d && (
        <>
          <Panel title="Collection & maintenance actions" icon={Settings}>
            <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
              {d.collection.has_api_profile
                ? <button className="btn btn-primary btn-sm" disabled={busy || !pid} onClick={() => { setMsg(''); runApi.mutate() }}><Plug size={14} /> {runApi.isPending ? 'Collecting…' : 'Run Collection (REST/XML)'}</button>
                : <Link className="btn btn-primary btn-sm" to={configureHref}><Plug size={14} /> Configure API profile</Link>}
              {d.collection.has_api_profile && <button className="btn btn-ghost btn-sm" disabled={busy || !pid} onClick={() => { setMsg(''); test.mutate() }}><FlaskConical size={14} /> {test.isPending ? 'Testing…' : 'Test Connection'}</button>}
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => { setMsg(''); runSsh.mutate() }}><DownloadCloud size={14} /> {runSsh.isPending ? 'Collecting…' : 'Run SSH Collection'}</button>
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => { setMsg(''); testSsh.mutate() }}><FlaskConical size={14} /> {testSsh.isPending ? 'Probing…' : 'Test SSH'}</button>
              <button className="btn btn-ghost btn-sm" disabled={busy || !d.mib.has_pack} onClick={() => { setMsg(''); runMib.mutate() }}><DownloadCloud size={14} /> {runMib.isPending ? 'Walking…' : 'Run SNMP MIB Collection'}</button>
              <Link className="btn btn-ghost btn-sm" to={d.mib.pack_id ? `/mibs?pack=${d.mib.pack_id}` : '/mibs'}><FlaskConical size={14} /> Test MIB Pack</Link>
              <RescanSplit targets={d.identity.ip ?? ''} label="Re-scan this device" size="sm" onMsg={setMsg} />
              <Link className="btn btn-ghost btn-sm" to={`/devices/${id}`}><Pencil size={14} /> Edit Device</Link>
            </div>
            <div className="muted" style={{ fontSize: 11, marginTop: 10 }}>
              REST/XML (Web-XML / XCC API) is the primary roster source. SNMP MIB and SSH CLI are read-only secondary/diagnostic sources — they never overwrite the primary AP/client/SSID tables.
            </div>
          </Panel>

          {/* Per-source status — one honest row per collection method. */}
          <Panel title="Collection Sources" icon={Layers}>
            <table className="data-table">
              <thead><tr><th>Source</th><th>Status</th><th>Rows / detail</th><th>Last run</th><th></th></tr></thead>
              <tbody>
                <tr>
                  <td><strong>SNMP Identity</strong></td>
                  <td><StatusPill status={(d.identity.sysobjectid || d.identity.sysdescr) ? 'up' : 'unknown'} label={(d.identity.sysobjectid || d.identity.sysdescr) ? 'collected' : '—'} /></td>
                  <td style={{ fontSize: 12 }}>{d.identity.product || d.identity.vendor || '—'}</td>
                  <td className="muted" style={{ fontSize: 11 }}>from scan</td>
                  <td><Link className="btn btn-ghost btn-xs" to={`/devices/${id}`}>Device</Link></td>
                </tr>
                <tr>
                  <td><strong>{apiLabel}</strong></td>
                  <td><StatusPill status={d.collection.has_api_profile ? (d.collection.ap_data_known ? 'up' : 'warning') : 'unknown'} label={d.collection.has_api_profile ? (d.collection.profile_status || 'configured') : 'not configured'} /></td>
                  <td style={{ fontSize: 12 }}>{d.collection.has_api_profile ? (d.collection.last_detail || 'profile configured') : 'no REST/XML profile'}</td>
                  <td className="muted" style={{ fontSize: 11 }}>{d.collection.collected_at ? dataAge(d.collection.collected_at) + ' ago' : '—'}</td>
                  <td>{d.collection.has_api_profile ? <button className="btn btn-ghost btn-xs" disabled={busy || !pid} onClick={() => { setMsg(''); runApi.mutate() }}>Run</button> : <Link className="btn btn-ghost btn-xs" to={configureHref}>Setup</Link>}</td>
                </tr>
                <tr>
                  <td><strong>SNMP Wireless MIB</strong></td>
                  <td><StatusPill status={d.mib.has_pack ? 'warning' : 'unknown'} label={d.mib.has_pack ? 'operational' : 'no pack'} /></td>
                  <td style={{ fontSize: 12 }}>{d.mib.has_pack ? `${d.mib.walked_tables.filter((t) => t.rows > 0).length} table(s) responded` : 'no applicable pack'}</td>
                  <td className="muted" style={{ fontSize: 11 }}>—</td>
                  <td><button className="btn btn-ghost btn-xs" disabled={busy || !d.mib.has_pack} onClick={() => { setMsg(''); runMib.mutate() }}>Run</button></td>
                </tr>
                <tr>
                  <td><strong>SSH CLI</strong></td>
                  <td><StatusPill status={sshTone2(d.ssh.status)} label={d.ssh.status} /></td>
                  <td style={{ fontSize: 12 }}>{`${d.ssh.aps} APs · ${d.ssh.clients} clients · ${d.ssh.supported.length} cmds`}</td>
                  <td className="muted" style={{ fontSize: 11 }}>{d.ssh.last_run ? dataAge(d.ssh.last_run) + ' ago' : '—'}</td>
                  <td><button className="btn btn-ghost btn-xs" disabled={busy} onClick={() => { setMsg(''); runSsh.mutate() }}>Run</button></td>
                </tr>
              </tbody>
            </table>
          </Panel>

          {/* Diagnostics — SSH command results (redacted). */}
          <Panel title="Diagnostics — SSH command results" icon={Terminal}>
            <button className="btn btn-ghost btn-sm" onClick={() => setShowDiag((v) => !v)}>{showDiag ? 'Hide' : 'Show'} command diagnostics</button>
            {showDiag && (
              <div style={{ marginTop: 12 }}>
                {sshResults.isLoading && <div className="muted">Loading…</div>}
                {sshResults.data && sshResults.data.length === 0 && <EmptyState icon={Terminal} title="No command results" message="Run or test SSH collection first." />}
                {sshResults.data && sshResults.data.length > 0 && (
                  <DataTable
                    rows={sshResults.data}
                    getKey={(r) => r.id}
                    searchText={(r) => `${r.command} ${r.status}`}
                    searchPlaceholder="Search command…"
                    filters={[{ key: 'status', label: 'Status', options: ['parsed', 'not_parsed', 'unsupported', 'failed', 'timeout'].map((v) => ({ value: v, label: v })), match: (r, v) => r.status === v }]}
                    cols={[
                      { key: 'command', label: 'Command', render: (r) => r.command, mono: true, sortVal: (r) => r.command },
                      { key: 'status', label: 'Status', render: (r) => <StatusPill status={sshTone(r.status)} label={r.status} />, sortVal: (r) => r.status },
                      { key: 'lines', label: 'Lines', render: (r) => r.line_count || 0, sortVal: (r) => r.line_count || 0 },
                      { key: 'parsed', label: 'Parsed', render: (r) => r.parsed_rows || 0, sortVal: (r) => r.parsed_rows || 0 },
                      { key: 'skipped', label: 'Skipped', render: (r) => r.skipped_rows ? <span className="badge badge-warning">{r.skipped_rows}</span> : 0 },
                      { key: 'diag', label: 'Headers / warnings', render: (r) => <div style={{ fontSize: 10, maxWidth: 200 }}>{r.headers && <div className="mono" style={{ opacity: .8 }}>{r.headers.slice(0, 70)}</div>}{r.warnings && <div style={{ color: '#ffb74d' }}>{r.warnings}</div>}</div> },
                      { key: 'out', label: 'Output (redacted)', render: (r) => <span style={{ fontFamily: 'monospace', fontSize: 10 }}>{r.error_message ? <span style={{ color: '#ef9a9a' }}>{r.error_message}</span> : (r.output_preview || '—').slice(0, 200)}</span> },
                    ]}
                  />
                )}
              </div>
            )}
          </Panel>

          {/* Advanced — Raw MIB Explorer. */}
          <Panel title="Advanced — Raw MIB Explorer" icon={FileSearch}>
            <button className="btn btn-ghost btn-sm" onClick={() => setShowRaw((v) => !v)}>{showRaw ? 'Hide' : 'Show'} raw MIB</button>
            {showRaw && (
              <div style={{ marginTop: 12 }}>
                {explorer.isLoading && <div className="muted">Loading…</div>}
                {explorer.data && explorer.data.total_rows === 0 && <EmptyState icon={FileSearch} title="Nothing captured" message="Run an SNMP MIB collection to populate the explorer." />}
                {explorer.data && explorer.data.total_rows > 0 && (
                  <>
                    <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>{explorer.data.total_rows} distinct OIDs · {explorer.data.groups.length} columns/subtrees ({rawRows.data?.length ?? 0} raw rows loaded)</div>
                    <DataTable
                      rows={explorer.data.groups}
                      getKey={(g) => g.column_oid}
                      searchText={(g) => `${g.column_oid} ${g.name} ${g.table} ${g.samples.map((s) => s.value).join(' ')}`}
                      searchPlaceholder="Search OID / name / value…"
                      filters={[{ key: 'mapped', label: 'Mapped', options: [{ value: 'yes', label: 'mapped' }, { value: 'no', label: 'unmapped' }], match: (g, v) => (v === 'yes') === g.mapped }]}
                      cols={[
                        { key: 'oid', label: 'Column OID', render: (g) => g.column_oid, mono: true },
                        { key: 'name', label: 'Name', render: (g) => g.name || <span className="muted">(undocumented)</span>, sortVal: (g) => g.name },
                        { key: 'type', label: 'Type', render: (g) => g.value_type, sortVal: (g) => g.value_type },
                        { key: 'rows', label: 'Rows', render: (g) => g.rows, sortVal: (g) => g.rows },
                        { key: 'maps', label: 'Maps to', render: (g) => g.field ? <span className="badge badge-success">{g.purpose}.{g.field}</span> : <span className="muted">—</span> },
                        { key: 'sample', label: 'Sample', render: (g) => <span style={{ fontFamily: 'monospace', fontSize: 10 }}>{g.samples.slice(0, 2).map((s, i) => <div key={i}>{s.index} → {s.value.slice(0, 40)}</div>)}</span> },
                      ]}
                    />
                  </>
                )}
              </div>
            )}
          </Panel>
        </>
      )}
    </div>
  )
}

// ---- AP/client column + filter builders ------------------------------------

// apState normalises any vendor AP-status label to a canonical state so counts,
// badges and filters work for every source: REST/XML stores real labels
// (Ruckus "Connected"/"Disconnected", Extreme "In Service"/"Critical"/"Out of
// Service"), SNMP/SSH store "online"/"offline".
export function apState(status?: string): 'up' | 'down' | 'warn' | 'unknown' {
  const s = (status || '').trim().toLowerCase()
  if (!s) return 'unknown'
  if (['online', 'up', 'active', 'connected', 'in service', 'inservice', 'operational', 'registered', 'running'].includes(s)) return 'up'
  if (['offline', 'down', 'disconnected', 'out of service', 'outofservice', 'inactive', 'unreachable', 'failed'].includes(s)) return 'down'
  if (['critical', 'degraded', 'warning', 'approval pending', 'upgrading', 'provisioning', 'rebooting', 'pending'].some((k) => s.includes(k))) return 'warn'
  return 'unknown'
}

function apStatusCell(a: AccessPoint, exposed: boolean) {
  const st = apState(a.status)
  if (st === 'up') return <StatusPill status="up" label={a.status || 'active'} />
  if (st === 'down') return <StatusPill status="down" label={a.status || 'non-active'} />
  if (st === 'warn') return <StatusPill status="warning" label={a.status || 'degraded'} />
  if (!a.status && !exposed) return <span className="muted" title="Source did not expose per-AP status">status not exposed</span>
  return <StatusPill status="unknown" label={a.status || 'unknown'} />
}

function apCols(exposed: boolean): DataCol<AccessPoint>[] {
  return [
    { key: 'status', label: 'Status', render: (a) => apStatusCell(a, exposed) },
    { key: 'serial', label: 'Serial', render: (a) => a.serial || '—', mono: true, sortVal: (a) => a.serial || '' },
    {
      key: 'name', label: 'Display name', render: (a) => (
        <span>{a.name}{a.serial && a.name === a.serial && <span className="badge badge-muted" title="No friendly name exposed by the CLI; derived from serial" style={{ marginLeft: 6 }}>derived from serial</span>}</span>
      ), sortVal: (a) => a.name,
    },
    { key: 'model', label: 'Model', render: (a) => a.model || '—', sortVal: (a) => a.model || '' },
    { key: 'ip', label: 'IP', render: (a) => a.ip || '—', mono: true },
    { key: 'mac', label: 'MAC', render: (a) => a.mac || '—', mono: true },
    { key: 'net', label: 'Site', render: (a) => a.site || <span className="muted">—</span>, sortVal: (a) => a.site || '' },
    { key: 'uptime', label: 'Uptime', render: (a) => a.uptime || <span className="muted">—</span>, sortVal: (a) => a.uptime || '' },
    { key: 'clients', label: 'Clients', render: (a) => a.client_count, sortVal: (a) => a.client_count },
    { key: 'seen', label: 'Last seen', render: (a) => tsShort(a.collected_at || a.last_seen_at), sortVal: (a) => a.collected_at || '' },
    { key: 'source', label: 'Source', render: (a) => srcLabel(a.source) },
  ]
}

function apFilters(aps: AccessPoint[]): DataFilter<AccessPoint>[] {
  const models = uniq(aps.map((a) => a.model || '').filter(Boolean))
  const sources = uniq(aps.map((a) => a.source || '').filter(Boolean))
  const sites = uniq(aps.map((a) => a.site || '').filter(Boolean))
  const f: DataFilter<AccessPoint>[] = [
    { key: 'status', label: 'Status', options: [{ value: 'up', label: 'active' }, { value: 'down', label: 'non-active' }, { value: 'warn', label: 'degraded' }, { value: 'unknown', label: 'unknown' }], match: (a, v) => apState(a.status) === v },
    { key: 'clients', label: 'Clients', options: [{ value: 'has', label: 'has clients' }, { value: 'none', label: 'no clients' }], match: (a, v) => (v === 'has' ? a.client_count > 0 : a.client_count === 0) },
    { key: 'model', label: 'Model', options: models.map((m) => ({ value: m, label: m })), match: (a, v) => a.model === v },
  ]
  if (sites.length) f.push({ key: 'site', label: 'Site', options: sites.map((s) => ({ value: s, label: s })), match: (a, v) => a.site === v })
  if (sources.length > 1) f.push({ key: 'source', label: 'Source', options: sources.map((s) => ({ value: s, label: srcLabel(s) })), match: (a, v) => a.source === v })
  return f
}

const clientCols: DataCol<WirelessClient>[] = [
  { key: 'mac', label: 'Client MAC', render: (c) => c.mac, mono: true, sortVal: (c) => c.mac },
  { key: 'ip', label: 'IP', render: (c) => c.ip || '—', mono: true, sortVal: (c) => c.ip },
  { key: 'host', label: 'Hostname / user', render: (c) => c.hostname || '—', sortVal: (c) => c.hostname },
  { key: 'ssid', label: 'SSID', render: (c) => c.ssid || '—', sortVal: (c) => c.ssid },
  { key: 'ap', label: 'AP', render: (c) => c.ap_name || '—', sortVal: (c) => c.ap_name },
  { key: 'rssi', label: 'RSSI', render: (c) => (c.rssi != null ? `${c.rssi} dBm` : '—'), sortVal: (c) => c.rssi ?? 0 },
  { key: 'snr', label: 'SNR', render: (c) => (c.snr != null ? `${c.snr} dB` : '—'), sortVal: (c) => c.snr ?? 0 },
  { key: 'band', label: 'Band', render: (c) => c.band || '—' },
  { key: 'rx', label: 'Rx', render: (c) => fmtBytes(c.rx_bytes), sortVal: (c) => c.rx_bytes ?? 0 },
  { key: 'tx', label: 'Tx', render: (c) => fmtBytes(c.tx_bytes), sortVal: (c) => c.tx_bytes ?? 0 },
  { key: 'since', label: 'Connected since', render: (c) => c.connected_since || '—', sortVal: (c) => c.connected_since || '' },
  { key: 'seen', label: 'Last seen', render: (c) => tsShort(c.collected_at), sortVal: (c) => c.collected_at || '' },
  { key: 'source', label: 'Source', render: (c) => srcLabel(c.source) },
]

function clientFilters(clients: WirelessClient[]): DataFilter<WirelessClient>[] {
  const ssidOpts = uniq(clients.map((c) => c.ssid || '').filter(Boolean))
  const bands = uniq(clients.map((c) => c.band || '').filter(Boolean))
  const aps = uniq(clients.map((c) => c.ap_name || '').filter(Boolean))
  const sources = uniq(clients.map((c) => c.source || '').filter(Boolean))
  const f: DataFilter<WirelessClient>[] = [
    { key: 'ssid', label: 'SSID', options: ssidOpts.map((s) => ({ value: s, label: s })), match: (c, v) => c.ssid === v },
    { key: 'band', label: 'Band', options: bands.map((b) => ({ value: b, label: b })), match: (c, v) => c.band === v },
  ]
  if (aps.length) f.push({ key: 'ap', label: 'AP', options: aps.map((a) => ({ value: a, label: a })), match: (c, v) => c.ap_name === v })
  f.push(
    { key: 'ip', label: 'IP', options: [{ value: 'has', label: 'has IP' }, { value: 'no', label: 'no IP' }], match: (c, v) => (v === 'has' ? !!c.ip : !c.ip) },
    { key: 'host', label: 'Hostname', options: [{ value: 'missing', label: 'missing hostname' }], match: (c, v) => (v === 'missing' ? !c.hostname : true) },
  )
  if (sources.length > 1) f.push({ key: 'source', label: 'Source', options: sources.map((s) => ({ value: s, label: srcLabel(s) })), match: (c, v) => c.source === v })
  return f
}

function ssidFilters(ssids: WirelessSSID[]): DataFilter<WirelessSSID>[] {
  const security = uniq(ssids.map((s) => s.security || '').filter(Boolean))
  const bands = uniq(ssids.map((s) => s.band || '').filter(Boolean))
  const sources = uniq(ssids.map((s) => s.source || '').filter(Boolean))
  const ssidState = (s: WirelessSSID) => (s.status === 'active' || s.status === 'enabled') ? 'enabled' : s.status === 'disabled' ? 'disabled' : 'unknown'
  const f: DataFilter<WirelessSSID>[] = [
    { key: 'status', label: 'Status', options: [{ value: 'enabled', label: 'enabled' }, { value: 'disabled', label: 'disabled' }, { value: 'unknown', label: 'unknown' }], match: (s, v) => ssidState(s) === v },
  ]
  if (security.length) f.push({ key: 'security', label: 'Security', options: security.map((x) => ({ value: x, label: x })), match: (s, v) => s.security === v })
  if (bands.length) f.push({ key: 'band', label: 'Band', options: bands.map((b) => ({ value: b, label: b })), match: (s, v) => s.band === v })
  if (sources.length > 1) f.push({ key: 'source', label: 'Source', options: sources.map((x) => ({ value: x, label: srcLabel(x) })), match: (s, v) => s.source === v })
  return f
}

// ---- small helpers ---------------------------------------------------------

function uniq(xs: string[]): string[] { return Array.from(new Set(xs)).sort() }
function cap(s: string): string { return s ? s.charAt(0).toUpperCase() + s.slice(1) : '—' }
function tsShort(ts?: string): string { return ts ? new Date(ts).toLocaleString() : '—' }
function statusTone(s: string): string {
  switch ((s || '').toLowerCase()) { case 'up': case 'online': return 'up'; case 'down': case 'offline': return 'down'; case 'needs attention': return 'warning'; default: return 'unknown' }
}
function dataAge(ts?: string): string {
  if (!ts) return '—'
  const ms = Date.now() - new Date(ts).getTime()
  const m = Math.floor(ms / 60000)
  if (m < 1) return 'just now'
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}
function srcLabel(s?: string): string {
  switch (s) {
    case 'extreme_xcc_ssh': return 'Extreme SSH CLI'
    case 'ruckus_zd_ssh': return 'Ruckus SSH CLI'
    case 'snmp_wireless_mib': return 'SNMP MIB'
    case 'extreme_xcc_api': return 'XCC REST API'
    case 'ruckus_zd_xml': return 'Ruckus ZD Web-XML'
    case 'snmp_baseline': return 'SNMP'
    default: return s || '—'
  }
}
// fmtBytes renders a byte counter as a compact human size; "—" when not reported.
function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${i === 0 ? v : v.toFixed(1)} ${u[i]}`
}
function collStatusKpiTone(s: string): 'ok' | 'warn' | 'crit' | 'info' | 'default' {
  switch (s) { case 'complete': return 'ok'; case 'partial': case 'summary_only': return 'warn'; case 'failed': return 'crit'; default: return 'default' }
}
function collStatusLabelShort(s: string): string {
  switch (s) { case 'complete': return 'Complete'; case 'partial': return 'Partial'; case 'summary_only': return 'Summary only'; case 'failed': return 'Failed'; default: return 'Not run' }
}
function sshTone(s: string): 'up' | 'down' | 'warning' | 'info' {
  switch (s) { case 'parsed': return 'up'; case 'unsupported': return 'warning'; case 'failed': case 'timeout': return 'down'; default: return 'info' }
}
function sshTone2(s: string): 'up' | 'down' | 'warning' | 'info' | 'unknown' {
  switch (s) { case 'collected': return 'up'; case 'partial': return 'warning'; case 'failed': return 'down'; case 'not_run': case 'not_applicable': return 'unknown'; default: return 'info' }
}
