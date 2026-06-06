import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, RefreshCw, Link2, Globe, Wrench } from 'lucide-react'
import { api, type CredTestHistory, type AuthMe, type Credential, type CredentialGroup, type Device, type Location, type WinRMDiag, locationPaths } from '../api'
import { Panel, EmptyState, timeAgo } from './ui'

const PROTO_LABEL: Record<string, string> = {
  snmp_v2c: 'SNMP v2c', snmp_v3: 'SNMP v3', ssh: 'SSH', winrm: 'WinRM', wmi: 'WMI / DCOM', onvif: 'ONVIF',
  http_basic: 'HTTP Basic', vendor_api: 'Vendor API', ldap: 'LDAP',
}
const label = (k: string) => PROTO_LABEL[k] ?? k

// expectedKindsFor mirrors the backend protocol plan: the credential kind(s) a
// device of this category is normally managed by. Used to show the "Expected
// access method" first and collapse irrelevant attempts (so a Windows workstation
// shows WinRM as its health, not a scary SNMP/ONVIF/SSH failure).
function expectedKindsFor(category?: string, osFamily?: string): string[] {
  if (osFamily === 'windows') return ['winrm']
  if (osFamily === 'linux') return ['ssh']
  switch (category) {
    case 'endpoint': case 'workstation': return ['winrm', 'wmi']
    case 'server': return ['winrm', 'ssh', 'wmi']
    case 'switch': case 'router': case 'firewall': return ['snmp_v2c', 'ssh']
    case 'camera': case 'nvr': return ['onvif', 'http_basic']
    case 'virtual_host': return ['vmware', 'vendor_api']
    case 'printer': case 'ups': return ['snmp_v2c']
    case 'pbx': case 'voice_gateway': return ['cucm_axl', 'vendor_api']
    default: return []
  }
}
const isExpectedKind = (kind: string, expected: string[]) =>
  expected.includes(kind) || (kind.startsWith('snmp') && expected.some((e) => e.startsWith('snmp')))
// expectedLabel — the primary expected protocol shown in the header.
function expectedLabel(category?: string, osFamily?: string): string {
  const ks = expectedKindsFor(category, osFamily)
  if (ks.length === 0) return 'SNMP / SSH (network) — depends on device type'
  return ks.map(label).join(' or ')
}

// DeviceCredentialHealth is the "Access Methods / Credential Health" section on a
// device's detail page. It shows, per protocol/credential-kind, the LATEST saved
// credential-test outcome (success/failure + when + why), plus recent history.
// Operators can Apply a working credential (bind it to the device) or Retry a
// failed test — both gated to devices.write. Never shows secrets.
export function DeviceCredentialHealth({ deviceId, category, osFamily }: { deviceId: string; category?: string; osFamily?: string }) {
  const qc = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me') })
  const q = useQuery({
    queryKey: ['device-cred-tests', deviceId],
    queryFn: () => api.get<CredTestHistory[]>(`/devices/${deviceId}/credential-tests?limit=100`),
  })
  const canWrite = !!(me.data?.admin || me.data?.permissions?.includes('devices.write'))
  const canManageCreds = !!(me.data?.admin || me.data?.permissions?.includes('credentials.manage'))
  const history = useMemo(() => q.data ?? [], [q.data])
  // Which successful credential the operator is applying to a scope (group/site).
  const [scope, setScope] = useState<{ credentialId: string; credentialName: string } | null>(null)

  // Device list (cached) — for category fallback, the host IP (WinRM diagnose),
  // and to find a WinRM credential to diagnose with.
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const dev = devices.data?.find((d) => d.id === deviceId)
  const effCategory = category ?? dev?.category
  const expected = useMemo(() => expectedKindsFor(effCategory, osFamily), [effCategory, osFamily])

  // WinRM diagnostic (legacy Windows / WSMan-2.0 triage).
  const [diag, setDiag] = useState<WinRMDiag | null>(null)
  const winDiag = useMutation({
    mutationFn: async () => {
      const creds = await api.get<Credential[]>('/credentials')
      const winrm = creds.find((c) => c.kind === 'winrm')
      if (!winrm) throw new Error('No WinRM credential configured — add one on the Credentials page.')
      if (!dev?.primary_ip) throw new Error('Device has no IP to diagnose.')
      return api.post<WinRMDiag>('/credentials/winrm-diagnose', { host: dev.primary_ip, credential_id: winrm.id })
    },
    onSuccess: (d) => setDiag(d),
  })
  const showDiagnose = expected.includes('winrm') || history.some((h) => h.kind === 'winrm') || effCategory === 'endpoint'

  // WMI/DCOM diagnostic (legacy Windows where WinRM is disabled).
  const [wmiMsg, setWmiMsg] = useState('')
  const wmiDiag = useMutation({
    mutationFn: async () => {
      if (!dev?.primary_ip) throw new Error('Device has no IP to diagnose.')
      const creds = await api.get<Credential[]>('/credentials')
      const wmi = creds.find((c) => c.kind === 'wmi') || creds.find((c) => c.kind === 'winrm')
      return api.post<{ dcom_reachable: boolean; dcom_status: string; dcom_detail: string; collector_configured: boolean; collect_result?: string; collect_detail?: string }>(
        '/credentials/wmi-diagnose', { host: dev.primary_ip, credential_id: wmi?.id ?? '' })
    },
    onSuccess: (d) => {
      const parts = [`DCOM(135): ${d.dcom_reachable ? 'reachable' : d.dcom_status}`, `collector ${d.collector_configured ? 'configured' : 'not configured'}`]
      if (d.collect_result) parts.push(`collect=${d.collect_result}`)
      setWmiMsg(parts.join(' · ') + (d.collect_detail ? ` — ${d.collect_detail}` : d.dcom_detail ? ` — ${d.dcom_detail}` : ''))
    },
    onError: (e) => setWmiMsg((e as Error).message),
  })

  // Latest result per credential-kind = the current credential health per protocol.
  const latestByKind = useMemo(() => {
    const m = new Map<string, CredTestHistory>()
    for (const h of history) { // history is newest-first
      if (!m.has(h.kind)) m.set(h.kind, h)
    }
    return [...m.values()].sort((a, b) => a.kind.localeCompare(b.kind))
  }, [history])

  // Split into the EXPECTED access method(s) for this device class vs OTHER
  // attempts (irrelevant protocols probed historically). A result flagged
  // relevant by the scan counts as expected too.
  const expectedRows = useMemo(
    () => latestByKind.filter((h) => isExpectedKind(h.kind, expected) || h.relevant),
    [latestByKind, expected],
  )
  const otherRows = useMemo(
    () => latestByKind.filter((h) => !(isExpectedKind(h.kind, expected) || h.relevant)),
    [latestByKind, expected],
  )

  const bind = useMutation({
    mutationFn: (credentialId: string) => api.put(`/devices/${deviceId}/credential`, { credential_id: credentialId }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['devices', 'all'] }); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
  })
  const retry = useMutation({
    mutationFn: (v: { credentialId: string }) => api.post('/credentials/test', { credential_ids: [v.credentialId], device_ids: [deviceId] }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['device-cred-tests', deviceId] }); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
  })

  const renderRow = (h: CredTestHistory) => (
    <tr key={h.kind}>
      <td>{label(h.kind)}</td>
      <td><span className={`badge badge-${h.success ? 'up' : h.category === 'auth_failed' ? 'down' : 'unknown'}`}>{h.success ? 'working' : h.category}</span></td>
      <td>{h.credential_name || '—'}</td>
      <td className="muted" title={h.tested_at}>{timeAgo(h.tested_at)}</td>
      <td className="muted" style={{ fontSize: 12 }}>{h.detail}{h.latency_ms ? ` · ${h.latency_ms}ms` : ''}</td>
      <td className="cell-actions">
        {canWrite && h.success && h.credential_id && (
          <button className="btn btn-ghost btn-xs" disabled={bind.isPending} title="Bind this working credential to the device" onClick={() => bind.mutate(h.credential_id!)}>
            <Link2 size={12} /> Apply
          </button>
        )}
        {canManageCreds && h.success && h.credential_id && (
          <button className="btn btn-ghost btn-xs" title="Apply this working credential to a credential group / site" onClick={() => setScope({ credentialId: h.credential_id!, credentialName: h.credential_name })}>
            <Globe size={12} /> Apply to scope
          </button>
        )}
        {canWrite && !h.success && h.credential_id && (
          <button className="btn btn-ghost btn-xs" disabled={retry.isPending} title="Re-run this credential test" onClick={() => retry.mutate({ credentialId: h.credential_id! })}>
            <RefreshCw size={12} /> Retry
          </button>
        )}
      </td>
    </tr>
  )
  const tableHead = <thead><tr><th>Protocol</th><th>Status</th><th>Credential</th><th>Last tested</th><th>Detail</th><th></th></tr></thead>

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><KeyRound size={15} /> Access Methods / Credential Health</span>}
      subtitle={history.length ? `${latestByKind.length} protocol(s) tested` : undefined}
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.data && history.length === 0 && (
        <EmptyState icon={KeyRound} title="Not tested yet" message="No credential test has been saved for this device. Use the Credentials page (or the device's operator actions) to test a credential — the result will appear here." />
      )}

      {/* Expected access method — the primary protocol for this device type. */}
      <div style={{ marginBottom: 10, fontSize: 13, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <span><span className="muted">Expected access method: </span><strong>{expectedLabel(effCategory, osFamily)}</strong></span>
        {showDiagnose && canManageCreds && (
          <button className="btn btn-ghost btn-xs" disabled={winDiag.isPending} title="Probe WinRM auth schemes + transport modes (legacy WSMan triage)" onClick={() => winDiag.mutate()}>
            <Wrench size={12} /> {winDiag.isPending ? 'Diagnosing…' : 'Diagnose WinRM'}
          </button>
        )}
        {showDiagnose && canManageCreds && (
          <button className="btn btn-ghost btn-xs" disabled={wmiDiag.isPending} title="Probe DCOM/RPC (135) reachability + WMI auth (legacy Windows fallback)" onClick={() => wmiDiag.mutate()}>
            <Wrench size={12} /> {wmiDiag.isPending ? 'Diagnosing…' : 'Diagnose WMI/DCOM'}
          </button>
        )}
      </div>
      {winDiag.error && <p className="error-msg" style={{ marginTop: 0 }}>{(winDiag.error as Error).message}</p>}
      {wmiMsg && <p className="muted" style={{ marginTop: 0, fontSize: 12 }}>WMI/DCOM — {wmiMsg}</p>}
      {diag && <WinRMDiagPanel d={diag} onClose={() => setDiag(null)} />}

      {expectedRows.length > 0 ? (
        <table className="data-table">{tableHead}<tbody>{expectedRows.map(renderRow)}</tbody></table>
      ) : history.length > 0 ? (
        <div className="muted" style={{ fontSize: 13, padding: '4px 0 8px' }}>
          The expected method ({expectedLabel(effCategory, osFamily)}) has not been tested successfully yet. Test or bind a matching credential.
        </div>
      ) : null}

      {otherRows.length > 0 && (
        <details style={{ marginTop: 10 }}>
          <summary style={{ cursor: 'pointer', fontWeight: 600, fontSize: 13 }}>Other attempts / not applicable ({otherRows.length})</summary>
          <p className="muted" style={{ fontSize: 12, margin: '6px 0' }}>
            Protocols probed historically that are not the expected management method for this device type. Failures here are usually expected and not a real problem.
          </p>
          <table className="data-table">{tableHead}<tbody>{otherRows.map(renderRow)}</tbody></table>
        </details>
      )}

      {bind.isSuccess && <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Credential bound ✓</p>}
      {(bind.error || retry.error) && <p className="error-msg" style={{ marginTop: 8 }}>{((bind.error || retry.error) as Error).message}</p>}

      {scope && (
        <ApplyScopeForm deviceId={deviceId} credentialId={scope.credentialId} credentialName={scope.credentialName} onClose={() => setScope(null)} />
      )}

      {history.length > 0 && (
        <details style={{ marginTop: 12 }}>
          <summary style={{ cursor: 'pointer', fontWeight: 600 }}>Test history ({history.length})</summary>
          <table className="data-table" style={{ marginTop: 8 }}>
            <thead><tr><th>When</th><th>Protocol</th><th>Credential</th><th>Result</th><th>Detail</th><th>By</th></tr></thead>
            <tbody>
              {history.map((h) => (
                <tr key={h.id}>
                  <td className="muted" title={h.tested_at}>{timeAgo(h.tested_at)}</td>
                  <td>{label(h.kind)}</td>
                  <td>{h.credential_name || '—'}</td>
                  <td><span className={`badge badge-${h.success ? 'up' : h.category === 'auth_failed' ? 'down' : 'unknown'}`}>{h.success ? 'success' : h.category}</span></td>
                  <td className="muted" style={{ fontSize: 12 }}>{h.detail}</td>
                  <td className="muted">{h.actor || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
    </Panel>
  )
}

// ApplyScopeForm promotes a verified-working credential from one device to a
// reusable credential group, and optionally binds that group to the device's
// site/location so future discovery + collection across that scope use it.
function ApplyScopeForm({ deviceId, credentialId, credentialName, onClose }: {
  deviceId: string; credentialId: string; credentialName: string; onClose: () => void
}) {
  const qc = useQueryClient()
  const groups = useQuery({ queryKey: ['credential-groups'], queryFn: () => api.get<CredentialGroup[]>('/credential-groups') })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const dev = (devices.data ?? []).find((d) => d.id === deviceId)
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  const [groupSel, setGroupSel] = useState('') // group id, or '' = none chosen, or '__new__'
  const [newName, setNewName] = useState('')
  const [bindSite, setBindSite] = useState(true)

  const apply = useMutation({
    mutationFn: () => api.post<{ group_name: string; location_bound: boolean }>(`/credentials/${credentialId}/apply-to-scope`, {
      group_id: groupSel && groupSel !== '__new__' ? groupSel : undefined,
      new_group_name: groupSel === '__new__' ? newName.trim() : undefined,
      location_id: bindSite && dev?.location_id ? dev.location_id : undefined,
    }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['credential-groups'] }),
  })

  const ready = (groupSel && groupSel !== '__new__') || (groupSel === '__new__' && newName.trim() !== '')

  return (
    <div className="card" style={{ marginTop: 12, background: 'var(--surface-2)' }}>
      <h3 style={{ marginTop: 0, display: 'inline-flex', gap: 8, alignItems: 'center' }}><Globe size={15} /> Apply “{credentialName}” to a credential group / site</h3>
      <p className="muted" style={{ fontSize: 12 }}>
        Adds this working credential to a credential group so HIMS can reuse it. Optionally bind the group to this device's
        site so future discovery + collection across the site try it automatically.
      </p>
      <div className="row" style={{ flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        <select className="field" value={groupSel} onChange={(e) => setGroupSel(e.target.value)}>
          <option value="">— choose group —</option>
          {(groups.data ?? []).map((g) => <option key={g.id} value={g.id}>{g.name} ({g.member_count} cred · {g.binding_count} site)</option>)}
          <option value="__new__">+ New group…</option>
        </select>
        {groupSel === '__new__' && (
          <input className="field" placeholder="new group name" value={newName} onChange={(e) => setNewName(e.target.value)} style={{ minWidth: 200 }} />
        )}
      </div>
      <label className="row" style={{ gap: 8, alignItems: 'center', marginTop: 10, fontSize: 13 }}>
        <input type="checkbox" checked={bindSite} disabled={!dev?.location_id} onChange={(e) => setBindSite(e.target.checked)} />
        {dev?.location_id
          ? <>Also bind this group to the device's site: <strong>{locPath[dev.location_id] ?? '—'}</strong></>
          : <span className="muted">Device has no site assigned — group will be created/updated without a site binding.</span>}
      </label>
      <div className="row" style={{ gap: 8, marginTop: 12 }}>
        <button className="btn btn-primary btn-sm" disabled={!ready || apply.isPending} onClick={() => apply.mutate()}>
          {apply.isPending ? 'Applying…' : 'Apply to scope'}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onClose}>{apply.isSuccess ? 'Close' : 'Cancel'}</button>
      </div>
      {apply.isSuccess && (
        <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
          Added to group <strong>{apply.data.group_name}</strong>{apply.data.location_bound ? ' and bound to the site ✓' : ' ✓'}
        </p>
      )}
      {apply.error && <p className="error-msg" style={{ marginTop: 8 }}>{(apply.error as Error).message}</p>}
    </div>
  )
}

// winrmRecommendation derives the operator-facing recommendation from a WinRM
// diagnostic, mapping the transport-matrix outcome to the four states the
// operator cares about.
function winrmRecommendation(d: WinRMDiag): { tone: 'ok' | 'warn' | 'crit'; state: string; text: string } {
  const enc = d.modes.find((m) => m.mode === 'ntlm-encrypted')
  if (enc?.result === 'success') {
    return { tone: 'ok', state: 'collection-ready', text: 'WinRM authenticated and the WSMan operation succeeded — this host collects via the standard Go WinRM path.' }
  }
  if (enc && (enc.result === 'auth_ok_operation_fault' || (enc.fault_kind === 'soap_fault'))) {
    return {
      tone: 'warn', state: 'legacy_wsman2_incompatible',
      text: `Authentication succeeded, but this Windows host uses an older WSMan stack (fault ${enc.fault_code || 'WSMan'}). Native PowerShell works; the Go WinRM library cannot execute commands here. Use the Windows Native Collector / WMI fallback — do NOT treat the credential as wrong.`,
    }
  }
  const any401 = d.modes.some((m) => m.fault_kind === 'http_401')
  if (any401 && !d.www_authenticate.some((s) => /ntlm|negotiate/i.test(s))) {
    return { tone: 'crit', state: 'auth_failed', text: 'The listener rejected the credential (HTTP 401). Check the username format (DOMAIN\\user vs UPN) and password, or the accepted auth schemes.' }
  }
  if (any401) {
    return { tone: 'crit', state: 'auth_failed', text: 'HTTP 401 — credential rejected. Verify username/password and that the account may log on via WinRM.' }
  }
  return { tone: 'warn', state: 'unreachable', text: 'Could not complete a WinRM exchange (unreachable/timeout). Confirm WinRM is enabled and 5985/5986 is open.' }
}

// WinRMDiagPanel renders the WinRM diagnostic: advertised auth schemes, the
// transport-matrix outcome, the SOAP fault (if any), and the exact recommendation.
function WinRMDiagPanel({ d, onClose }: { d: WinRMDiag; onClose: () => void }) {
  const rec = winrmRecommendation(d)
  const toneColor = { ok: '#2e7d32', warn: '#8a6d00', crit: '#b71c1c' }[rec.tone]
  const modeBadge = (r: string) =>
    r === 'success' ? 'badge-up'
      : r === 'auth_ok_operation_fault' ? 'badge-warning'
        : r === 'auth_failed' ? 'badge-down' : 'badge-unknown'
  return (
    <div className="card" style={{ marginTop: 10, borderLeft: `4px solid ${toneColor}`, background: 'var(--surface-2)' }}>
      <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
        <strong style={{ fontSize: 13 }}>WinRM diagnostic — {d.host}</strong>
        <button className="btn btn-ghost btn-xs" onClick={onClose}>Close</button>
      </div>
      <div style={{ fontSize: 12, marginTop: 6 }}>
        <div><span className="muted">Endpoint: </span><span className="mono">{d.endpoint}</span></div>
        <div><span className="muted">Auth schemes advertised: </span><strong>{d.www_authenticate.join(', ') || '—'}</strong></div>
        <div><span className="muted">Username parsed: </span>domain=<strong>{d.parsed_domain || '(none)'}</strong> user=<strong>{d.parsed_user}</strong>{!d.domain_sent_in_ntlm && ' · NTLM sends no domain (UPN)'}</div>
      </div>
      <table className="data-table" style={{ marginTop: 8 }}>
        <thead><tr><th>Mode</th><th>Auth</th><th>Result</th><th>Fault</th><th>Latency</th></tr></thead>
        <tbody>
          {d.modes.map((m) => (
            <tr key={m.mode}>
              <td>{m.mode}{m.encryption ? ' (encrypted)' : ''}</td>
              <td>{m.auth_method}</td>
              <td><span className={`badge ${modeBadge(m.result)}`}>{m.result}</span></td>
              <td className="muted" style={{ fontSize: 12 }}>{m.fault_code || m.error_type || '—'}{m.fault_detail ? ` · ${m.fault_detail}` : ''}</td>
              <td className="muted">{m.latency_ms}ms</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div style={{ marginTop: 8, fontSize: 13 }}>
        <span className="badge" style={{ background: toneColor, color: '#fff' }}>{rec.state}</span>
        <div style={{ marginTop: 4 }}>{rec.text}</div>
      </div>
    </div>
  )
}
