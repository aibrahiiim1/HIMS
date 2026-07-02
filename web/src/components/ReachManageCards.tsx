import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Wifi, ShieldCheck, ClipboardList, CircleSlash } from 'lucide-react'
import type { ComponentType } from 'react'
import { api, type DeviceStatusSummary, MGMT_BADGE } from '../api'
import { Panel, InfoHint } from './ui'

type Tone = 'ok' | 'crit' | 'warn' | 'muted' | 'info'
const toneColor = (t: Tone) => (t === 'ok' ? 'var(--ok)' : t === 'crit' ? 'var(--crit)' : t === 'warn' ? 'var(--warn)' : t === 'info' ? 'var(--brand)' : 'var(--text-muted)')

// Managed-like states are handled by the outcome row, not counted as a gap.
const MANAGED_LIKE = new Set(['managed', 'inventory_only', 'virtual'])
// Gap states worst-first; only the ones with a live count > 0 render as chips, so
// a clean fleet shows no noise.
const GAP_STATES: { key: string; label: string; crit: boolean }[] = [
  { key: 'credential_failed', label: 'Credential failed', crit: true },
  { key: 'not_authorized', label: 'Not authorized on host', crit: true },
  { key: 'agent_offline', label: 'Agent offline', crit: true },
  { key: 'collection_failed', label: 'Collection failed', crit: true },
  { key: 'needs_credential', label: 'Needs credential', crit: false },
  { key: 'needs_agent', label: 'Needs agent', crit: false },
  { key: 'web_authenticated', label: 'Web only (no deep)', crit: false },
  { key: 'unmanaged', label: 'Unmanaged', crit: false },
]
const GAP_KEYS = new Set(GAP_STATES.map((g) => g.key))

// The four reachability states, in health order, for the segmented bar + legend.
const REACH: { key: string; label: string; color: string }[] = [
  { key: 'online', label: 'Online', color: 'var(--ok)' },
  { key: 'warning', label: 'Warning', color: 'var(--warn)' },
  { key: 'offline', label: 'Offline', color: 'var(--crit)' },
  { key: 'unknown', label: 'Unknown', color: 'var(--text-muted)' },
]

const groupLabel: React.CSSProperties = { fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 8, color: 'var(--text-muted)', fontWeight: 600 }

// OutcomeTile — a prominent management-outcome stat (Managed / Inventory only /
// Not managed). Big value, tone-colored left border on a real gap, routed.
function OutcomeTile({ to, label, value, tone, icon: Icon, hint }: {
  to: string; label: string; value: number; tone: Tone; icon: ComponentType<{ size?: number }>; hint: string
}) {
  const color = toneColor(tone)
  const accent = value > 0 && tone === 'warn' ? color : 'var(--border)'
  return (
    <Link to={to} className="rm-tile" style={{ textDecoration: 'none', color: 'inherit', display: 'flex', flexDirection: 'column', gap: 3, padding: '10px 12px', border: '1px solid var(--border)', borderLeft: `3px solid ${accent}`, borderRadius: 8, minWidth: 0 }}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color, fontSize: 12, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}><Icon size={13} /> {label}<InfoHint text={hint} label={label} /></span>
      <span style={{ fontSize: 26, fontWeight: 800, lineHeight: 1 }}>{value.toLocaleString()}</span>
    </Link>
  )
}

// ReachManageCards keeps the two questions SEPARATE and reads well at half width:
// reachability as a segmented health bar + clickable legend, management as three
// outcome tiles (Managed / Inventory-only / Not managed) with the real gaps shown
// as compact chips (zero-count states are hidden as noise).
export function ReachManageCards() {
  const q = useQuery({
    queryKey: ['device-status-summary'],
    queryFn: () => api.get<DeviceStatusSummary>('/devices/status-summary'),
    refetchInterval: 30_000,
    retry: 0,
  })
  const d = q.data
  const r = d?.reachability ?? {}
  const m = d?.management ?? {}
  const n = (rec: Record<string, number>, k: string) => rec[k] ?? 0

  const notManaged = Object.entries(m).reduce((s, [k, v]) => (MANAGED_LIKE.has(k) ? s : s + (v || 0)), 0)
  const gaps = GAP_STATES.filter((g) => n(m, g.key) > 0)
  const otherGaps = Object.keys(m).filter((k) => !MANAGED_LIKE.has(k) && !GAP_KEYS.has(k) && n(m, k) > 0)
  const reachTotal = REACH.reduce((s, x) => s + n(r, x.key), 0) || 1

  return (
    <Panel
      className="fill"
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><Wifi size={15} /> Reachability &amp; Management</span>}
      subtitle="two separate questions — kept separate"
      actions={<span className="muted" style={{ fontSize: 12 }}>Online ≠ Managed</span>}
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.error && <p className="error-msg">{(q.error as Error).message}</p>}
      {d && (
        <div style={{ display: 'grid', gap: 20 }}>
          {/* Reachability — segmented health bar + clickable legend. */}
          <div>
            <div style={groupLabel}>Reachability — is it answering the network?</div>
            <div style={{ display: 'flex', height: 12, borderRadius: 6, overflow: 'hidden', background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
              {REACH.filter((s) => n(r, s.key) > 0).map((s) => (
                <div key={s.key} style={{ width: `${(n(r, s.key) / reachTotal) * 100}%`, minWidth: 4, background: s.color }} title={`${s.label}: ${n(r, s.key)}`} />
              ))}
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px 18px', marginTop: 12 }}>
              {REACH.map((s) => {
                const v = n(r, s.key)
                const alert = v > 0 && (s.key === 'offline' || s.key === 'warning')
                return (
                  <Link key={s.key} to={`/inventory?reachability=${s.key}`} style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 13, color: 'inherit', textDecoration: 'none' }}>
                    <span style={{ width: 9, height: 9, borderRadius: '50%', background: s.color, flexShrink: 0 }} />
                    <span className="muted">{s.label}</span>
                    <b style={{ color: alert ? s.color : undefined }}>{v.toLocaleString()}</b>
                  </Link>
                )
              })}
            </div>
          </div>

          {/* Management — outcome tiles, then the real gaps as chips. */}
          <div>
            <div style={groupLabel}>Management — can HIMS log in &amp; collect?</div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(138px, 1fr))', gap: 8 }}>
              <OutcomeTile to="/inventory?management=managed" label="Managed" value={n(m, 'managed')} tone="ok" icon={ShieldCheck} hint="Proven working credential — HIMS authenticates and collects." />
              <OutcomeTile to="/inventory?management=inventory_only" label="Inventory only" value={n(m, 'inventory_only')} tone="info" icon={ClipboardList} hint="Recorded and monitored for reachability; access deliberately opted out — no credential expected." />
              <OutcomeTile to="/inventory/unmanaged" label="Not managed" value={notManaged} tone={notManaged > 0 ? 'warn' : 'muted'} icon={CircleSlash} hint="Manageable devices with no proven access yet (sum of the gaps below). Inventory-only is NOT counted here." />
            </div>
            {(gaps.length > 0 || otherGaps.length > 0) && (
              <div style={{ marginTop: 12, display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 6 }}>
                <span className="muted" style={{ fontSize: 12, marginRight: 2 }}>Gaps to fix:</span>
                {gaps.map((g) => (
                  <Link key={g.key} to={`/inventory?management=${g.key}`} className={`badge ${g.crit ? 'badge-down' : 'badge-warning'}`} style={{ textDecoration: 'none' }}>
                    {g.label}: {n(m, g.key)}
                  </Link>
                ))}
                {otherGaps.map((k) => (
                  <Link key={k} to={`/inventory?management=${k}`} className="badge badge-warning" style={{ textDecoration: 'none' }}>{MGMT_BADGE[k]?.label ?? k}: {n(m, k)}</Link>
                ))}
              </div>
            )}
          </div>

          {/* Cross-axis callouts — proof the two signals are distinct. */}
          {(d.online_unmanaged > 0 || d.offline_prev_managed > 0) && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 'auto' }}>
              {d.online_unmanaged > 0 && (
                <Link to="/inventory?reachability=online&management=not_managed" className="badge badge-warning" style={{ textDecoration: 'none' }} title="Devices that answer the network but HIMS cannot manage — open ports are not management.">
                  Online but Unmanaged: {d.online_unmanaged}
                </Link>
              )}
              {d.offline_prev_managed > 0 && (
                <Link to="/data-quality" className="badge badge-unknown" style={{ textDecoration: 'none' }} title="Devices offline now that have a proven working management method on record.">
                  Offline (was Managed): {d.offline_prev_managed}
                </Link>
              )}
            </div>
          )}
        </div>
      )}
    </Panel>
  )
}
