import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Play } from 'lucide-react'
import { api, type TrustAuditRow, type TrustActionResult } from '../api'
import { ACTION_LABEL, ACTION_CONFIRM } from '../trustPatterns'

const STATUS_CLS: Record<string, string> = {
  collected: 'badge-up', queued: 'badge-warn', needs_credential: 'badge-warn',
  failed: 'badge-crit', unsupported: 'badge-unknown',
}

// TrustActionButton — a single SAFE guided action: confirm → one-shot POST → honest result inline.
// Operator-triggered; disabled while running (the backend also guards against duplicate runs).
export function TrustActionButton({ row }: { row: TrustAuditRow }) {
  const qc = useQueryClient()
  const [result, setResult] = useState<TrustActionResult | null>(null)
  const m = useMutation({
    mutationFn: () => api.post<TrustActionResult>('/discovery/trust-action', { device_id: row.device_id, action: row.action }),
    onSuccess: (r) => { setResult(r); qc.invalidateQueries({ queryKey: ['trust-audit'] }); qc.invalidateQueries({ queryKey: ['action-center'] }) },
    onError: (e: unknown) => setResult({ action: row.action!, status: 'failed', detail: e instanceof Error ? e.message : String(e), at: '' }),
  })
  if (!row.action) return <span className="muted">—</span>
  const shown = result || (row.last_result ? { status: row.last_result.split(':')[0], detail: row.last_result } as TrustActionResult : null)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <button className="btn btn-xs" disabled={m.isPending}
        onClick={() => { if (window.confirm(ACTION_CONFIRM[row.action!] || 'Run this action?')) m.mutate() }}>
        <Play size={11} /> {m.isPending ? 'Running…' : (ACTION_LABEL[row.action] || row.action)}
      </button>
      {shown && <span className={'badge ' + (STATUS_CLS[shown.status] || 'badge-unknown')} style={{ fontSize: 10 }} title={shown.detail}>{shown.status}</span>}
    </div>
  )
}

// TrustGapTable — per-device actionable rows with evidence/expected/missing + the guided action.
// Shared by the Trust Audit page and the Action Center "Discovery trust gaps" panel.
export function TrustGapTable({ rows }: { rows: TrustAuditRow[] }) {
  if (rows.length === 0) return null
  return (
    <table className="data-table">
      <thead><tr><th>Device</th><th>Pattern</th><th>Evidence</th><th>Missing</th><th>Last result</th><th>Action</th></tr></thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.device_id}>
            <td className="cell-name"><Link to={`/devices/${r.device_id}`}>{r.ip}</Link><br /><small className="muted">{r.category}</small></td>
            <td style={{ fontSize: 12 }}>{(r.pattern || '').replace(/_/g, ' ')}</td>
            <td><span style={{ display: 'inline-flex', gap: 3, flexWrap: 'wrap' }}>{r.evidence.slice(0, 3).map((e) => <span key={e} className="badge badge-up" style={{ fontSize: 10 }}>{e}</span>)}</span></td>
            <td><span style={{ display: 'inline-flex', gap: 3 }}>{(r.missing_attempt || []).map((mm) => <span key={mm} className="badge badge-crit" style={{ fontSize: 10 }}>{mm}</span>)}</span></td>
            <td className="muted" style={{ fontSize: 11 }}>{r.last_result || '—'}</td>
            <td><TrustActionButton row={r} /></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
