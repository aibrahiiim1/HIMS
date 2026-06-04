import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ClipboardCheck, ChevronRight, CircleCheck, TriangleAlert, Info, RefreshCw } from 'lucide-react'
import { api, type DataQualityReport } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'

const detailBase: Record<string, string> = { switch: '/devices', server: '/servers', firewall: '/firewalls', camera: '/cctv', nvr: '/cctv', wireless_controller: '/wlan', printer: '/printers', ups: '/ups', pbx: '/pbx', virtual_host: '/virtual-hosts' }

const SEV: Record<string, { cls: string; icon: typeof Info }> = {
  critical: { cls: 'badge-down', icon: TriangleAlert },
  warning: { cls: 'badge-warning', icon: TriangleAlert },
  info: { cls: 'badge-unknown', icon: Info },
}

export function DataQuality() {
  const q = useQuery({ queryKey: ['data-quality'], queryFn: () => api.get<DataQualityReport>('/data-quality') })
  const [open, setOpen] = useState<string | null>(null)
  const r = q.data

  return (
    <div>
      <PageHeader title="Data Quality" icon={ClipboardCheck} subtitle="Inventory hygiene — duplicates, missing classification and stale records"
        actions={<button className="btn btn-xs" onClick={() => q.refetch()}><RefreshCw size={13} /> Re-check</button>} />

      {q.isLoading && <Panel><div className="loading">Analyzing inventory…</div></Panel>}

      {r && (
        <>
          <div className="kpi-grid" style={{ marginBottom: 16 }}>
            <Kpi label="Devices analyzed" value={r.total_devices} icon={ClipboardCheck} />
            <Kpi label="Issue types" value={r.issue_count} tone={r.issue_count ? 'warn' : 'ok'} icon={TriangleAlert} />
            <Kpi label="Last checked" value={timeAgo(r.generated_at)} />
          </div>

          {r.clean && <Panel><EmptyState icon={CircleCheck} title="No data quality issues" message="Every device is classified, located and recently seen, with no duplicates or conflicts." /></Panel>}

          <div className="stack">
            {r.issues.map((iss) => {
              const sev = SEV[iss.severity] ?? SEV.info
              const SIcon = sev.icon
              const isOpen = open === iss.key
              return (
                <Panel key={iss.key}
                  title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><SIcon size={15} /> {iss.label}</span>}
                  subtitle={<span className={`badge ${sev.cls}`}>{iss.count}</span>}
                  actions={<button className="btn btn-xs btn-ghost" onClick={() => setOpen(isOpen ? null : iss.key)}>
                    <ChevronRight size={14} style={{ transform: isOpen ? 'rotate(90deg)' : 'none', transition: 'transform .15s' }} /> {isOpen ? 'Hide' : `Show ${Math.min(iss.count, 100)}`}
                  </button>}
                >
                  <p className="muted" style={{ fontSize: 13, marginBottom: isOpen ? 12 : 0 }}>{iss.description}</p>
                  {isOpen && (
                    <table className="data-table">
                      <thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Vendor</th>{iss.devices.some((d) => d.note) && <th>Note</th>}</tr></thead>
                      <tbody>
                        {iss.devices.map((d) => {
                          const base = detailBase[d.category]
                          return (
                            <tr key={d.id + (d.note ?? '')}>
                              <td>{base ? <Link className="cell-name" to={`${base}/${d.id}`}>{d.name}</Link> : <span className="cell-name">{d.name}</span>}</td>
                              <td className="mono">{d.primary_ip || '—'}</td>
                              <td>{d.category.replace(/_/g, ' ')}</td>
                              <td>{d.vendor || '—'}</td>
                              {iss.devices.some((x) => x.note) && <td className="muted">{d.note || ''}</td>}
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  )}
                </Panel>
              )
            })}
          </div>
        </>
      )}
    </div>
  )
}
