import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type FirewallStatus, type VpnTunnel, type HAMember, type License, type DeviceFact } from '../api'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

const HA_LABEL: Record<string, string> = {
  standalone: 'Standalone', 'active-active': 'Active-Active',
  'active-passive': 'Active-Passive', unknown: 'Unknown',
}

// The firewall template (FortiGate): HA summary, resource facts, VPN
// tunnels (up/down), HA members (sync state), and license contracts.
export function FirewallDetail() {
  const { id } = useParams<{ id: string }>()
  const status = useQuery({ queryKey: ['fwstatus', id], queryFn: () => api.get<FirewallStatus>(`/devices/${id}/firewall-status`), retry: false })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const tunnels = useQuery({ queryKey: ['vpn', id], queryFn: () => api.get<VpnTunnel[]>(`/devices/${id}/vpn-tunnels`) })
  const members = useQuery({ queryKey: ['ha', id], queryFn: () => api.get<HAMember[]>(`/devices/${id}/ha-members`) })
  const lics = useQuery({ queryKey: ['lic', id], queryFn: () => api.get<License[]>(`/devices/${id}/licenses`) })

  const fm = new Map((facts.data ?? []).map((f) => [f.key, f.value ?? '']))
  const s = status.data

  return (
    <div>
      <div className="card">
        <h2>Firewall status</h2>
        {!s && <div className="muted">No firewall status collected yet. Bind a working SNMP credential.</div>}
        {s && (
          <dl className="kv">
            <div><dt>HA mode</dt><dd><span className="badge badge-access">{HA_LABEL[s.ha_mode] ?? s.ha_mode}</span></dd></div>
            <div><dt>HA group</dt><dd>{s.ha_group_name || '—'}</dd></div>
            <div><dt>Cluster members</dt><dd>{s.ha_member_count}</dd></div>
            <div><dt>Active sessions</dt><dd>{s.session_count?.toLocaleString() ?? '—'}</dd></div>
            <div><dt>CPU</dt><dd>{fm.has('cpu.load_pct') ? `${fm.get('cpu.load_pct')}%` : '—'}</dd></div>
            <div><dt>Memory</dt><dd>{fm.has('memory.used_pct') ? `${fm.get('memory.used_pct')}%` : '—'}</dd></div>
            <div><dt>Disk</dt><dd>{fm.has('disk.used_pct') ? `${fm.get('disk.used_pct')}%` : '—'}</dd></div>
            <div><dt>Disk used</dt><dd>{fmtBytes(Number(fm.get('disk.used_bytes')) || null)}</dd></div>
          </dl>
        )}
      </div>

      <div className="card">
        <h2>VPN tunnels {tunnels.data && tunnels.data.length > 0 ? `(${tunnels.data.length})` : ''}</h2>
        {tunnels.data && tunnels.data.length === 0 && <div className="muted">No IPsec tunnels reported.</div>}
        {tunnels.data && tunnels.data.length > 0 && (
          <table>
            <thead><tr><th>Tunnel</th><th>Status</th><th>Remote GW</th><th>In</th><th>Out</th></tr></thead>
            <tbody>
              {tunnels.data.map((t) => (
                <tr key={t.id}>
                  <td>{t.tunnel_name}{t.p1_name && <span className="muted"> ({t.p1_name})</span>}</td>
                  <td><span className={`badge badge-${t.status === 'up' ? 'up' : 'down'}`}>{t.status}</span></td>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{t.remote_gw ?? '—'}</td>
                  <td>{fmtBytes(t.in_octets)}</td>
                  <td>{fmtBytes(t.out_octets)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {members.data && members.data.length > 0 && (
        <div className="card">
          <h2>Cluster members ({members.data.length})</h2>
          <table>
            <thead><tr><th>Serial</th><th>Hostname</th><th>CPU</th><th>Mem</th><th>Sync</th></tr></thead>
            <tbody>
              {members.data.map((m) => (
                <tr key={m.id}>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{m.serial}</td>
                  <td>{m.hostname ?? '—'}</td>
                  <td>{m.cpu_pct != null ? `${m.cpu_pct}%` : '—'}</td>
                  <td>{m.mem_pct != null ? `${m.mem_pct}%` : '—'}</td>
                  <td><span className={`badge badge-${m.sync_status === 'synchronized' ? 'up' : m.sync_status === 'unsynchronized' ? 'down' : 'unknown'}`}>{m.sync_status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="card">
        <h2>License / subscription {lics.data && lics.data.length > 0 ? `(${lics.data.length})` : ''}</h2>
        {lics.data && lics.data.length === 0 && <div className="muted">No FortiGuard/support contracts reported.</div>}
        {lics.data && lics.data.length > 0 && (
          <table>
            <thead><tr><th>Contract</th><th>Expiry</th></tr></thead>
            <tbody>{lics.data.map((l) => <tr key={l.id}><td>{l.contract}</td><td>{l.expiry ?? '—'}</td></tr>)}</tbody>
          </table>
        )}
      </div>
    </div>
  )
}
