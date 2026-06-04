import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Building2, Server, TriangleAlert, CircleCheck } from 'lucide-react'
import { api, type SiteRollup } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, HealthRing } from '../components/ui'

export function MultiSite() {
  const q = useQuery({ queryKey: ['sites-overview'], queryFn: () => api.get<SiteRollup[]>('/sites/overview'), refetchInterval: 30_000 })
  const sites = q.data ?? []
  const realSites = sites.filter((s) => s.site_id !== 'unassigned')
  const totalDevices = sites.reduce((a, s) => a + s.devices, 0)
  const totalAlerts = sites.reduce((a, s) => a + s.open_alerts, 0)
  const totalDown = sites.reduce((a, s) => a + s.down, 0)

  return (
    <div>
      <PageHeader title="Multi-Site View" icon={Building2} subtitle="Per-hotel health, device count, alerts and category mix across the group" />

      <div className="kpi-grid">
        <Kpi label="Sites" value={realSites.length} icon={Building2} tone="info" />
        <Kpi label="Devices" value={totalDevices} icon={Server} />
        <Kpi label="Offline" value={totalDown} icon={TriangleAlert} tone={totalDown > 0 ? 'crit' : 'default'} />
        <Kpi label="Open Alerts" value={totalAlerts} icon={TriangleAlert} tone={totalAlerts > 0 ? 'crit' : 'default'} />
      </div>

      {q.isLoading && <div className="loading">Loading…</div>}
      {q.data && sites.length === 0 && <EmptyState icon={Building2} title="No sites" message="Create hotel/site locations and assign devices to them (Locations + Inventory) to see per-site rollups." />}

      <div className="grid-3">
        {sites.map((s) => {
          const healthy = s.devices > 0 ? Math.round((s.up / s.devices) * 100) : 0
          const cats = Object.entries(s.by_category).sort((a, b) => b[1] - a[1]).slice(0, 5)
          const isUnassigned = s.site_id === 'unassigned'
          return (
            <Panel key={s.site_id} title={s.site_name} icon={Building2}
              subtitle={isUnassigned ? 'no site assigned' : (s.kind || 'site')}>
              <div className="row" style={{ alignItems: 'center', gap: 16, marginBottom: 12 }}>
                <HealthRing score={healthy} label="up" size={64} />
                <div style={{ display: 'grid', gap: 2, fontSize: 13 }}>
                  <div><b>{s.devices}</b> devices</div>
                  <div className="muted"><CircleCheck size={11} style={{ color: 'var(--ok,#16a34a)' }} /> {s.up} up · {s.down} down · {s.warning} attn</div>
                  <div className={s.open_alerts > 0 ? '' : 'muted'}>{s.open_alerts > 0 ? <TriangleAlert size={11} style={{ color: 'var(--crit)' }} /> : null} {s.open_alerts} open alert{s.open_alerts === 1 ? '' : 's'}</div>
                </div>
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, marginBottom: 10 }}>
                {cats.map(([cat, n]) => <span key={cat} className="badge badge-unknown">{cat.replace(/_/g, ' ')} {n}</span>)}
              </div>
              {!isUnassigned && <Link className="btn btn-ghost btn-xs" to="/inventory">View devices →</Link>}
            </Panel>
          )
        })}
      </div>
    </div>
  )
}
