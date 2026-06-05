import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { History, ChevronRight } from 'lucide-react'
import { api, type CredTestRun, type CredTestHistory } from '../api'
import { Panel, EmptyState, timeAgo } from './ui'

const PROTO_LABEL: Record<string, string> = {
  snmp_v2c: 'SNMP v2c', snmp_v3: 'SNMP v3', ssh: 'SSH', winrm: 'WinRM', onvif: 'ONVIF',
  http_basic: 'HTTP Basic', vendor_api: 'Vendor API', ldap: 'LDAP',
}
const label = (k: string) => PROTO_LABEL[k] ?? k

function ResultBadge({ h }: { h: CredTestHistory }) {
  return <span className={`badge badge-${h.success ? 'up' : h.category === 'auth_failed' ? 'down' : 'unknown'}`}>{h.success ? 'success' : h.category}</span>
}

// CredentialRunsPanel is the "Runs / History" view for the Credential Testing
// page: every saved test run with its per-pair results. Durable history, no
// secrets.
export function CredentialRunsPanel() {
  const runs = useQuery({ queryKey: ['cred-test-runs'], queryFn: () => api.get<CredTestRun[]>('/credential-tests/runs?limit=50') })
  const [open, setOpen] = useState<string | null>(null)
  const list = runs.data ?? []

  return (
    <Panel title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><History size={15} /> Test Runs / History</span>} subtitle={list.length ? `${list.length}` : undefined} pad={false}>
      {runs.isLoading && <div className="loading">Loading…</div>}
      {runs.data && list.length === 0 && (
        <EmptyState icon={History} title="No test runs yet" message="Credential tests you run are saved here with their full per-device results." />
      )}
      {list.length > 0 && (
        <table className="data-table">
          <thead><tr><th></th><th>When</th><th>By</th><th>Pairs</th><th>Working</th><th>Failed</th></tr></thead>
          <tbody>
            {list.map((run) => (
              <RunRow key={run.id} run={run} open={open === run.id} onToggle={() => setOpen(open === run.id ? null : run.id)} />
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

function RunRow({ run, open, onToggle }: { run: CredTestRun; open: boolean; onToggle: () => void }) {
  const results = useQuery({
    queryKey: ['cred-test-run-results', run.id],
    queryFn: () => api.get<CredTestHistory[]>(`/credential-tests/runs/${run.id}/results`),
    enabled: open,
  })
  return (
    <>
      <tr style={{ cursor: 'pointer' }} onClick={onToggle}>
        <td><ChevronRight size={13} style={{ transform: open ? 'rotate(90deg)' : 'none', transition: 'transform .12s' }} /></td>
        <td className="muted" title={run.started_at}>{timeAgo(run.started_at)}</td>
        <td>{run.actor || '—'}</td>
        <td>{run.pairs}</td>
        <td><span className="badge badge-up">{run.successes}</span></td>
        <td>{run.failures > 0 ? <span className="badge badge-down">{run.failures}</span> : '0'}</td>
      </tr>
      {open && (
        <tr>
          <td colSpan={6} style={{ background: 'var(--surface-2)' }}>
            {results.isLoading ? <div className="loading">Loading…</div> : (
              <table className="data-table" style={{ margin: 0 }}>
                <thead><tr><th>Device</th><th>Protocol</th><th>Credential</th><th>Result</th><th>Detail</th></tr></thead>
                <tbody>
                  {(results.data ?? []).map((h) => (
                    <tr key={h.id}>
                      <td className="mono" style={{ fontSize: 12 }}>{h.device_id.slice(0, 8)}</td>
                      <td>{label(h.kind)}</td>
                      <td>{h.credential_name || '—'}</td>
                      <td><ResultBadge h={h} /></td>
                      <td className="muted" style={{ fontSize: 12 }}>{h.detail}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </td>
        </tr>
      )}
    </>
  )
}

// CredentialHistoryPanel shows the saved test history for ONE credential
// (per-credential latest status + trail). Used from the Credentials list.
export function CredentialHistoryPanel({ credentialId, credentialName }: { credentialId: string; credentialName: string }) {
  const q = useQuery({
    queryKey: ['cred-history', credentialId],
    queryFn: () => api.get<CredTestHistory[]>(`/credentials/${credentialId}/credential-tests?limit=100`),
  })
  const list = q.data ?? []
  return (
    <Panel title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><History size={15} /> Test history — {credentialName}</span>} pad={false}>
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.data && list.length === 0 && (
        <EmptyState icon={History} title="Not tested yet" message="This credential has not been tested against any device yet." />
      )}
      {list.length > 0 && (
        <table className="data-table">
          <thead><tr><th>When</th><th>Device</th><th>Protocol</th><th>Result</th><th>Detail</th><th>By</th></tr></thead>
          <tbody>
            {list.map((h) => (
              <tr key={h.id}>
                <td className="muted" title={h.tested_at}>{timeAgo(h.tested_at)}</td>
                <td className="cell-name">{h.device_name || h.device_id.slice(0, 8)}</td>
                <td>{label(h.kind)}</td>
                <td><ResultBadge h={h} /></td>
                <td className="muted" style={{ fontSize: 12 }}>{h.detail}</td>
                <td className="muted">{h.actor || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
