import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Wifi, WifiOff, AlertTriangle, HelpCircle, ShieldCheck, KeyRound, Bot, ShieldX, XCircle, CircleSlash } from 'lucide-react'
import { api, type DeviceStatusSummary } from '../api'
import { Panel } from './ui'

// A small routed stat tile. The number is a bookmarkable drill-down into the
// Inventory filtered by reachability= or management=.
function Tile({ to, label, value, tone, icon: Icon }: { to: string; label: string; value: number; tone: 'ok' | 'crit' | 'warn' | 'muted'; icon: React.ComponentType<{ size?: number }> }) {
  const color = tone === 'ok' ? 'var(--ok)' : tone === 'crit' ? 'var(--crit)' : tone === 'warn' ? 'var(--warn)' : 'var(--text-muted)'
  return (
    <Link to={to} className="rm-tile" style={{ textDecoration: 'none', color: 'inherit', display: 'flex', flexDirection: 'column', gap: 4, padding: '10px 12px', border: '1px solid var(--border)', borderRadius: 8, minWidth: 0 }}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color, fontSize: 12 }}><Icon size={13} /> {label}</span>
      <span style={{ fontSize: 22, fontWeight: 700, lineHeight: 1 }}>{value}</span>
    </Link>
  )
}

// ReachManageCards renders TWO clearly separated stat groups so the operator can
// never confuse "how many devices are online" with "how many devices HIMS can
// actually manage". Reachability (ping/TCP/SNMP) and Management (a working,
// authenticated collection method) are different questions with different counts.
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

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><Wifi size={15} /> Reachability &amp; Management</span>}
      actions={<span className="muted" style={{ fontSize: 12 }}>Online ≠ Managed — two separate signals</span>}
    >
      {q.isLoading && <div className="loading">Loading…</div>}
      {q.error && <p className="error-msg">{(q.error as Error).message}</p>}
      {d && (
        <div style={{ display: 'grid', gap: 16 }}>
          {/* Reachability group — is the device responding on the network? */}
          <div>
            <div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 6 }}>Reachability — responding on the network</div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 8 }}>
              <Tile to="/inventory?reachability=online" label="Online" value={n(r, 'online')} tone="ok" icon={Wifi} />
              <Tile to="/inventory?reachability=offline" label="Offline" value={n(r, 'offline')} tone={n(r, 'offline') > 0 ? 'crit' : 'muted'} icon={WifiOff} />
              <Tile to="/inventory?reachability=warning" label="Warning" value={n(r, 'warning')} tone={n(r, 'warning') > 0 ? 'warn' : 'muted'} icon={AlertTriangle} />
              <Tile to="/inventory?reachability=unknown" label="Unknown" value={n(r, 'unknown')} tone="muted" icon={HelpCircle} />
            </div>
          </div>

          {/* Management group — can HIMS authenticate and collect via a working method? */}
          <div>
            <div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 6 }}>Management — HIMS can authenticate &amp; collect</div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 8 }}>
              <Tile to="/inventory?management=managed" label="Managed" value={n(m, 'managed')} tone="ok" icon={ShieldCheck} />
              <Tile to="/inventory?management=needs_credential" label="Needs credential" value={n(m, 'needs_credential')} tone={n(m, 'needs_credential') > 0 ? 'warn' : 'muted'} icon={KeyRound} />
              <Tile to="/inventory?management=credential_failed" label="Credential failed" value={n(m, 'credential_failed')} tone={n(m, 'credential_failed') > 0 ? 'crit' : 'muted'} icon={ShieldX} />
              <Tile to="/inventory?management=needs_agent" label="Needs agent" value={n(m, 'needs_agent')} tone={n(m, 'needs_agent') > 0 ? 'warn' : 'muted'} icon={Bot} />
              <Tile to="/inventory?management=agent_offline" label="Agent offline" value={n(m, 'agent_offline')} tone={n(m, 'agent_offline') > 0 ? 'crit' : 'muted'} icon={Bot} />
              <Tile to="/inventory?management=collection_failed" label="Collection failed" value={n(m, 'collection_failed')} tone={n(m, 'collection_failed') > 0 ? 'crit' : 'muted'} icon={XCircle} />
              <Tile to="/inventory?management=unmanaged" label="Unmanaged" value={n(m, 'unmanaged')} tone="muted" icon={CircleSlash} />
            </div>
          </div>

          {/* Cross-axis callouts — the cases that prove the two signals are distinct. */}
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, paddingTop: 4 }}>
            <Link to="/inventory?reachability=online&management=unmanaged" className="badge badge-warning" style={{ textDecoration: 'none' }} title="Devices that respond on the network but HIMS cannot manage — open ports are not management">
              Online but Unmanaged: {d.online_unmanaged}
            </Link>
            <Link to="/data-quality" className="badge badge-unknown" style={{ textDecoration: 'none' }} title="Devices offline now that have a working management method on record">
              Offline (was Managed): {d.offline_prev_managed}
            </Link>
          </div>
        </div>
      )}
    </Panel>
  )
}
