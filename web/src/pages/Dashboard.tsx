import { useQuery } from '@tanstack/react-query'
import { api, type ExpenseByCategory, type MonitoringOverviewRow, type RoleSummaryRow } from '../api'

interface CountRow { category?: string; status?: string; count: number }
interface DashboardData {
  by_category?: CountRow[]
  by_status?: CountRow[]
  by_role?: RoleSummaryRow[]
  monitoring?: MonitoringOverviewRow[]
  expenses_by_category?: ExpenseByCategory[]
  headline?: {
    open_work_orders?: number
    open_alerts?: number
    expiring_systems?: number
    devices_needing_attention?: number
    total_expenses?: number
  }
}

const tile: React.CSSProperties = {
  padding: '16px 20px', borderRadius: 8, background: '#1a1a1a', border: '1px solid #444', minWidth: 150,
}

function Tile({ label, value, warn }: { label: string; value: number | string; warn?: boolean }) {
  return (
    <div style={tile}>
      <div style={{ fontSize: 30, fontWeight: 700, color: warn ? '#ef9a9a' : '#eee' }}>{value}</div>
      <div className="muted">{label}</div>
    </div>
  )
}

function Breakdown({ title, rows }: { title: string; rows: { label: string; count: number }[] }) {
  const total = rows.reduce((a, r) => a + r.count, 0)
  return (
    <div className="card" style={{ flex: 1, minWidth: 260 }}>
      <h3>{title}</h3>
      {rows.length === 0 && <div className="muted">No data.</div>}
      {rows.map((r) => (
        <div key={r.label} style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '4px 0' }}>
          <div style={{ width: 130 }}>{r.label}</div>
          <div style={{ flex: 1, background: '#222', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{ width: `${total ? (r.count / total) * 100 : 0}%`, background: '#1565c0', height: 14 }} />
          </div>
          <div style={{ width: 36, textAlign: 'right' }}>{r.count}</div>
        </div>
      ))}
    </div>
  )
}

export function Dashboard() {
  const d = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get<DashboardData>('/dashboard'), refetchInterval: 30_000 })
  const h = d.data?.headline ?? {}

  return (
    <div>
      <div className="card">
        <h2>Executive dashboard</h2>
        <p className="muted" style={{ marginBottom: 12 }}>Fleet-wide rollup across inventory, monitoring, and operations.</p>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          <Tile label="Needs attention" value={h.devices_needing_attention ?? 0} warn={(h.devices_needing_attention ?? 0) > 0} />
          <Tile label="Open alerts" value={h.open_alerts ?? 0} warn={(h.open_alerts ?? 0) > 0} />
          <Tile label="Open work orders" value={h.open_work_orders ?? 0} />
          <Tile label="Expiring systems (90d)" value={h.expiring_systems ?? 0} warn={(h.expiring_systems ?? 0) > 0} />
          <Tile label="Total expenses" value={(h.total_expenses ?? 0).toLocaleString()} />
        </div>
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16 }}>
        <Breakdown title="Inventory by category" rows={(d.data?.by_category ?? []).map((r) => ({ label: r.category ?? '?', count: r.count }))} />
        <Breakdown title="Devices by status" rows={(d.data?.by_status ?? []).map((r) => ({ label: r.status ?? '?', count: r.count }))} />
        <Breakdown title="Monitoring health" rows={(d.data?.monitoring ?? []).map((r) => ({ label: r.status, count: r.count }))} />
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16 }}>
        <Breakdown title="Roles" rows={(d.data?.by_role ?? []).map((r) => ({ label: r.role, count: r.count }))} />
        <Breakdown title="Expenses by category" rows={(d.data?.expenses_by_category ?? []).map((r) => ({ label: r.category, count: Math.round(r.total) }))} />
      </div>
    </div>
  )
}
