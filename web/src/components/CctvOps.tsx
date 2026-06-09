import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Video, RefreshCw } from 'lucide-react'
import { api, type CCTVSummary, type CCTVFleetRun, type CCTVFleetItem } from '../api'
import { Panel, StatusPill } from './ui'

// CctvOps is the CCTV hub control: the fleet breakdown (NVRs vs DVRs vs standalone
// cameras + camera channels — channels reported separately, never counted as
// devices) and a one-click fleet-wide collection that uses each device's bound
// credential only (no spraying, so it cannot trigger a Hikvision lockout).
const OUTCOME_TONE: Record<string, 'up' | 'down' | 'warning' | 'unknown'> = {
  collected: 'up', auth: 'down', lockout: 'warning', unsupported: 'warning',
  unreachable: 'unknown', no_credential: 'warning', skipped: 'unknown', error: 'down',
}

export function CctvOps() {
  const qc = useQueryClient()
  const [showItems, setShowItems] = useState(false)

  const summary = useQuery({ queryKey: ['cctv-summary'], queryFn: () => api.get<CCTVSummary>('/cctv/summary') })
  const fleet = useQuery({
    queryKey: ['cctv-fleet'],
    queryFn: () => api.get<CCTVFleetRun>('/cctv/collect-fleet'),
    refetchInterval: (q) => ((q.state.data as CCTVFleetRun | undefined)?.running ? 2000 : false),
  })
  const start = useMutation({
    mutationFn: () => api.post<{ started: boolean; total: number }>('/cctv/collect-fleet', {}),
    onSuccess: () => { setShowItems(true); qc.invalidateQueries({ queryKey: ['cctv-fleet'] }) },
  })
  // When a run finishes, refresh the breakdown + device lists.
  const run = fleet.data
  const running = !!run?.running
  const s = summary.data

  const pct = run && run.total > 0 ? Math.round((run.done / run.total) * 100) : 0
  const items = run?.items ?? []
  const sorted = [...items].sort((a, b) => (a.status === b.status ? 0 : a.status === 'failed' ? -1 : 1))

  return (
    <Panel title="CCTV Fleet" icon={Video}
      subtitle="NVRs · DVRs · standalone cameras · camera channels"
      actions={
        <button className="btn btn-primary btn-sm" disabled={running || start.isPending}
          onClick={() => start.mutate()} title="Collect every camera/NVR/DVR using its bound credential only (no spraying)">
          <RefreshCw size={14} className={running || start.isPending ? 'spin' : ''} /> {running ? 'Collecting…' : 'Collect all CCTV'}
        </button>
      }>
      <div className="row" style={{ flexWrap: 'wrap', gap: 18, fontSize: 14 }}>
        <Stat label="NVRs" value={s?.nvrs} />
        <Stat label="DVRs" value={s?.dvrs} />
        <Stat label="Standalone cameras" value={s?.cameras} />
        <Stat label="Camera channels" value={s?.channels} sub={s ? `${s.channels_linked} linked to a device` : undefined} />
        <Stat label="CCTV devices" value={s?.devices_total} sub="channels excluded" />
      </div>

      {run && (running || run.done > 0) && (
        <div style={{ marginTop: 14 }}>
          <div className="row" style={{ justifyContent: 'space-between', fontSize: 13, marginBottom: 6 }}>
            <span>{running ? 'Collecting' : 'Last run'}: {run.done}/{run.total} · <strong style={{ color: 'var(--ok)' }}>{run.collected} collected</strong> · <strong style={{ color: 'var(--crit)' }}>{run.failed} failed</strong>{run.skipped > 0 ? <> · <strong>{run.skipped} skipped</strong></> : null}</span>
            <button className="btn btn-ghost btn-xs" onClick={() => setShowItems((v) => !v)}>{showItems ? 'Hide' : 'Show'} per-device results</button>
          </div>
          <div style={{ height: 6, background: 'var(--surface-2, #1c2730)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{ width: `${pct}%`, height: '100%', background: running ? 'var(--info, #3b82f6)' : 'var(--ok, #22c55e)', transition: 'width .3s' }} />
          </div>
          {showItems && items.length > 0 && (
            <table className="data-table" style={{ marginTop: 10 }}>
              <thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Outcome</th><th>Detail</th></tr></thead>
              <tbody>
                {sorted.map((it: CCTVFleetItem) => (
                  <tr key={it.device_id}>
                    <td className="cell-name">{it.name}</td>
                    <td className="mono">{it.ip || '—'}</td>
                    <td>{it.was_category !== it.now_category ? <span>{it.was_category} → <strong>{it.now_category}</strong></span> : it.now_category}</td>
                    <td><StatusPill status={OUTCOME_TONE[it.outcome] ?? 'unknown'} label={it.outcome} /></td>
                    <td className="muted" style={{ maxWidth: 420, fontSize: 12 }}>{it.detail || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
      <p className="muted" style={{ marginTop: 12, fontSize: 12 }}>
        Each device is collected with its <strong>bound credential only</strong> — no credential spraying, so this cannot trigger a Hikvision IP lockout. Devices without a bound web credential are reported as <em>no_credential</em>; bind the device’s web login and re-run.
      </p>
    </Panel>
  )
}

function Stat({ label, value, sub }: { label: string; value?: number; sub?: string }) {
  return (
    <div>
      <div style={{ fontSize: 22, fontWeight: 700 }}>{value ?? '—'}</div>
      <div className="muted" style={{ fontSize: 12 }}>{label}{sub ? <span> · {sub}</span> : ''}</div>
    </div>
  )
}
