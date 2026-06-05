import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, RefreshCw, Link2 } from 'lucide-react'
import { api, type CredTestHistory, type AuthMe } from '../api'
import { Panel, EmptyState, timeAgo } from './ui'

const PROTO_LABEL: Record<string, string> = {
  snmp_v2c: 'SNMP v2c', snmp_v3: 'SNMP v3', ssh: 'SSH', winrm: 'WinRM', onvif: 'ONVIF',
  http_basic: 'HTTP Basic', vendor_api: 'Vendor API', ldap: 'LDAP',
}
const label = (k: string) => PROTO_LABEL[k] ?? k

// DeviceCredentialHealth is the "Access Methods / Credential Health" section on a
// device's detail page. It shows, per protocol/credential-kind, the LATEST saved
// credential-test outcome (success/failure + when + why), plus recent history.
// Operators can Apply a working credential (bind it to the device) or Retry a
// failed test — both gated to devices.write. Never shows secrets.
export function DeviceCredentialHealth({ deviceId }: { deviceId: string }) {
  const qc = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me') })
  const q = useQuery({
    queryKey: ['device-cred-tests', deviceId],
    queryFn: () => api.get<CredTestHistory[]>(`/devices/${deviceId}/credential-tests?limit=100`),
  })
  const canWrite = !!(me.data?.admin || me.data?.permissions?.includes('devices.write'))
  const history = useMemo(() => q.data ?? [], [q.data])

  // Latest result per credential-kind = the current credential health per protocol.
  const latestByKind = useMemo(() => {
    const m = new Map<string, CredTestHistory>()
    for (const h of history) { // history is newest-first
      if (!m.has(h.kind)) m.set(h.kind, h)
    }
    return [...m.values()].sort((a, b) => a.kind.localeCompare(b.kind))
  }, [history])

  const bind = useMutation({
    mutationFn: (credentialId: string) => api.put(`/devices/${deviceId}/credential`, { credential_id: credentialId }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['devices', 'all'] }); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
  })
  const retry = useMutation({
    mutationFn: (v: { credentialId: string }) => api.post('/credentials/test', { credential_ids: [v.credentialId], device_ids: [deviceId] }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['device-cred-tests', deviceId] }); qc.invalidateQueries({ queryKey: ['access-coverage'] }) },
  })

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><KeyRound size={15} /> Access Methods / Credential Health</span>}
      subtitle={history.length ? `${latestByKind.length} protocol(s) tested` : undefined}
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.data && history.length === 0 && (
        <EmptyState icon={KeyRound} title="Not tested yet" message="No credential test has been saved for this device. Use the Credentials page (or the device's operator actions) to test a credential — the result will appear here." />
      )}

      {latestByKind.length > 0 && (
        <table className="data-table">
          <thead><tr><th>Protocol</th><th>Status</th><th>Credential</th><th>Last tested</th><th>Detail</th><th></th></tr></thead>
          <tbody>
            {latestByKind.map((h) => (
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
                  {canWrite && !h.success && h.credential_id && (
                    <button className="btn btn-ghost btn-xs" disabled={retry.isPending} title="Re-run this credential test" onClick={() => retry.mutate({ credentialId: h.credential_id! })}>
                      <RefreshCw size={12} /> Retry
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {bind.isSuccess && <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Credential bound ✓</p>}
      {(bind.error || retry.error) && <p className="error-msg" style={{ marginTop: 8 }}>{((bind.error || retry.error) as Error).message}</p>}

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
