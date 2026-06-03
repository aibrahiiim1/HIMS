import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Device, type RoleSummaryRow } from '../api'

const LABELS: Record<string, string> = {
  domain_controller: 'Domain Controllers',
  dns: 'DNS Servers',
  dhcp: 'DHCP Servers',
  sql_server: 'SQL Server',
  oracle: 'Oracle',
  postgresql: 'PostgreSQL',
  file_server: 'File Servers',
  web_server: 'Web Servers',
  hyperv_host: 'Hyper-V Hosts',
  esxi_host: 'ESXi Hosts',
}
const label = (r: string) => LABELS[r] ?? r

// The CMDB role cut: a device may hold several roles (DC + DNS + DHCP). Roles
// are inferred from open service ports during discovery; deep confirmation
// (LDAP bind / SQL handshake) is a deferred follow-up.
export function Roles() {
  const [role, setRole] = useState<string | null>(null)
  const summary = useQuery({ queryKey: ['role-summary'], queryFn: () => api.get<RoleSummaryRow[]>('/roles/summary') })
  const devices = useQuery({
    queryKey: ['role-devices', role],
    queryFn: () => api.get<Device[]>(`/roles/${role}/devices`),
    enabled: !!role,
  })

  return (
    <div>
      <div className="card">
        <h2>Roles</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Multi-role CMDB view — a device can be Domain Controller + DNS + DHCP at once. Roles are
          inferred from open service ports (candidate roles); deep confirmation is a follow-up.
        </p>
        {summary.data && summary.data.length === 0 && (
          <div className="muted">
            No roles recorded yet. Roles are applied when the discovery→persist apply worker runs
            (see BACKLOG). Inference logic + this view are ready.
          </div>
        )}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          {(summary.data ?? []).map((r) => (
            <button
              key={r.role}
              onClick={() => setRole(r.role)}
              style={{
                padding: '12px 16px', borderRadius: 8, cursor: 'pointer',
                border: role === r.role ? '2px solid #90caf9' : '1px solid #444',
                background: '#1a1a1a', color: '#eee', minWidth: 140, textAlign: 'left',
              }}
            >
              <div style={{ fontSize: 24, fontWeight: 700 }}>{r.count}</div>
              <div className="muted">{label(r.role)}</div>
            </button>
          ))}
        </div>
      </div>

      {role && (
        <div className="card">
          <h3>{label(role)}</h3>
          {devices.isLoading && <div className="loading">Loading…</div>}
          {devices.data && devices.data.length === 0 && <div className="muted">No devices.</div>}
          {devices.data && devices.data.length > 0 && (
            <table>
              <thead><tr><th>Name</th><th>IP</th><th>Category</th><th>Status</th></tr></thead>
              <tbody>
                {devices.data.map((d) => (
                  <tr key={d.id}>
                    <td><strong>{d.name}</strong></td>
                    <td>{d.primary_ip ?? '—'}</td>
                    <td>{d.category}</td>
                    <td>{d.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  )
}
