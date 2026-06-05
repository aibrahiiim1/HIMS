import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { KeyRound, ShieldCheck, AlertTriangle } from 'lucide-react'
import { api, type AccessCoverage } from '../api'
import { Panel, EmptyState } from './ui'

const REASON_LABEL: Record<string, string> = {
  no_credential_bound: 'No credential bound',
  credential_failed: 'Credential failed',
  not_tested: 'Not tested',
}
const reasonLabel = (r: string) => REASON_LABEL[r] ?? r.replace(/_/g, ' ')

// ManagementAccessCoverage shows how many devices HIMS can actually manage and
// by which protocol — from real credential bindings + authenticated-collection
// evidence (never open ports). Every number is a routed Link into the Inventory
// filtered view, so the drill-down is shareable/bookmarkable.
export function ManagementAccessCoverage() {
  const q = useQuery({
    queryKey: ['access-coverage'],
    queryFn: () => api.get<AccessCoverage>('/dashboard/access-coverage'),
    refetchInterval: 60_000,
    retry: 0,
  })
  const d = q.data
  const maxCount = Math.max(1, ...(d?.by_protocol ?? []).map((p) => p.device_count))

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><ShieldCheck size={15} /> Management Access Coverage</span>}
      actions={
        <span style={{ display: 'inline-flex', gap: 6 }}>
          <Link className="btn btn-ghost btn-xs" to="/credentials"><KeyRound size={12} /> Credentials</Link>
          <Link className="btn btn-ghost btn-xs" to="/data-quality"><AlertTriangle size={12} /> Data Quality</Link>
        </span>
      }
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.error && <p className="error-msg">{(q.error as Error).message}</p>}
      {d && d.total_devices === 0 && (
        <EmptyState icon={ShieldCheck} title="No devices yet" message="Run a discovery scan to populate the fleet, then bind credentials to manage devices." />
      )}
      {d && d.total_devices > 0 && (
        <>
          {/* Headline: managed / total + coverage %, both routed. */}
          <div className="row" style={{ alignItems: 'center', justifyContent: 'space-between', gap: 16, marginBottom: 14 }}>
            <Link to="/inventory?access=managed" className="cell-name" style={{ textDecoration: 'none' }}>
              <div style={{ fontSize: 26, fontWeight: 700, lineHeight: 1 }}>{d.managed_devices}<span className="muted" style={{ fontSize: 15, fontWeight: 400 }}> / {d.total_devices}</span></div>
              <div className="muted" style={{ fontSize: 12 }}>managed devices</div>
            </Link>
            <div style={{ textAlign: 'right' }}>
              <div style={{ fontSize: 26, fontWeight: 700, lineHeight: 1, color: d.coverage_percent >= 75 ? 'var(--ok)' : d.coverage_percent >= 40 ? 'var(--warn)' : 'var(--crit)' }}>{d.coverage_percent}%</div>
              <div className="muted" style={{ fontSize: 12 }}>coverage</div>
            </div>
          </div>
          {/* Coverage bar */}
          <div style={{ height: 6, borderRadius: 4, background: 'var(--surface-2)', overflow: 'hidden', marginBottom: 16 }}>
            <div style={{ width: `${d.coverage_percent}%`, height: '100%', background: 'var(--brand)' }} />
          </div>

          {/* Protocol breakdown — each row routes to the filtered Inventory. */}
          {d.by_protocol.length === 0 ? (
            <p className="muted" style={{ fontSize: 13 }}>No working management methods detected yet. Bind credentials or run an authenticated collection.</p>
          ) : (
            <div style={{ display: 'grid', gap: 6 }}>
              {d.by_protocol.map((p) => (
                <Link key={p.protocol} to={`/inventory?accessProtocol=${encodeURIComponent(p.protocol)}`}
                  className="access-row" style={{ display: 'grid', gridTemplateColumns: '130px 1fr 40px', alignItems: 'center', gap: 10, textDecoration: 'none', color: 'inherit', padding: '2px 0' }}>
                  <span style={{ fontSize: 13 }}>{p.label}</span>
                  <span style={{ height: 8, borderRadius: 4, background: 'var(--surface-2)', overflow: 'hidden' }}>
                    <span style={{ display: 'block', width: `${Math.round((p.device_count / maxCount) * 100)}%`, height: '100%', background: 'var(--brand)' }} />
                  </span>
                  <span style={{ textAlign: 'right', fontWeight: 600, fontSize: 13 }}>{p.device_count}</span>
                </Link>
              ))}
            </div>
          )}

          {/* Unmanaged summary + reasons. */}
          <div style={{ marginTop: 16, paddingTop: 12, borderTop: '1px solid var(--border)' }}>
            <Link to="/inventory?access=unmanaged" className="cell-name" style={{ textDecoration: 'none', display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <span className={`badge ${d.unmanaged_devices > 0 ? 'badge-warning' : 'badge-up'}`}>{d.unmanaged_devices}</span>
              <span style={{ fontSize: 13 }}>device(s) need credentials</span>
            </Link>
            {d.unmanaged.reasons.length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
                {d.unmanaged.reasons.map((r) => (
                  <Link key={r.reason} to={`/inventory?accessIssue=${encodeURIComponent(r.reason)}`} className="badge badge-unknown" style={{ textDecoration: 'none' }}>
                    {reasonLabel(r.reason)}: {r.count}
                  </Link>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </Panel>
  )
}
