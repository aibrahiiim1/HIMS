import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ClipboardCheck, ChevronRight, CircleCheck, TriangleAlert, Info, RefreshCw, MapPin } from 'lucide-react'
import { api, type DataQualityReport, type ReconcileSitesResult } from '../api'
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
                  {iss.key === 'missing_location' && <ReconcileSites onApplied={() => q.refetch()} />}
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

// ReconcileSites — assigns unassigned devices to a site by matching their IP
// against the declared site subnets (Locations → Subnets). Always previews
// (dry-run) before applying; only evidence-based matches are offered.
function ReconcileSites({ onApplied }: { onApplied: () => void }) {
  const [preview, setPreview] = useState<ReconcileSitesResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const run = async (dryRun: boolean) => {
    setBusy(true); setErr(null)
    try {
      const res = await api.post<ReconcileSitesResult>('/data-quality/reconcile-sites', { dry_run: dryRun })
      if (dryRun) { setPreview(res); setDone(null) }
      else { setDone(`Assigned ${res.updated ?? 0} device(s) to a site by subnet match.`); setPreview(null); onApplied() }
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)) }
    finally { setBusy(false) }
  }

  return (
    <div style={{ margin: '4px 0 12px', padding: 12, borderRadius: 8, background: 'var(--surface-2, rgba(125,125,125,.06))' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <MapPin size={15} />
        <span style={{ fontSize: 13, fontWeight: 600 }}>Assign by subnet</span>
        <span className="muted" style={{ fontSize: 12, flex: 1 }}>
          Match each unassigned device's IP to a declared site subnet. Devices whose IP matches no subnet stay unassigned.
        </span>
        <button className="btn btn-xs" disabled={busy} onClick={() => run(true)}>{busy && !preview ? 'Checking…' : 'Preview matches'}</button>
      </div>

      {err && <p style={{ color: 'var(--danger, #c0392b)', fontSize: 12, marginTop: 8 }}>{err}</p>}
      {done && <p style={{ color: 'var(--ok, #2e7d32)', fontSize: 12, marginTop: 8 }}><CircleCheck size={12} style={{ verticalAlign: -2 }} /> {done}</p>}

      {preview && (
        <div style={{ marginTop: 10 }}>
          {preview.matched === 0 ? (
            <p className="muted" style={{ fontSize: 12 }}>
              No unassigned devices fall within a declared subnet. Add the relevant CIDRs under Locations → Subnets, then preview again.
              {preview.unmatched > 0 && ` (${preview.unmatched} unassigned device(s) have no matching subnet.)`}
            </p>
          ) : (
            <>
              <p style={{ fontSize: 13, marginBottom: 6 }}>
                <strong>{preview.matched}</strong> device(s) match a declared subnet; <strong>{preview.unmatched}</strong> stay unassigned (no matching subnet).
              </p>
              <table className="data-table" style={{ marginBottom: 10 }}>
                <thead><tr><th>Site</th><th>Devices to assign</th></tr></thead>
                <tbody>
                  {preview.by_site.map((s) => (
                    <tr key={s.location_id}><td>{s.location_name}</td><td>{s.count}</td></tr>
                  ))}
                </tbody>
              </table>
              <button className="btn btn-xs btn-primary" disabled={busy} onClick={() => run(false)}>
                {busy ? 'Applying…' : `Assign ${preview.matched} device(s)`}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  )
}
