import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ShieldCheck, AlertTriangle, RefreshCw } from 'lucide-react'
import { api, type TrustAuditReport } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'
import { PATTERN_LABEL } from '../trustPatterns'

// Discovery Trust Audit — device-class-agnostic. For every weak / shallowly-managed device it shows
// evidence → expected collectors → attempted → missing, whether a weaker protocol won while a
// stronger was skipped, the corrected action, and the honest final state. Data is from
// /discovery/trust-audit which actively re-probes weak hosts (read-only fingerprint).
const Chips = ({ items, tone }: { items: string[] | null; tone?: string }) => (
  <span style={{ display: 'inline-flex', gap: 3, flexWrap: 'wrap' }}>
    {(items ?? []).map((x) => <span key={x} className={'badge ' + (tone || 'badge-unknown')} style={{ fontSize: 10 }}>{x}</span>)}
    {(!items || items.length === 0) && <span className="muted">—</span>}
  </span>
)

export function TrustAudit() {
  const q = useQuery({ queryKey: ['trust-audit'], queryFn: () => api.get<TrustAuditReport>('/discovery/trust-audit'), staleTime: 60_000 })
  const [patF, setPatF] = useState('')
  const [onlyWeakerWon, setOnlyWeakerWon] = useState(false)
  const d = q.data

  const rows = useMemo(() => (d?.devices ?? []).filter((r) => {
    if (patF && r.pattern !== patF) return false
    if (onlyWeakerWon && !r.weaker_won) return false
    return true
  }), [d, patF, onlyWeakerWon])
  const weakerWon = (d?.devices ?? []).filter((r) => r.weaker_won).length

  return (
    <div>
      <PageHeader title="Discovery Trust Audit" icon={ShieldCheck} subtitle="Evidence → expected collectors → attempted → missing, for every weak/shallow device, across all device types"
        actions={<button className="btn btn-sm" disabled={q.isFetching} onClick={() => q.refetch()}><RefreshCw size={14} /> {q.isFetching ? 'Probing…' : 'Re-run audit'}</button>} />
      {q.isLoading && <div className="loading">Running fleet audit (active re-probe of weak hosts)…</div>}
      {d && (
        <>
          <div className="kpi-grid">
            <Kpi label="Total devices" value={d.total_devices} icon={ShieldCheck} tone="info" />
            <Kpi label="Weak / shallow" value={d.weak_devices} icon={AlertTriangle} tone={d.weak_devices ? 'warn' : 'ok'} />
            <Kpi label="Weaker won (strong skipped)" value={weakerWon} icon={AlertTriangle} tone={weakerWon ? 'crit' : 'ok'} />
            <Kpi label="Active probe" value={d.probed ? 'on' : 'off'} sub="read-only fingerprint" />
          </div>

          <Panel title="Patterns" subtitle={`${d.patterns.length}`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Pattern</th><th>Count</th><th>Device types</th><th>Examples</th></tr></thead>
              <tbody>
                {d.patterns.map((p) => (
                  <tr key={p.pattern} style={{ cursor: 'pointer', background: patF === p.pattern ? 'var(--surface-2)' : undefined }} onClick={() => setPatF(patF === p.pattern ? '' : p.pattern)}>
                    <td className="cell-name">{PATTERN_LABEL[p.pattern] ?? p.pattern}</td>
                    <td>{p.count}</td>
                    <td className="muted">{p.device_types.join(', ')}</td>
                    <td className="muted" style={{ fontSize: 12 }}>{p.examples.join(', ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>

          <Panel title="Per-device audit" subtitle={`${rows.length}`} pad={false}
            actions={
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <select className="field" style={{ fontSize: 12 }} value={patF} onChange={(e) => setPatF(e.target.value)}>
                  <option value="">All patterns</option>
                  {d.patterns.map((p) => <option key={p.pattern} value={p.pattern}>{PATTERN_LABEL[p.pattern] ?? p.pattern}</option>)}
                </select>
                <label style={{ fontSize: 12, display: 'flex', gap: 4, alignItems: 'center' }}><input type="checkbox" checked={onlyWeakerWon} onChange={(e) => setOnlyWeakerWon(e.target.checked)} /> weaker-won only</label>
              </div>
            }>
            {rows.length === 0 && <EmptyState icon={ShieldCheck} title="Nothing to audit" message="No weak/shallow devices match — discovery is complete here." />}
            {rows.length > 0 && (
              <table className="data-table">
                <thead><tr><th>IP</th><th>Category</th><th>State</th><th>Evidence</th><th>Expected</th><th>Attempted</th><th>Missing</th><th>Corrected action</th><th>Honest state</th></tr></thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.device_id} style={r.weaker_won ? { background: 'rgba(220,80,80,0.08)' } : undefined}>
                      <td className="cell-name"><Link to={`/devices/${r.device_id}`}>{r.ip}</Link>{r.weaker_won && <span className="badge badge-crit" style={{ marginLeft: 4, fontSize: 9 }}>weaker won</span>}</td>
                      <td>{r.category}</td>
                      <td className="muted">{r.state.replace(/_/g, ' ')}</td>
                      <td><Chips items={r.evidence} tone="badge-up" /></td>
                      <td><Chips items={r.expected_collectors} /></td>
                      <td><Chips items={r.succeeded_collectors} tone="badge-up" /></td>
                      <td><Chips items={r.missing_attempt} tone="badge-crit" /></td>
                      <td className="muted" style={{ fontSize: 12, maxWidth: 260 }}>{r.corrected_action}</td>
                      <td className="muted" style={{ fontSize: 11, maxWidth: 220 }}>{r.final_honest_state}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </>
      )}
    </div>
  )
}
