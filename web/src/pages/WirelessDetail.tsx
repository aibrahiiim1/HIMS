import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Wifi, Router, Users, Radio, ShieldCheck, Activity, Plug, FlaskConical, DownloadCloud, Terminal, Layers, FileSearch, Pencil } from 'lucide-react'
import { api, type WirelessDetailResp, type MibWalkRow, type MibExplorerResp, type SSHCliSummary, type SSHCliRow, type AccessPoint, type WirelessClient } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill } from '../components/ui'
import { DataTable, type DataCol, type DataFilter } from '../components/DataTable'

// Wireless Controller — operator dashboard. Honest about which source produced
// which data (SNMP identity / SNMP MIB / SSH CLI / XCC API), never presents
// partial collection as complete, and never invents AP status the source did
// not expose. Rosters are paginated/searchable/sortable client-side.
export function WirelessDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['wireless', id], queryFn: () => api.get<WirelessDetailResp>(`/devices/${id}/wireless`) })
  const d = q.data
  const c = d?.counts ?? {}
  const refetch = () => qc.invalidateQueries({ queryKey: ['wireless', id] })
  const [msg, setMsg] = useState('')

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

  // Collapsible advanced sections (lazy-loaded).
  const [showDiag, setShowDiag] = useState(false)
  const [showRaw, setShowRaw] = useState(false)
  const sshResults = useQuery({ queryKey: ['ssh-cli-results', id], queryFn: () => api.get<SSHCliRow[]>(`/devices/${id}/ssh-cli-results`), enabled: showDiag })
  const explorer = useQuery({ queryKey: ['mib-explorer', id], queryFn: () => api.get<MibExplorerResp>(`/devices/${id}/mib-explorer`), enabled: showRaw })
  const rawRows = useQuery({ queryKey: ['mib-rows', id], queryFn: () => api.get<MibWalkRow[]>(`/devices/${id}/mib-rows`), enabled: showRaw })

  const configureHref = d
    ? `/vendor-profiles?create=1&vendor_type=extreme_xcc&device_id=${id}&target_url=${encodeURIComponent(`https://${d.identity.ip}:8443`)}`
    : '/vendor-profiles'

  const aps = d?.aps ?? []
  const clients = d?.clients ?? []
  const ssids = d?.ssids ?? []
  const sm = d?.summary

  // AP online/offline counts. Prefer the controller-summary split (SSH CLI path);
  // otherwise derive from the AP rows themselves, which carry per-AP status when a
  // source exposes it (e.g. Ruckus SNMP MIB). So the Active/Non-active KPIs reflect
  // reality for any vendor, not only the SSH-summary controllers.
  const apRowOnline = aps.filter((a) => a.status === 'online').length
  const apRowOffline = aps.filter((a) => a.status === 'offline').length
  const apStatusShown = (sm?.ap_status_exposed ?? false) || apRowOnline + apRowOffline > 0
  const apActive = sm?.ap_status_exposed ? sm.active_aps : apRowOnline
  const apNonActive = sm?.ap_status_exposed ? sm.non_active_aps : apRowOffline

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

  const busy = test.isPending || runApi.isPending || runMib.isPending || runSsh.isPending || testSsh.isPending
  const managedVia = d ? ['SNMP', ...(d.ssh.status === 'collected' || d.ssh.status === 'partial' ? ['SSH'] : []), ...(d.collection.has_api_profile ? ['API'] : [])] : []

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Wifi} />

      {/* Action toolbar + identity. */}
      <Panel title="Controller" icon={Router}>
        {d && (
          <>
            <DefList items={[
              { label: 'Vendor / Product', value: `${d.identity.vendor || '—'}${d.identity.product ? ' · ' + d.identity.product : ''}` },
              { label: 'Model / Firmware', value: `${d.identity.model || '—'}${d.identity.firmware ? ' · ' + d.identity.firmware : ''}` },
              { label: 'Status / Managed via', value: `${cap(d.identity.status)} · Managed via ${managedVia.join(' + ') || 'SNMP'}` },
              { label: 'Last collection', value: d.summary.collected_at ? `${new Date(d.summary.collected_at).toLocaleString()} (${dataAge(d.summary.collected_at)})` : (d.collection.collected_at ? new Date(d.collection.collected_at).toLocaleString() : '—') },
            ]} />
            <div className="row" style={{ gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
              <button className="btn btn-primary btn-sm" disabled={busy} onClick={() => { setMsg(''); runSsh.mutate() }}><DownloadCloud size={14} /> {runSsh.isPending ? 'Collecting…' : 'Run SSH Collection'}</button>
              <button className="btn btn-ghost btn-sm" disabled={busy || !d.mib.has_pack} onClick={() => { setMsg(''); runMib.mutate() }}><DownloadCloud size={14} /> {runMib.isPending ? 'Walking…' : 'Run SNMP MIB Collection'}</button>
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => { setMsg(''); testSsh.mutate() }}><FlaskConical size={14} /> {testSsh.isPending ? 'Probing…' : 'Test SSH'}</button>
              <Link className="btn btn-ghost btn-sm" to={d.mib.pack_id ? `/mibs?pack=${d.mib.pack_id}` : '/mibs'}><FlaskConical size={14} /> Test MIB Pack</Link>
              {d.collection.has_api_profile
                ? <button className="btn btn-ghost btn-sm" disabled={busy || !pid} onClick={() => { setMsg(''); runApi.mutate() }}><Plug size={14} /> Run XCC API</button>
                : <Link className="btn btn-ghost btn-sm" to={configureHref}><Plug size={14} /> Configure API Profile</Link>}
              <Link className="btn btn-ghost btn-sm" to={`/devices/${id}`}><Pencil size={14} /> Edit Device</Link>
            </div>
            {msg && <div className={'enc-banner ' + (msg.startsWith('✗') ? 'crit' : 'info')} style={{ marginTop: 10, whiteSpace: 'pre-wrap' }}>{msg}</div>}
          </>
        )}
      </Panel>

      {/* KPI cards — controller-reported vs parsed, never overstated. */}
      {d && sm && (() => {
        const apMissing = Math.max(0, sm.ap_total - (c.aps ?? 0))
        const cliMissing = Math.max(0, sm.clients_total - (c.clients ?? 0))
        return (
          <div className="kpi-grid">
            <Kpi label="Total APs (reported)" value={sm.ap_total || (c.aps ?? 0)} icon={Radio} tone={apMissing > 0 ? 'warn' : 'info'} sub={`parsed ${c.aps ?? 0} rows${apMissing > 0 ? ` · missing ${apMissing}` : ''}`} />
            <Kpi label="Active APs" value={apStatusShown ? apActive : '—'} icon={Radio} sub={apStatusShown ? undefined : 'status not exposed'} />
            <Kpi label="Non-active APs" value={apStatusShown ? apNonActive : '—'} icon={Radio} sub={apStatusShown ? undefined : 'status not exposed'} />
            <Kpi label="Total clients (reported)" value={sm.clients_total || (c.clients ?? 0)} icon={Users} tone={cliMissing > 0 ? 'warn' : 'info'} sub={`parsed ${c.clients ?? 0}${cliMissing > 0 ? ` · live ±${cliMissing}` : ''}`} />
            <Kpi label="SSIDs" value={c.ssids ?? 0} icon={ShieldCheck} sub="full WLAN list incl. disabled (show wlans)" />
            <Kpi label="Networks" value={sm.networks} icon={Router} sub="client-derived" />
            <Kpi label="Switches" value={sm.switches} icon={Router} />
            <Kpi label="Collection status" value={collStatusLabelShort(sm.collection_status)} icon={Activity} tone={collStatusKpiTone(sm.collection_status)} sub={sm.collected_at ? dataAge(sm.collected_at) + ' ago' : undefined} />
          </div>
        )
      })()}

      {/* Mismatch / partial / not-exposed warnings — front and centre, never buried. */}
      {d && sm && (
        <>
          {sm.parsed_ap_rows < sm.ap_total && (
            <div className="enc-banner warn">Controller summary reports {sm.ap_total} APs but HIMS parsed {sm.parsed_ap_rows} AP rows from SSH CLI. Collection is partial — review parser/command coverage.</div>
          )}
          {sm.parsed_client_rows < sm.clients_total && (
            <div className="enc-banner warn">Controller summary reports {sm.clients_total} clients but HIMS parsed {sm.parsed_client_rows} client rows. Client counts change live.</div>
          )}
          {!apStatusShown && aps.length > 0 && (
            <div className="enc-banner info">Per-AP active/non-active status is not exposed by this controller's SSH CLI (show summary/status are rejected). Configure the Extreme XCC API for per-AP status.</div>
          )}
          {sm.ap_status_exposed && sm.non_active_aps > 0 && (
            <div className="enc-banner warn">Controller reports {sm.non_active_aps} non-active AP(s), but the current source does not expose which individual APs are non-active.</div>
          )}
        </>
      )}

      {/* Collection sources — one honest row per source. */}
      <Panel title="Collection Sources" icon={Layers}>
        {d && (
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
                <td><strong>SNMP Wireless MIB</strong></td>
                <td><StatusPill status={d.mib.has_pack ? 'warning' : 'unknown'} label={d.mib.has_pack ? 'operational only' : 'no pack'} /></td>
                <td style={{ fontSize: 12 }}>{d.mib.has_pack ? `${d.mib.walked_tables.filter(t => t.rows > 0).length} table(s) responded · no AP roster on this firmware` : 'no applicable pack'}</td>
                <td className="muted" style={{ fontSize: 11 }}>—</td>
                <td><button className="btn btn-ghost btn-xs" disabled={busy || !d.mib.has_pack} onClick={() => { setMsg(''); runMib.mutate() }}>Run</button></td>
              </tr>
              <tr>
                <td><strong>SSH CLI (Extreme XCC)</strong></td>
                <td><StatusPill status={sshTone2(d.ssh.status)} label={d.ssh.status} /></td>
                <td style={{ fontSize: 12 }}>{d.ssh.aps} APs · {d.ssh.clients} clients · {d.ssh.supported.length} supported / {d.ssh.unsupported.length} unsupported cmds</td>
                <td className="muted" style={{ fontSize: 11 }}>{d.ssh.last_run ? new Date(d.ssh.last_run).toLocaleString() : '—'}</td>
                <td><button className="btn btn-ghost btn-xs" disabled={busy} onClick={() => { setMsg(''); runSsh.mutate() }}>Run</button></td>
              </tr>
              <tr>
                <td><strong>Extreme XCC API</strong></td>
                <td><StatusPill status={d.collection.has_api_profile ? (d.collection.ap_data_known ? 'up' : 'warning') : 'unknown'} label={d.collection.has_api_profile ? (d.collection.profile_status || 'configured') : 'not configured'} /></td>
                <td style={{ fontSize: 12 }}>{d.collection.has_api_profile ? (d.collection.last_detail || 'profile configured') : 'no JSON API profile'}</td>
                <td className="muted" style={{ fontSize: 11 }}>—</td>
                <td>{d.collection.has_api_profile ? <button className="btn btn-ghost btn-xs" disabled={busy || !pid} onClick={() => { setMsg(''); runApi.mutate() }}>Run</button> : <Link className="btn btn-ghost btn-xs" to={configureHref}>Setup</Link>}</td>
              </tr>
            </tbody>
          </table>
        )}
      </Panel>

      {/* Access Points. */}
      <Panel title={`Access Points${aps.length ? ` (${aps.length})` : ''}`} icon={Radio}>
        {d && aps.length === 0 && <EmptyState icon={Radio} title="No AP inventory" message="Run SSH Collection to pull the AP roster, or configure the Extreme XCC API." />}
        {d && aps.length > 0 && (
          <DataTable<AccessPoint>
            rows={aps}
            getKey={(a) => a.id}
            searchText={(a) => `${a.serial || ''} ${a.name} ${a.ip || ''} ${a.mac || ''} ${a.model || ''}`}
            searchPlaceholder="Search serial / name / IP / MAC / model…"
            filters={apFilters(aps)}
            cols={apCols(sm?.ap_status_exposed ?? false)}
          />
        )}
      </Panel>

      {/* Clients. */}
      <Panel title={`Clients${clients.length ? ` (${clients.length})` : ''}`} icon={Users}
        subtitle={sm && sm.clients_total ? `controller reports ${sm.clients_total}` : undefined}>
        {d && clients.length === 0 && <EmptyState icon={Users} title="No client roster" message="Run SSH Collection to pull associated clients. Client counts change live." />}
        {d && clients.length > 0 && (
          <DataTable<WirelessClient>
            rows={clients}
            getKey={(cl) => cl.id}
            searchText={(cl) => `${cl.mac} ${cl.ip || ''} ${cl.hostname || ''} ${cl.ssid || ''} ${cl.ap_name || ''}`}
            searchPlaceholder="Search MAC / IP / hostname / SSID / AP…"
            filters={clientFilters(clients)}
            cols={clientCols}
          />
        )}
      </Panel>

      {/* SSIDs / WLANs. */}
      <Panel title={`SSIDs / WLANs${ssids.length ? ` (${ssids.length})` : ''}`} icon={ShieldCheck}>
        {d && ssids.length === 0 && <EmptyState icon={ShieldCheck} title="No SSIDs" message="SSIDs are derived from active client associations or the controller API." />}
        {d && ssids.length > 0 && (
          <DataTable
            rows={ssids}
            getKey={(s) => s.id}
            searchText={(s) => `${s.name} ${s.security || ''} ${s.band || ''}`}
            searchPlaceholder="Search SSID…"
            pageSizeDefault={25}
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
        )}
      </Panel>

      {/* Diagnostics — collapsible. */}
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

      {/* Advanced / Raw MIB — collapsible. */}
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
                  searchText={(g) => `${g.column_oid} ${g.name} ${g.table} ${g.samples.map(s => s.value).join(' ')}`}
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
    </div>
  )
}

// ---- AP/client column + filter builders ------------------------------------

function apStatusCell(a: AccessPoint, exposed: boolean) {
  if (a.status === 'online') return <StatusPill status="up" label="active" />
  if (a.status === 'offline') return <StatusPill status="down" label="non-active" />
  if (!exposed) return <span className="muted" title="Source did not expose per-AP status">status not exposed</span>
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
    { key: 'net', label: 'Network/Site', render: () => <span className="muted">—</span> },
    { key: 'adoption', label: 'Adoption', render: () => <span className="muted">—</span> },
    { key: 'clients', label: 'Clients', render: (a) => a.client_count, sortVal: (a) => a.client_count },
    { key: 'seen', label: 'Last seen', render: (a) => tsShort(a.collected_at || a.last_seen_at), sortVal: (a) => a.collected_at || '' },
    { key: 'source', label: 'Source', render: (a) => srcLabel(a.source) },
  ]
}

function apFilters(aps: AccessPoint[]): DataFilter<AccessPoint>[] {
  const models = uniq(aps.map((a) => a.model || '').filter(Boolean))
  const sources = uniq(aps.map((a) => a.source || '').filter(Boolean))
  return [
    { key: 'status', label: 'Status', options: [{ value: 'online', label: 'active' }, { value: 'offline', label: 'non-active' }, { value: 'unknown', label: 'unknown' }], match: (a, v) => (a.status || 'unknown') === v },
    { key: 'clients', label: 'Clients', options: [{ value: 'has', label: 'has clients' }, { value: 'none', label: 'no clients' }], match: (a, v) => (v === 'has' ? a.client_count > 0 : a.client_count === 0) },
    { key: 'model', label: 'Model', options: models.map((m) => ({ value: m, label: m })), match: (a, v) => a.model === v },
    { key: 'source', label: 'Source', options: sources.map((s) => ({ value: s, label: srcLabel(s) })), match: (a, v) => a.source === v },
  ]
}

const clientCols: DataCol<WirelessClient>[] = [
  { key: 'mac', label: 'Client MAC', render: (c) => c.mac, mono: true, sortVal: (c) => c.mac },
  { key: 'ip', label: 'IP', render: (c) => c.ip || '—', mono: true, sortVal: (c) => c.ip },
  { key: 'host', label: 'Hostname / user', render: (c) => c.hostname || '—', sortVal: (c) => c.hostname },
  { key: 'ssid', label: 'SSID', render: (c) => c.ssid || '—', sortVal: (c) => c.ssid },
  { key: 'ap', label: 'AP (serial)', render: (c) => c.ap_name || '—', mono: true, sortVal: (c) => c.ap_name },
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
  return [
    { key: 'ssid', label: 'SSID', options: ssidOpts.map((s) => ({ value: s, label: s })), match: (c, v) => c.ssid === v },
    { key: 'band', label: 'Band', options: bands.map((b) => ({ value: b, label: b })), match: (c, v) => c.band === v },
    { key: 'ip', label: 'IP', options: [{ value: 'has', label: 'has IP' }, { value: 'no', label: 'no IP' }], match: (c, v) => (v === 'has' ? !!c.ip : !c.ip) },
    { key: 'host', label: 'Hostname', options: [{ value: 'missing', label: 'missing hostname' }], match: (c, v) => (v === 'missing' ? !c.hostname : true) },
  ]
}

// ---- small helpers ---------------------------------------------------------

function uniq(xs: string[]): string[] { return Array.from(new Set(xs)).sort() }
function cap(s: string): string { return s ? s.charAt(0).toUpperCase() + s.slice(1) : '—' }
function tsShort(ts?: string): string { return ts ? new Date(ts).toLocaleString() : '—' }
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
    case 'extreme_xcc_ssh': return 'SSH CLI'
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
  switch (s) { case 'collected': return 'up'; case 'partial': return 'warning'; case 'failed': return 'down'; case 'not_run': return 'unknown'; default: return 'info' }
}
