import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Wifi, Router, Users, Radio, ShieldCheck, Activity, AlertTriangle, Plug, FlaskConical, DownloadCloud, Layers, FileSearch, Terminal } from 'lucide-react'
import { api, type WirelessDetailResp, type MibWalkRow, type MibExplorerResp, type SSHCliSummary, type SSHCliRow } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill } from '../components/ui'

const apStatus = (s: string) => (s === 'online' ? 'up' : s === 'offline' ? 'down' : 'unknown')
const sevTone = (s: string) => (s === 'critical' ? 'down' : s === 'warning' ? 'warning' : 'info')

// Wireless Controller detail. Identity + "managed via SNMP" always show (from the
// device's stored SNMP facts). The AP/SSID/client/radio rosters come from a
// roster-capable collector (Extreme XCC API profile); until one runs, the page is
// honest about what's missing and points at the exact next action.
export function WirelessDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['wireless', id], queryFn: () => api.get<WirelessDetailResp>(`/devices/${id}/wireless`) })
  const d = q.data
  const c = d?.counts ?? {}
  const apiNeeded = !!d && !d.collection.has_api_profile
  const [actionMsg, setActionMsg] = useState('')

  const pid = d?.collection.profile_id
  const refetch = () => qc.invalidateQueries({ queryKey: ['wireless', id] })
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; detail: string }>(`/vendor-profiles/${pid}/test`, {}),
    onSuccess: (r) => { setActionMsg((r.ok ? '✓ ' : '✗ ') + r.detail); refetch() },
    onError: (e) => setActionMsg((e as Error).message),
  })
  const run = useMutation({
    mutationFn: () => api.post<{ collected: boolean; detail: string }>(`/vendor-profiles/${pid}/run-collection`, { device_id: id }),
    onSuccess: (r) => { setActionMsg((r.collected ? '✓ ' : '⚠ ') + r.detail); refetch() },
    onError: (e) => setActionMsg((e as Error).message),
  })

  // SNMP Wireless MIB collection (independent of the REST API path).
  const [mibMsg, setMibMsg] = useState('')
  const [showRaw, setShowRaw] = useState(false)
  const runMib = useMutation({
    mutationFn: () => api.post<{ collected: boolean; detail: string }>(`/devices/${id}/collect-wireless-mib`, {}),
    onSuccess: (r) => { setMibMsg((r.collected ? '✓ ' : '⚠ ') + r.detail); refetch() },
    onError: (e) => setMibMsg((e as Error).message),
  })
  const rawRows = useQuery({
    queryKey: ['mib-rows', id], queryFn: () => api.get<MibWalkRow[]>(`/devices/${id}/mib-rows`), enabled: showRaw,
  })
  const [showExplorer, setShowExplorer] = useState(false)
  const explorer = useQuery({
    queryKey: ['mib-explorer', id], queryFn: () => api.get<MibExplorerResp>(`/devices/${id}/mib-explorer`), enabled: showExplorer,
  })

  // Extreme XCC SSH CLI collection.
  const [sshMsg, setSshMsg] = useState('')
  const [showSshResults, setShowSshResults] = useState(false)
  const runSsh = useMutation({
    mutationFn: () => api.post<SSHCliSummary>(`/devices/${id}/collect-ssh-cli`, {}),
    onSuccess: (r) => { setSshMsg((r.ok ? '✓ ' : '⚠ ') + r.detail); refetch() },
    onError: (e) => setSshMsg('✗ ' + (e as Error).message),
  })
  const testSsh = useMutation({
    mutationFn: () => api.post<SSHCliSummary>(`/devices/${id}/test-ssh-cli`, {}),
    onSuccess: (r) => { setSshMsg((r.ok ? '✓ ' : '⚠ ') + r.detail); refetch() },
    onError: (e) => setSshMsg('✗ ' + (e as Error).message),
  })
  const sshResults = useQuery({
    queryKey: ['ssh-cli-results', id], queryFn: () => api.get<SSHCliRow[]>(`/devices/${id}/ssh-cli-results`), enabled: showSshResults,
  })
  const configureHref = d
    ? `/vendor-profiles?create=1&vendor_type=extreme_xcc&device_id=${id}&target_url=${encodeURIComponent(`https://${d.identity.ip}:8443`)}`
    : '/vendor-profiles'

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Wifi} />

      {d && (
        <div className="kpi-grid">
          <Kpi label="Controller" value={d.identity.vendor || '—'} icon={Router} tone="info" sub={d.identity.product || undefined} />
          <Kpi label="Access Points" value={c.aps ?? 0} icon={Radio} sub={d.collection.ap_data_known ? `${c.aps_online ?? 0} online / ${c.aps_offline ?? 0} offline` : 'no roster yet'} />
          <Kpi label="SSIDs" value={c.ssids ?? 0} icon={ShieldCheck} />
          <Kpi label="Clients" value={c.clients ?? 0} icon={Users} />
        </div>
      )}

      {/* Controller identity — always available from SNMP. */}
      <Panel title="Controller Identity" icon={Router}>
        {d && (
          <DefList items={[
            { label: 'Name', value: d.identity.name || '—' },
            { label: 'Vendor', value: d.identity.vendor || '—' },
            { label: 'Product', value: d.identity.product || '—' },
            { label: 'Model', value: d.identity.model || '—' },
            { label: 'Firmware / Version', value: d.identity.firmware || '—' },
            { label: 'Serial', value: d.identity.serial || '—' },
            { label: 'sysName', value: d.identity.sysname || '—' },
            { label: 'sysObjectID', value: d.identity.sysobjectid || '—' },
            { label: 'sysDescr', value: d.identity.sysdescr || '—' },
            { label: 'Managed via', value: (d.identity.managed_via ?? []).join(', ').toUpperCase() || '—' },
          ]} />
        )}
      </Panel>

      {/* Collection status + next action. */}
      <Panel title="Wireless Collection" icon={Activity}>
        {d && (
          <>
            <DefList items={[
              { label: 'Source', value: collLabel(d.collection.source) },
              { label: 'Last collected', value: d.collection.collected_at ? new Date(d.collection.collected_at).toLocaleString() : '—' },
              { label: 'API profile', value: d.collection.has_api_profile ? `configured${d.collection.profile_status ? ` (${d.collection.profile_status})` : ''}` : 'not configured' },
              ...(d.collection.last_detail ? [{ label: 'Last result', value: d.collection.last_detail }] : []),
            ]} />
            {apiNeeded && (
              <div className="enc-banner info" style={{ marginTop: 10 }}>
                <strong>Deep wireless data: API profile required.</strong> {d.collection.next_action}
              </div>
            )}
            {!apiNeeded && d.collection.next_action && (
              <div className="enc-banner info" style={{ marginTop: 10 }}>{d.collection.next_action}</div>
            )}
            <div className="row" style={{ gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
              {apiNeeded
                ? <Link className="btn btn-primary btn-sm" to={configureHref}><Plug size={14} /> Configure Extreme XCC Profile</Link>
                : <>
                    <button className="btn btn-ghost btn-sm" disabled={!pid || test.isPending} onClick={() => { setActionMsg(''); test.mutate() }}><FlaskConical size={14} /> {test.isPending ? 'Testing…' : 'Test Connection'}</button>
                    <button className="btn btn-primary btn-sm" disabled={!pid || run.isPending} onClick={() => { setActionMsg(''); run.mutate() }}><DownloadCloud size={14} /> {run.isPending ? 'Collecting…' : 'Run Collection'}</button>
                    <Link className="btn btn-ghost btn-sm" to="/vendor-profiles"><Plug size={14} /> Manage Profile</Link>
                  </>}
            </div>
            {actionMsg && <div className={'enc-banner ' + (actionMsg.startsWith('✗') ? 'crit' : 'info')} style={{ marginTop: 10, whiteSpace: 'pre-wrap' }}>{actionMsg}</div>}
          </>
        )}
      </Panel>

      {/* SNMP Wireless MIB — built-in/user MIB pack collection over SNMP. */}
      <Panel title="SNMP Wireless MIB" icon={Layers}>
        {d && (
          <>
            {(() => {
              const tablesWithRows = d.mib.walked_tables.filter((t) => t.rows > 0)
              const rawTotal = d.mib.walked_tables.reduce((n, t) => n + t.rows, 0)
              return (
                <DefList items={[
                  { label: 'Applicable MIB pack', value: d.mib.has_pack ? `${d.mib.pack_name} (${d.mib.pack_source})` : 'none — no enabled pack matches this controller' },
                  { label: 'Supported tables (responded)', value: tablesWithRows.length
                    ? tablesWithRows.map((t) => `${t.table} (${t.rows} rows)`).join(', ')
                    : 'none responded with rows' },
                  { label: 'Raw MIB rows captured', value: rawTotal ? `${rawTotal}` : '0 — run a collection' },
                  { label: 'Mapped into wireless tables', value: mappedSummary(d) },
                ]} />
              )
            })()}
            {!d.mib.has_pack && (
              <div className="enc-banner info" style={{ marginTop: 10 }}>
                No MIB pack applies. Upload or enable a pack whose applies-to matches this controller in <Link to="/mibs">MIB Management</Link> — the built-in Extreme/HiPath pack is a fallback.
              </div>
            )}
            <div className="row" style={{ gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
              <button className="btn btn-primary btn-sm" disabled={!d.mib.has_pack || runMib.isPending} onClick={() => { setMibMsg(''); runMib.mutate() }}>
                <DownloadCloud size={14} /> {runMib.isPending ? 'Collecting…' : 'Run SNMP Wireless Collection'}
              </button>
              <button className="btn btn-ghost btn-sm" onClick={() => setShowExplorer((v) => !v)}><Layers size={14} /> {showExplorer ? 'Hide MIB Explorer' : 'Open MIB Explorer'}</button>
              <Link className="btn btn-ghost btn-sm" to={d.mib.pack_id ? `/mibs?pack=${d.mib.pack_id}` : '/mibs'}><FlaskConical size={14} /> Test MIB Pack</Link>
              <Link className="btn btn-ghost btn-sm" to={d.mib.pack_id ? `/mibs?pack=${d.mib.pack_id}` : '/mibs'}><Plug size={14} /> Create mapping</Link>
              <button className="btn btn-ghost btn-sm" onClick={() => setShowRaw((v) => !v)}><FileSearch size={14} /> {showRaw ? 'Hide Raw MIB Rows' : 'View Raw MIB Rows'}</button>
            </div>
            {mibMsg && <div className={'enc-banner ' + (mibMsg.startsWith('✗') ? 'crit' : 'info')} style={{ marginTop: 10, whiteSpace: 'pre-wrap' }}>{mibMsg}</div>}
            {showExplorer && (
              <div style={{ marginTop: 12 }}>
                {explorer.isLoading && <div className="muted">Loading MIB explorer…</div>}
                {explorer.data && explorer.data.total_rows === 0 && <EmptyState icon={Layers} title="Nothing captured yet" message="Run an SNMP MIB collection (or test the pack) to populate the explorer." />}
                {explorer.data && explorer.data.total_rows > 0 && (
                  <>
                    <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>{explorer.data.total_rows} distinct OIDs · {explorer.data.groups.length} columns/subtrees. Subtrees the bundled MIB documents are named; firmware-specific objects show their nearest known parent.</div>
                    <table className="data-table">
                      <thead><tr><th>Column OID</th><th>Name</th><th>Type</th><th>Rows</th><th>Maps to</th><th>Sample (index → value)</th></tr></thead>
                      <tbody>
                        {explorer.data.groups.slice(0, 120).map((g) => (
                          <tr key={g.column_oid}>
                            <td className="mono" style={{ fontSize: 11 }}>{g.column_oid}</td>
                            <td>{g.name || <span className="muted">(undocumented)</span>}</td>
                            <td>{g.value_type}</td>
                            <td>{g.rows}</td>
                            <td>{g.field ? <span className="badge badge-success">{g.purpose}.{g.field}</span> : <Link to={mapLink(d.mib.pack_id, g)}>map…</Link>}</td>
                            <td className="mono" style={{ fontSize: 10, maxWidth: 280, overflow: 'hidden' }}>
                              {g.samples.slice(0, 3).map((s, i) => <div key={i}>{s.index} → {s.value}</div>)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {explorer.data.groups.length > 120 && <div className="muted" style={{ marginTop: 6 }}>Showing first 120 of {explorer.data.groups.length} columns.</div>}
                  </>
                )}
              </div>
            )}
            {showRaw && (
              <div style={{ marginTop: 12 }}>
                {rawRows.isLoading && <div className="muted">Loading raw rows…</div>}
                {rawRows.data && rawRows.data.length === 0 && <EmptyState icon={FileSearch} title="No raw MIB rows" message="Run a collection or test against this device to capture raw walk rows." />}
                {rawRows.data && rawRows.data.length > 0 && (
                  <table className="data-table">
                    <thead><tr><th>Table</th><th>OID</th><th>Index</th><th>Value</th></tr></thead>
                    <tbody>
                      {rawRows.data.slice(0, 300).map((rw) => (
                        <tr key={rw.id}>
                          <td>{rw.table_name}</td>
                          <td className="mono" style={{ fontSize: 11 }}>{rw.oid}</td>
                          <td className="mono">{rw.idx}</td>
                          <td style={{ fontSize: 12 }}>{rw.raw_value}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {rawRows.data && rawRows.data.length > 300 && <div className="muted" style={{ marginTop: 6 }}>Showing first 300 of {rawRows.data.length} rows.</div>}
              </div>
            )}
          </>
        )}
      </Panel>

      {/* Extreme XCC SSH CLI — read-only CLI roster collection. */}
      <Panel title="SSH CLI (Extreme XCC)" icon={Terminal}>
        {d && (
          <>
            <DefList items={[
              { label: 'Collection sources', value: collectionSourcesLine(d) },
              { label: 'SSH CLI status', value: sshStatusLabel(d.ssh.status) + (d.ssh.last_run ? ` · last run ${new Date(d.ssh.last_run).toLocaleString()}` : '') },
              { label: 'Supported commands', value: d.ssh.supported.length ? d.ssh.supported.join(', ') : '— none yet' },
              { label: 'Unsupported commands', value: d.ssh.unsupported.length ? d.ssh.unsupported.join(', ') : '—' },
              { label: 'Mapped via SSH', value: (d.ssh.aps || d.ssh.clients) ? `${d.ssh.aps} APs, ${d.ssh.clients} clients, ${d.ssh.parsed_rows} parsed rows` : `${d.ssh.parsed_rows} parsed rows (no AP/client roster)` },
            ]} />
            {d.ssh.status === 'unsupported' && (
              <div className="enc-banner info" style={{ marginTop: 10 }}>SSH CLI does not expose AP/SSID/client roster using supported commands on this controller's restricted CLI.</div>
            )}
            {(d.ssh.status === 'partial') && (d.ssh.aps + d.ssh.clients === 0) && (
              <div className="enc-banner info" style={{ marginTop: 10 }}>SSH CLI ran but does not expose AP/SSID/client roster using the supported commands; identity/operational/log output captured.</div>
            )}
            <div className="row" style={{ gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
              <button className="btn btn-primary btn-sm" disabled={runSsh.isPending} onClick={() => { setSshMsg(''); runSsh.mutate() }}>
                <DownloadCloud size={14} /> {runSsh.isPending ? 'Collecting…' : 'Run SSH CLI Collection'}
              </button>
              <button className="btn btn-ghost btn-sm" disabled={testSsh.isPending} onClick={() => { setSshMsg(''); testSsh.mutate() }}>
                <FlaskConical size={14} /> {testSsh.isPending ? 'Probing…' : 'Test SSH CLI Commands'}
              </button>
              <button className="btn btn-ghost btn-sm" onClick={() => setShowSshResults((v) => !v)}>
                <Terminal size={14} /> {showSshResults ? 'Hide SSH Command Results' : 'View SSH Command Results'}
              </button>
            </div>
            {sshMsg && <div className={'enc-banner ' + (sshMsg.startsWith('✗') ? 'crit' : 'info')} style={{ marginTop: 10, whiteSpace: 'pre-wrap' }}>{sshMsg}</div>}
            {showSshResults && (
              <div style={{ marginTop: 12 }}>
                {sshResults.isLoading && <div className="muted">Loading command results…</div>}
                {sshResults.data && sshResults.data.length === 0 && <EmptyState icon={Terminal} title="No SSH command results" message="Run or test SSH CLI collection to probe the controller's read-only commands." />}
                {sshResults.data && sshResults.data.length > 0 && (
                  <table className="data-table">
                    <thead><tr><th>Command</th><th>Status</th><th>Rows</th><th>Output preview (redacted)</th></tr></thead>
                    <tbody>
                      {sshResults.data.map((r) => (
                        <tr key={r.id}>
                          <td className="mono">{r.command}</td>
                          <td><StatusPill status={sshTone(r.status)} label={r.status} /></td>
                          <td>{r.parsed_rows || (r.error_message ? '—' : 0)}</td>
                          <td style={{ fontFamily: 'monospace', fontSize: 10, maxWidth: 460, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                            {r.error_message ? <span style={{ color: '#ef9a9a' }}>{r.error_message}</span> : (r.output_preview || '—').slice(0, 400)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            )}
          </>
        )}
      </Panel>

      {/* Access Points. */}
      <Panel title="Access Points" icon={Radio} subtitle={d?.collection.ap_data_known ? `${c.aps_online}/${c.aps} online` : undefined} pad={false}>
        {d && d.aps.length === 0 && (
          <EmptyState icon={Radio} title="No AP inventory yet"
            message={apiNeeded
              ? 'Per-AP detail (model, serial, firmware, clients, status) requires the controller API. Configure an Extreme XCC profile, then Run Collection.'
              : 'No access points reported by the last collection.'} />
        )}
        {d && d.aps.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Model</th><th>IP</th><th>MAC</th><th>Serial</th><th>Firmware</th><th>Clients</th><th>Status</th></tr></thead>
            <tbody>
              {d.aps.map((a) => (
                <tr key={a.id}>
                  <td className="cell-name">{a.name}</td>
                  <td>{a.model || '—'}</td>
                  <td className="mono">{a.ip || '—'}</td>
                  <td className="mono">{a.mac || '—'}</td>
                  <td className="mono">{a.serial || '—'}</td>
                  <td>{a.firmware || '—'}</td>
                  <td>{a.client_count}</td>
                  <td><StatusPill status={apStatus(a.status)} label={a.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {/* SSIDs. */}
      <Panel title="SSIDs / WLANs" icon={ShieldCheck} subtitle={d?.ssids.length ? `${d.ssids.length}` : undefined} pad={false}>
        {d && d.ssids.length === 0 && (
          <EmptyState icon={ShieldCheck} title="No SSIDs yet"
            message={apiNeeded ? 'SSID/WLAN services come from the controller API. Configure an Extreme XCC profile to collect them.' : 'No SSIDs reported by the last collection.'} />
        )}
        {d && d.ssids.length > 0 && (
          <table className="data-table">
            <thead><tr><th>SSID</th><th>Status</th><th>Security</th><th>Band</th><th>VLAN</th><th>Clients</th></tr></thead>
            <tbody>
              {d.ssids.map((s) => (
                <tr key={s.id}>
                  <td className="cell-name">{s.name}</td>
                  <td><StatusPill status={s.status === 'enabled' ? 'up' : s.status === 'disabled' ? 'down' : 'unknown'} label={s.status} /></td>
                  <td>{s.security || '—'}</td>
                  <td>{s.band || '—'}</td>
                  <td>{s.vlan || '—'}</td>
                  <td>{s.client_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {/* Clients. */}
      <Panel title="Clients" icon={Users} subtitle={d?.clients.length ? `${d.clients.length}` : undefined} pad={false}>
        {d && d.clients.length === 0 && (
          <EmptyState icon={Users} title="No client roster"
            message={apiNeeded ? 'Associated clients (MAC/IP/AP/SSID/RSSI) require the controller API. Some firmware/APIs do not expose clients — that will be shown honestly after collection.' : 'No clients reported by the last collection.'} />
        )}
        {d && d.clients.length > 0 && (
          <table className="data-table">
            <thead><tr><th>MAC</th><th>IP</th><th>Hostname</th><th>AP</th><th>SSID</th><th>RSSI</th><th>Band</th></tr></thead>
            <tbody>
              {d.clients.slice(0, 200).map((cl) => (
                <tr key={cl.id}>
                  <td className="mono">{cl.mac}</td>
                  <td className="mono">{cl.ip || '—'}</td>
                  <td>{cl.hostname || '—'}</td>
                  <td>{cl.ap_name || '—'}</td>
                  <td>{cl.ssid || '—'}</td>
                  <td>{cl.rssi != null ? `${cl.rssi} dBm` : '—'}</td>
                  <td>{cl.band || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {/* Radios (only when present). */}
      {d && d.radios.length > 0 && (
        <Panel title="Radios" icon={Radio} subtitle={`${d.radios.length}`} pad={false}>
          <table className="data-table">
            <thead><tr><th>AP</th><th>Radio</th><th>Band</th><th>Channel</th><th>Power</th><th>Clients</th></tr></thead>
            <tbody>
              {d.radios.map((rd) => (
                <tr key={rd.id}>
                  <td className="cell-name">{rd.ap_name}</td>
                  <td>{rd.radio || '—'}</td>
                  <td>{rd.band || '—'}</td>
                  <td>{rd.channel ?? '—'}</td>
                  <td>{rd.power_dbm != null ? `${rd.power_dbm} dBm` : '—'}</td>
                  <td>{rd.client_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}

      {/* Events / alarms. */}
      <Panel title="Recent Events / Alarms" icon={AlertTriangle} subtitle={d?.events.length ? `${d.events.length}` : undefined} pad={false}>
        {d && d.events.length === 0 && (
          <EmptyState icon={AlertTriangle} title="No events" message={apiNeeded ? 'Controller events/alarms come from the API once a profile is configured.' : 'No recent events reported.'} />
        )}
        {d && d.events.length > 0 && (
          <table className="data-table">
            <thead><tr><th>When</th><th>Severity</th><th>Category</th><th>Message</th></tr></thead>
            <tbody>
              {d.events.map((e) => (
                <tr key={e.id}>
                  <td>{new Date(e.at).toLocaleString()}</td>
                  <td><StatusPill status={sevTone(e.severity)} label={e.severity} /></td>
                  <td>{e.category || '—'}</td>
                  <td>{e.message || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}

// mappedSummary describes what the SNMP MIB source actually populated (honest:
// names the rosters that this firmware did not expose).
function mappedSummary(d: WirelessDetailResp): string {
  const mib = (xs: { source?: string }[]) => xs.filter((x) => x.source === 'snmp_wireless_mib').length
  const aps = mib(d.aps), ssids = mib(d.ssids), clients = mib(d.clients)
  const parts: string[] = []
  if (clients) parts.push(`${clients} clients`)
  if (aps) parts.push(`${aps} APs`)
  if (ssids) parts.push(`${ssids} SSIDs`)
  if (parts.length === 0) return 'nothing yet — AP/SSID/client tables not exposed by this firmware on the mapped OIDs'
  return parts.join(', ') + ' (source: SNMP MIB)'
}

// mapLink builds a deep link into MIB Management's mapping editor, prefilled
// from an explorer subtree (derives the table root from the column OID).
function mapLink(packId: string | undefined, g: { column_oid: string; table: string; purpose?: string }): string {
  if (!packId) return '/mibs'
  const m = g.column_oid.match(/^(.*)\.1\.\d+$/) // <tableRoot>.1.<col>
  const root = m ? m[1] : g.column_oid
  const params = new URLSearchParams({ pack: packId, root, table: g.table })
  if (g.purpose) params.set('purpose', g.purpose)
  return `/mibs?${params.toString()}`
}

// collectionSourcesLine summarizes all four collection paths at a glance.
function collectionSourcesLine(d: WirelessDetailResp): string {
  const identity = (d.identity.sysobjectid || d.identity.sysdescr) ? 'collected' : '—'
  let mib = 'no pack'
  if (d.mib.has_pack) {
    const withRows = d.mib.walked_tables.filter((t) => t.rows > 0).length
    mib = withRows > 0 ? 'applied (no AP roster)' : 'applied (no rows)'
  }
  const api = d.collection.has_api_profile ? `configured${d.collection.profile_status ? ` (${d.collection.profile_status})` : ''}` : 'not configured'
  return `SNMP Identity: ${identity} · SNMP MIB: ${mib} · Extreme XCC API: ${api} · SSH CLI: ${sshStatusLabel(d.ssh.status)}`
}

function sshStatusLabel(s: string): string {
  switch (s) {
    case 'collected': return 'collected'
    case 'partial': return 'partial'
    case 'unsupported': return 'unsupported (restricted CLI)'
    case 'failed': return 'failed'
    default: return 'not run'
  }
}

function sshTone(s: string): 'up' | 'down' | 'warning' | 'info' {
  switch (s) {
    case 'parsed': return 'up'
    case 'unsupported': return 'warning'
    case 'failed': case 'timeout': return 'down'
    default: return 'info'
  }
}

function collLabel(s: string): string {
  switch (s) {
    case 'extreme_xcc_api': return 'Extreme XCC API'
    case 'cloud_xiq': return 'ExtremeCloud IQ (cloud)'
    case 'snmp_baseline': return 'SNMP baseline (identity only)'
    case 'snmp_wireless_mib': return 'SNMP Wireless MIB'
    case 'unifi': case 'omada': case 'ruckus': return s
    default: return s || '—'
  }
}
