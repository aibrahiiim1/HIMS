import { useQuery } from '@tanstack/react-query'
import { ScanSearch } from 'lucide-react'
import { api, type ScanDetail, type ClassificationEvidenceChannels } from '../api'
import { EmptyState } from './ui'

// ClassificationEvidence renders the Phase-3 "why was this classified this way"
// record (probe_data.classification_detail) plus the surrounding scan signals
// (protocols tried, bound credential, next action). Reused by Scan Job Results
// (expandable row) and Device Detail (Discovery tab). Read-only.

const SRC_LABEL: Record<string, string> = {
  fingerprint: 'fingerprint match',
  driver: 'driver match',
  plan: 'protocol-plan guess',
  none: 'no positive signal',
}

const lbl = (s?: string) => (s ?? '').replace(/_/g, ' ')

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ fontSize: 10, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--muted, #8a93a6)', marginBottom: 4 }}>{title}</div>
      {children}
    </div>
  )
}

function evidenceRows(ev: ClassificationEvidenceChannels): Array<[string, string]> {
  const out: Array<[string, string]> = []
  if (ev.sysobjectid) out.push(['sysObjectID', ev.sysobjectid])
  if (ev.sysdescr) out.push(['sysDescr', ev.sysdescr])
  if (ev.sysname) out.push(['sysName', ev.sysname])
  if (ev.http_server) out.push(['HTTP Server', ev.http_server])
  if (ev.ssh_banner) out.push(['SSH banner', ev.ssh_banner])
  if (ev.ports && ev.ports.length) out.push(['Open ports', ev.ports.join(', ')])
  return out
}

export function ClassificationEvidence({ detail }: { detail: ScanDetail }) {
  const cd = detail.classification_detail
  const finalClass = detail.classification || 'unknown'
  const conf = typeof detail.confidence === 'number' ? detail.confidence : undefined
  const isUnknown = !finalClass || finalClass === 'unknown'

  return (
    <div style={{ fontSize: 12, lineHeight: 1.5, padding: '4px 2px', maxWidth: 920 }}>
      {/* Verdict header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>
        <strong style={{ textTransform: 'capitalize', fontSize: 13 }}>{lbl(finalClass)}</strong>
        {conf !== undefined && conf > 0 && <span className="badge badge-info">{conf}% confidence</span>}
        {cd?.final_source && <span className="muted">via {SRC_LABEL[cd.final_source] ?? cd.final_source}</span>}
        {detail.class_note && <span className="badge badge-warning" style={{ textTransform: 'none' }} title={detail.class_note}>classification preserved</span>}
      </div>

      {isUnknown && cd?.likely_type && (
        <div style={{ marginBottom: 10, padding: '6px 8px', background: 'rgba(80,140,255,0.08)', borderRadius: 6 }}>
          Most likely a <strong style={{ textTransform: 'capitalize' }}>{lbl(cd.likely_type)}</strong> — best guess from the evidence below (no rule matched with enough confidence).
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 16 }}>
        {/* Evidence channels */}
        {cd && evidenceRows(cd.evidence).length > 0 && (
          <Section title="Evidence observed">
            {evidenceRows(cd.evidence).map(([k, v]) => (
              <div key={k} style={{ display: 'flex', gap: 6, marginBottom: 2 }}>
                <span className="muted" style={{ minWidth: 92, flexShrink: 0 }}>{k}</span>
                <span className="mono" style={{ wordBreak: 'break-all' }}>{v}</span>
              </div>
            ))}
          </Section>
        )}

        {/* Winning fingerprints */}
        {cd?.winners && cd.winners.length > 0 && (
          <Section title="Matched fingerprints (ranked)">
            {cd.winners.map((w, i) => (
              <div key={i} style={{ marginBottom: 3 }}>
                <span className={`badge badge-${i === 0 ? 'up' : 'unknown'}`} style={{ textTransform: 'capitalize' }}>{lbl(w.device_type)}</span>{' '}
                <span>{w.vendor}{w.model ? ` · ${w.model}` : ''}</span>{' '}
                <span className="muted">{w.confidence}% · {w.kind}:{w.pattern}</span>
              </div>
            ))}
          </Section>
        )}

        {/* Rejected candidates + why */}
        {cd?.rejected && cd.rejected.length > 0 && (
          <Section title="Rejected candidates">
            {cd.rejected.map((rj, i) => {
              const excluded = rj.reason.toLowerCase().includes('excluded')
              return (
                <div key={i} style={{ marginBottom: 3 }}>
                  <span className={`badge badge-${excluded ? 'down' : 'warning'}`} style={{ textTransform: 'capitalize' }}>{lbl(rj.device_type)}</span>{' '}
                  <span className="muted">{rj.vendor} · {rj.kind}:{rj.pattern}</span>
                  <div className="muted" style={{ fontSize: 11, marginLeft: 2 }}>↳ {rj.reason}</div>
                </div>
              )
            })}
          </Section>
        )}

        {/* Protocols + binding */}
        <Section title="Protocols & access">
          <div style={{ marginBottom: 2 }}><span className="muted" style={{ minWidth: 92, display: 'inline-block' }}>Expected</span>{(detail.expected_protocols ?? []).join(', ').toUpperCase() || '—'}</div>
          <div style={{ marginBottom: 2 }}><span className="muted" style={{ minWidth: 92, display: 'inline-block' }}>Opportunistic</span>{(detail.opportunistic_protocols ?? []).join(', ').toUpperCase() || '—'}</div>
          <div style={{ marginBottom: 2 }}><span className="muted" style={{ minWidth: 92, display: 'inline-block' }}>Not applicable</span>{(detail.skipped_protocols ?? []).join(', ').toUpperCase() || '—'}</div>
          <div style={{ marginBottom: 2 }}><span className="muted" style={{ minWidth: 92, display: 'inline-block' }}>Bound cred</span>{detail.bound_cred ? <span className="badge badge-up">{detail.bound_cred}</span> : '—'}</div>
        </Section>
      </div>

      {/* Fallback for older results lacking classification_detail */}
      {!cd && detail.evidence && detail.evidence.length > 0 && (
        <Section title="Evidence observed">
          {detail.evidence.map((e, i) => <div key={i} className="mono" style={{ wordBreak: 'break-all' }}>{e}</div>)}
        </Section>
      )}

      {detail.next_action && (
        <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--border, #2a3042)' }}>
          <span className="muted">Next action: </span>{detail.next_action}
        </div>
      )}
    </div>
  )
}

// DeviceClassificationEvidence fetches a device's latest scan probe_data and
// renders the evidence panel — or a polite empty state when the device has no
// recorded scan yet. Reusable across detail pages (rendered inside ClassificationCard).
export function DeviceClassificationEvidence({ deviceId }: { deviceId: string }) {
  const q = useQuery({
    queryKey: ['classification-evidence', deviceId],
    queryFn: () => api.get<{ detail: ScanDetail | null }>(`/devices/${deviceId}/classification-evidence`),
  })
  if (q.isLoading) return <div className="loading" style={{ fontSize: 12 }}>Loading evidence…</div>
  const detail = q.data?.detail
  if (!detail || (!detail.classification_detail && !(detail.evidence && detail.evidence.length))) {
    return (
      <EmptyState
        icon={ScanSearch}
        title="No classification evidence yet"
        message="No classification evidence recorded yet. Run discovery/collect to populate evidence."
      />
    )
  }
  return <ClassificationEvidence detail={detail} />
}
