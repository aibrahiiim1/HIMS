import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Wifi, WifiOff, AlertTriangle, HelpCircle, ShieldCheck, KeyRound, Bot, ShieldX, XCircle, CircleSlash, Lock, Globe, ClipboardList } from 'lucide-react'
import type { ComponentType } from 'react'
import { api, type DeviceStatusSummary, MGMT_BADGE } from '../api'
import { Panel, InfoHint } from './ui'

type Tone = 'ok' | 'crit' | 'warn' | 'muted' | 'info'
const toneColor = (t: Tone) => (t === 'ok' ? 'var(--ok)' : t === 'crit' ? 'var(--crit)' : t === 'warn' ? 'var(--warn)' : t === 'info' ? 'var(--brand)' : 'var(--text-muted)')

// Management states that already have a curated gap tile (icon + tone). Managed /
// inventory-only / virtual are handled by the summary row, not the gap row.
const MANAGED_LIKE = new Set(['managed', 'inventory_only', 'virtual'])
// Gap states, worst-first, each with a plain label + drill-down. Only the ones with
// a live count > 0 are rendered — so a clean fleet shows no noisy rows of zeros.
const GAP_STATES: { key: string; label: string; tone: Tone; icon: ComponentType<{ size?: number }> }[] = [
  { key: 'credential_failed', label: 'Credential failed', tone: 'crit', icon: ShieldX },
  { key: 'not_authorized', label: 'Not authorized on host', tone: 'crit', icon: Lock },
  { key: 'agent_offline', label: 'Agent offline', tone: 'crit', icon: Bot },
  { key: 'collection_failed', label: 'Collection failed', tone: 'crit', icon: XCircle },
  { key: 'needs_credential', label: 'Needs credential', tone: 'warn', icon: KeyRound },
  { key: 'needs_agent', label: 'Needs agent', tone: 'warn', icon: Bot },
  { key: 'web_authenticated', label: 'Web only (no deep)', tone: 'warn', icon: Globe },
  { key: 'unmanaged', label: 'Unmanaged', tone: 'muted', icon: CircleSlash },
]
const GAP_KEYS = new Set(GAP_STATES.map((g) => g.key))

// Tile — one routed stat. Consistent height so the grid lines up. `big` bumps the
// value for the headline management outcomes (Managed / Inventory-only).
function Tile({ to, label, value, tone, icon: Icon, hint, big }: {
  to: string; label: string; value: number; tone: Tone; icon: ComponentType<{ size?: number }>; hint?: string; big?: boolean
}) {
  const color = toneColor(tone)
  return (
    <Link to={to} className="rm-tile" style={{ textDecoration: 'none', color: 'inherit', display: 'flex', flexDirection: 'column', gap: 4, padding: '10px 12px', border: '1px solid var(--border)', borderLeft: `3px solid ${value > 0 && tone !== 'muted' && tone !== 'ok' ? color : 'var(--border)'}`, borderRadius: 8, minWidth: 0 }}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color, fontSize: 12, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}><Icon size={13} /> {label}{hint && <InfoHint text={hint} label={label} />}</span>
      <span style={{ fontSize: big ? 26 : 20, fontWeight: 700, lineHeight: 1 }}>{value}</span>
    </Link>
  )
}

// An even, wrapping grid so tiles always line up in tidy rows (no 8-then-2 orphans).
const gridStyle: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 8 }
const groupLabel: React.CSSProperties = { fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 6, color: 'var(--text-muted)', fontWeight: 600 }

// ReachManageCards keeps the two questions visually SEPARATE: "is it reachable?"
// (ping/TCP/SNMP) and "can HIMS log in and collect?" (a proven method). The
// management block leads with the outcome (Managed / Inventory-only / Not managed)
// and then shows ONLY the real gaps — zero-count states are hidden as noise.
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
  // Catch-all: any other non-managed state present (not_attempted, pending_collection,
  // partially_managed…) so a real gap can never be hidden just because it lacks a tile.
  const otherGaps = Object.keys(m).filter((k) => !MANAGED_LIKE.has(k) && !GAP_KEYS.has(k) && n(m, k) > 0)

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><Wifi size={15} /> Reachability &amp; Management</span>}
      subtitle="two separate questions — kept separate"
      actions={<span className="muted" style={{ fontSize: 12 }}>Online ≠ Managed</span>}
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.error && <p className="error-msg">{(q.error as Error).message}</p>}
      {d && (
        <div style={{ display: 'grid', gap: 18 }}>
          {/* Reachability — is the device answering on the network? */}
          <div>
            <div style={groupLabel}>Reachability — is it answering the network?</div>
            <div style={gridStyle}>
              <Tile to="/inventory?reachability=online" label="Online" value={n(r, 'online')} tone="ok" icon={Wifi} hint="Responds to its monitoring check right now." />
              <Tile to="/inventory?reachability=offline" label="Offline" value={n(r, 'offline')} tone={n(r, 'offline') > 0 ? 'crit' : 'muted'} icon={WifiOff} hint="Monitoring check is failing — no response." />
              <Tile to="/inventory?reachability=warning" label="Warning" value={n(r, 'warning')} tone={n(r, 'warning') > 0 ? 'warn' : 'muted'} icon={AlertTriangle} hint="Degraded — a supplemental check is down, or flapping." />
              <Tile to="/inventory?reachability=unknown" label="Unknown" value={n(r, 'unknown')} tone="muted" icon={HelpCircle} hint="No monitoring check has run yet." />
            </div>
          </div>

          {/* Management — outcome first (Managed / Inventory-only / Not managed). */}
          <div>
            <div style={groupLabel}>Management — can HIMS log in &amp; collect?</div>
            <div style={gridStyle}>
              <Tile to="/inventory?management=managed" label="Managed" value={n(m, 'managed')} tone="ok" icon={ShieldCheck} big hint="Proven working credential — HIMS authenticates and collects." />
              <Tile to="/inventory?management=inventory_only" label="Inventory only" value={n(m, 'inventory_only')} tone="info" icon={ClipboardList} big hint="Recorded and monitored for reachability; access deliberately opted out — no credential expected." />
              <Tile to="/inventory/unmanaged" label="Not managed" value={notManaged} tone={notManaged > 0 ? 'warn' : 'muted'} icon={CircleSlash} big hint="Manageable devices with no proven access yet (sum of the gaps below). Inventory-only is NOT counted here." />
            </div>
          </div>

          {/* Gaps to fix — only the states that actually have devices (no zero noise). */}
          {(gaps.length > 0 || otherGaps.length > 0) && (
            <div>
              <div style={groupLabel}>Gaps to fix — why those {notManaged} aren&apos;t managed</div>
              <div style={gridStyle}>
                {gaps.map((g) => (
                  <Tile key={g.key} to={`/inventory?management=${g.key}`} label={g.label} value={n(m, g.key)} tone={g.tone} icon={g.icon} />
                ))}
                {otherGaps.map((k) => (
                  <Tile key={k} to={`/inventory?management=${k}`} label={MGMT_BADGE[k]?.label ?? k} value={n(m, k)} tone="warn" icon={CircleSlash} />
                ))}
              </div>
            </div>
          )}

          {/* Cross-axis callouts — proof the two signals are distinct. */}
          {(d.online_unmanaged > 0 || d.offline_prev_managed > 0) && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
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
