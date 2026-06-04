import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Brain, Boxes } from 'lucide-react'
import { api, type Device, type RoleSummaryRow } from '../api'
import { PageHeader, Panel, Kpi, BarList, EmptyState, StatusPill, colorFor } from '../components/ui'

const LABELS: Record<string, string> = {
  domain_controller: 'Domain Controllers', dns: 'DNS Servers', dhcp: 'DHCP Servers',
  sql_server: 'SQL Server', oracle: 'Oracle', postgresql: 'PostgreSQL',
  file_server: 'File Servers', web_server: 'Web Servers', hyperv_host: 'Hyper-V Hosts', esxi_host: 'ESXi Hosts',
}
const label = (r: string) => LABELS[r] ?? r.replace(/_/g, ' ')
const detailBase: Record<string, string> = { switch: '/devices', server: '/servers', firewall: '/firewalls', camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx', virtual_host: '/virtual-hosts' }

// Device Intelligence — the classification engine's results: candidate device
// roles + detected services inferred during discovery from open service ports
// (DHCP/DNS/HTTP/SSH/SQL/…). This is read-only intelligence over device_roles.
export function DeviceIntelligence() {
  const [role, setRole] = useState<string | null>(null)
  const summary = useQuery({ queryKey: ['role-summary'], queryFn: () => api.get<RoleSummaryRow[]>('/roles/summary') })
  const devices = useQuery({ queryKey: ['role-devices', role], queryFn: () => api.get<Device[]>(`/roles/${role}/devices`), enabled: !!role })

  const rows = summary.data ?? []
  const total = rows.reduce((a, r) => a + r.count, 0)
  const bars = rows.map((r) => ({ label: label(r.role), value: r.count, color: colorFor(r.role) }))

  return (
    <div>
      <PageHeader title="Device Intelligence" icon={Brain} subtitle="Classification-engine results — candidate device roles and detected services from discovery" />

      <div className="kpi-grid">
        <Kpi label="Classified Roles" value={rows.length} icon={Brain} tone="info" />
        <Kpi label="Role Assignments" value={total} icon={Boxes} tone="default" sub="across devices" />
        <Kpi label="Top Role" value={rows.length ? label([...rows].sort((a, b) => b.count - a.count)[0].role) : '—'} tone="default" />
      </div>

      <Panel title="Detected Services & Candidate Roles" icon={Brain}>
        {summary.isLoading && <div className="loading">Loading…</div>}
        {rows.length === 0 && (
          <EmptyState icon={Brain} title="No classified roles yet" message="Roles and services are inferred during discovery from open service ports (DHCP/DNS/HTTP/SSH/SQL/…). Run a discovery scan to populate." />
        )}
        {rows.length > 0 && (
          <div className="grid-side">
            <BarList rows={bars} />
            <div className="seg" style={{ alignItems: 'flex-start' }}>
              {rows.map((r) => (
                <button key={r.role} className={'seg-chip' + (role === r.role ? ' active' : '')} onClick={() => setRole(role === r.role ? null : r.role)}>
                  {label(r.role)} <span className="seg-count">{r.count}</span>
                </button>
              ))}
            </div>
          </div>
        )}
      </Panel>

      {role && (
        <Panel title={label(role)} subtitle={`${devices.data?.length ?? 0} devices`} pad={false}>
          {devices.isLoading && <div className="loading">Loading…</div>}
          {devices.data && devices.data.length === 0 && <EmptyState icon={Boxes} title="No devices in this role" />}
          {devices.data && devices.data.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Vendor</th><th>Status</th></tr></thead>
              <tbody>
                {devices.data.map((d) => {
                  const base = detailBase[d.category]
                  return (
                    <tr key={d.id}>
                      <td className="cell-name">{base ? <Link to={`${base}/${d.id}`}>{d.name}</Link> : d.name}</td>
                      <td className="mono">{d.primary_ip ?? '—'}</td>
                      <td>{d.category.replace(/_/g, ' ')}</td>
                      <td>{d.vendor ?? '—'}</td>
                      <td><StatusPill status={d.status} /></td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </Panel>
      )}
    </div>
  )
}
