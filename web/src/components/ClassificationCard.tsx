import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ScanSearch, Lock, LockOpen, RefreshCw } from 'lucide-react'
import { api, type Classification, type ReclassifyResponse } from '../api'
import { Panel, EmptyState } from './ui'

const OS_LABEL: Record<string, string> = {
  windows: 'Windows', linux: 'Linux', network_os: 'Network OS', embedded: 'Embedded', macos: 'macOS',
}

function confTone(c: number | null): string {
  if (c == null) return 'badge-unknown'
  if (c >= 75) return 'badge-up'
  if (c >= 50) return 'badge-warning'
  return 'badge-down'
}

// ClassificationCard shows a device's evidence-based classification (category,
// OS family, confidence) + the evidence trail, with a re-classify (live probe)
// action and a manual-override lock toggle. Backed by /devices/{id}/classification.
export function ClassificationCard({ deviceId }: { deviceId: string }) {
  const qc = useQueryClient()
  const q = useQuery({
    queryKey: ['classification', deviceId],
    queryFn: () => api.get<Classification>(`/devices/${deviceId}/classification`),
  })
  const invalidate = () => qc.invalidateQueries({ queryKey: ['classification', deviceId] })

  const reclassify = useMutation({
    mutationFn: () => api.post<ReclassifyResponse>(`/devices/${deviceId}/reclassify`, {}),
    onSuccess: invalidate,
  })
  const lock = useMutation({
    mutationFn: (locked: boolean) => api.post<Classification>(`/devices/${deviceId}/classification-lock`, { locked }),
    onSuccess: invalidate,
  })

  const c = q.data
  const locked = !!c?.classification_locked

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><ScanSearch size={15} /> Classification</span>}
      actions={
        <span style={{ display: 'inline-flex', gap: 6 }}>
          <button className="btn btn-xs" disabled={reclassify.isPending || locked} title={locked ? 'Unlock to re-classify' : 'Probe the device and re-run the classifier'} onClick={() => reclassify.mutate()}>
            <RefreshCw size={13} /> {reclassify.isPending ? 'Probing…' : 'Re-classify'}
          </button>
          <button className="btn btn-xs btn-ghost" disabled={lock.isPending} onClick={() => lock.mutate(!locked)}>
            {locked ? <><LockOpen size={13} /> Unlock</> : <><Lock size={13} /> Lock</>}
          </button>
        </span>
      }
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {c && (
        <>
          <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', alignItems: 'center', marginBottom: 10 }}>
            <div><div className="muted" style={{ fontSize: 11 }}>Category</div><span className="badge">{c.category.replace(/_/g, ' ')}</span></div>
            <div><div className="muted" style={{ fontSize: 11 }}>OS family</div><strong>{c.os_family ? (OS_LABEL[c.os_family] ?? c.os_family) : '—'}</strong></div>
            <div><div className="muted" style={{ fontSize: 11 }}>Subtype</div><strong>{c.device_class || '—'}</strong></div>
            <div><div className="muted" style={{ fontSize: 11 }}>Confidence</div><span className={`badge ${confTone(c.confidence_score)}`}>{c.confidence_score == null ? 'unscored' : `${c.confidence_score}%`}</span></div>
            {locked && <span className="badge badge-warning" title="Manual override — auto-classification won't change this"><Lock size={11} style={{ verticalAlign: -1 }} /> locked</span>}
          </div>

          {reclassify.data && !reclassify.data.changed && (
            <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>{reclassify.data.message || 'No change.'}</p>
          )}

          {c.evidence.length === 0 ? (
            <EmptyState icon={ScanSearch} title="No classification evidence yet" message="Re-classify to probe the device and build an evidence trail." />
          ) : (
            <table className="data-table">
              <thead><tr><th>Source</th><th>Signal</th><th>Points to</th><th>Conf.</th></tr></thead>
              <tbody>
                {c.evidence.map((e, i) => (
                  <tr key={i}>
                    <td><span className="badge badge-unknown">{e.source}</span></td>
                    <td className="mono" style={{ fontSize: 12 }}>{e.signal}</td>
                    <td>{[e.category, e.os_family, e.subtype].filter(Boolean).join(' / ') || '—'}</td>
                    <td>{e.confidence}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {(reclassify.error || lock.error) && (
            <p className="error-msg" style={{ marginTop: 8 }}>{((reclassify.error || lock.error) as Error).message}</p>
          )}
        </>
      )}
    </Panel>
  )
}
