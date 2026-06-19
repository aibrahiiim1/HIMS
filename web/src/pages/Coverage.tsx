import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { BarChart3, ShieldCheck, ClipboardList, Bell, HardDrive, Download } from 'lucide-react'
import { api, type CoverageReport, type DataQualityReport, type CovCount } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, TabBar } from '../components/ui'

const API = '/api/v1'
const sevBadge = (s: string) => <span className={'badge ' + (s === 'critical' ? 'badge-crit' : s === 'warning' ? 'badge-warn' : 'badge-unknown')}>{s}</span>
const stateLink = (state: string) => `/inventory?management=${state}`
const roleLabel: Record<string, string> = {
  virtual_host_esxi: 'ESXi Host', virtual_host_hyperv: 'Hyper-V Host', virtual_machine: 'Virtual Machine',
  physical_server: 'Physical Server', unknown_server: 'Unknown Server',
}

function CovTable({ title, rows, hrefFor }: { title: string; rows: CovCount[]; hrefFor?: (r: CovCount) => string }) {
  if (!rows?.length) return null
  return (
    <Panel title={title} pad={false}>
      <table className="data-table">
        <thead><tr><th>{title}</th><th>Total</th><th>Managed</th><th>Unmanaged</th><th>Coverage</th></tr></thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.name}>
              <td className="cell-name">{hrefFor ? <Link to={hrefFor(r)}>{roleLabel[r.name] ?? r.name.replace(/_/g, ' ')}</Link> : (roleLabel[r.name] ?? r.name.replace(/_/g, ' '))}</td>
              <td>{r.total}</td><td>{r.managed}</td><td>{r.unmanaged}</td>
              <td className="muted">{r.total ? Math.round((r.managed / r.total) * 100) : 0}%</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  )
}

// Reporting & Coverage — consolidated operational visibility: Management, Data Quality, Alerts,
// Virtualization, and CSV export. All counts come from /reports/coverage + /data-quality (live).
export function Coverage() {
  const [tab, setTab] = useState('management')
  const cov = useQuery({ queryKey: ['coverage'], queryFn: () => api.get<CoverageReport>('/reports/coverage') })
  const dq = useQuery({ queryKey: ['data-quality'], queryFn: () => api.get<DataQualityReport>('/data-quality'), enabled: tab === 'dq' })
  const c = cov.data

  const tabs = [
    { key: 'management', label: 'Management Coverage', icon: ShieldCheck },
    { key: 'dq', label: 'Data Quality', icon: ClipboardList, count: dq.data?.issue_count },
    { key: 'alerts', label: 'Alerts Coverage', icon: Bell },
    { key: 'virt', label: 'Virtualization', icon: HardDrive },
    { key: 'export', label: 'Export', icon: Download },
  ]

  return (
    <div>
      <PageHeader title="Reporting & Coverage" icon={BarChart3} subtitle="What is managed, what is not, why, and what needs action — live operational visibility" />
      {cov.isLoading && <div className="loading">Loading coverage…</div>}
      <TabBar tabs={tabs} active={tab} onChange={setTab} />

      {tab === 'management' && c && (
        <>
          <div className="kpi-grid">
            <Kpi label="Total devices" value={c.management.total} icon={BarChart3} tone="info" />
            <Kpi label="Managed" value={c.management.managed} icon={ShieldCheck} tone="ok" sub={`${c.management.total ? Math.round((c.management.managed / c.management.total) * 100) : 0}%`} />
            <Kpi label="Credential failed" value={c.management.by_state['credential_failed'] ?? 0} tone={(c.management.by_state['credential_failed'] ?? 0) > 0 ? 'crit' : 'default'} />
            <Kpi label="Not authorized" value={c.management.by_state['not_authorized'] ?? 0} tone="warn" />
            <Kpi label="Collection failed" value={c.management.by_state['collection_failed'] ?? 0} tone="warn" />
            <Kpi label="Needs agent" value={c.management.by_state['needs_agent'] ?? 0} tone="default" />
          </div>
          <Panel title="By management state" pad={false}>
            <table className="data-table">
              <thead><tr><th>State</th><th>Count</th></tr></thead>
              <tbody>
                {Object.entries(c.management.by_state).sort((a, b) => b[1] - a[1]).map(([s, n]) => (
                  <tr key={s}><td className="cell-name"><Link to={stateLink(s)}>{s.replace(/_/g, ' ')}</Link></td><td>{n}</td></tr>
                ))}
              </tbody>
            </table>
          </Panel>
          <div className="grid-2" style={{ alignItems: 'start' }}>
            <CovTable title="By server role" rows={c.management.by_role} hrefFor={() => '/servers'} />
            <CovTable title="By category" rows={c.management.by_category} hrefFor={(r) => `/inventory?category=${r.name}`} />
          </div>
          <CovTable title="By site" rows={c.management.by_site} />
        </>
      )}

      {tab === 'dq' && (
        <Panel title="Data Quality issues" icon={ClipboardList} subtitle={dq.data ? `${dq.data.issue_count}` : undefined} pad={false}>
          {dq.isLoading && <div className="loading">Loading…</div>}
          {dq.data && dq.data.issues.length === 0 && <EmptyState icon={ShieldCheck} title="No data-quality issues" message="Every device has complete, consistent inventory." />}
          {dq.data && dq.data.issues.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Issue</th><th>Severity</th><th>Count</th><th>Sample devices</th></tr></thead>
              <tbody>
                {dq.data.issues.map((i) => (
                  <tr key={i.key}>
                    <td className="cell-name" title={i.description}>{i.label}</td>
                    <td>{sevBadge(i.severity)}</td>
                    <td>{i.count}</td>
                    <td className="muted" style={{ fontSize: 12 }}>
                      {i.devices.slice(0, 4).map((d, idx) => (
                        <span key={d.id}>{idx > 0 && ', '}<Link to={`/devices/${d.id}`}>{d.name}</Link></span>
                      ))}{i.devices.length > 4 && ` +${i.count - 4} more`}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'alerts' && c && (
        <>
          <div className="kpi-grid">
            <Kpi label="Critical open" value={c.alerts.by_severity['critical'] ?? 0} icon={Bell} tone={(c.alerts.by_severity['critical'] ?? 0) > 0 ? 'crit' : 'default'} />
            <Kpi label="Warning open" value={c.alerts.by_severity['warning'] ?? 0} icon={Bell} tone="warn" />
            <Kpi label="Check / State" value={`${c.alerts.by_kind['check'] ?? 0} / ${c.alerts.by_kind['state'] ?? 0}`} icon={Bell} />
            <Kpi label="Stale (>24h)" value={c.alerts.stale} icon={Bell} />
            <Kpi label="Ack unresolved" value={c.alerts.acked_unresolved} icon={Bell} tone={c.alerts.acked_unresolved > 0 ? 'warn' : 'default'} />
            <Kpi label="Rules on/off" value={`${c.alerts.rules_enabled} / ${c.alerts.rules_disabled}`} icon={Bell} />
          </div>
          <Panel title="Open alerts by condition" pad={false}>
            <table className="data-table">
              <thead><tr><th>Condition</th><th>Count</th></tr></thead>
              <tbody>{Object.entries(c.alerts.by_condition).sort((a, b) => b[1] - a[1]).map(([k, n]) => (
                <tr key={k}><td className="cell-name"><Link to={`/alerts`}>{k.replace(/_/g, ' ')}</Link></td><td>{n}</td></tr>
              ))}</tbody>
            </table>
          </Panel>
          <Panel title="Down devices not alerted" icon={Bell} subtitle={`${(c.alerts.down_not_alerted ?? []).length}`} pad={false}>
            {(c.alerts.down_not_alerted ?? []).length === 0 && <EmptyState icon={ShieldCheck} title="None" message="Every down device has an alert (or is within threshold/suppressed)." />}
            {(c.alerts.down_not_alerted ?? []).length > 0 && (
              <table className="data-table">
                <thead><tr><th>Device</th><th>IP</th><th>Reason</th></tr></thead>
                <tbody>{(c.alerts.down_not_alerted ?? []).map((d) => (
                  <tr key={d.id}><td className="cell-name"><Link to={`/devices/${d.id}`}>{d.name}</Link></td><td className="mono">{d.ip}</td><td className="muted">{d.detail}</td></tr>
                ))}</tbody>
              </table>
            )}
          </Panel>
          {(c.alerts.flapping ?? []).length > 0 && (
            <Panel title="Flapping / repeated devices" subtitle={`${(c.alerts.flapping ?? []).length}`} pad={false}>
              <table className="data-table"><thead><tr><th>Device</th><th>Alerts</th></tr></thead>
                <tbody>{(c.alerts.flapping ?? []).map((d) => (<tr key={d.id}><td className="cell-name"><Link to={`/devices/${d.id}`}>{d.name}</Link></td><td className="muted">{d.detail}</td></tr>))}</tbody>
              </table>
            </Panel>
          )}
        </>
      )}

      {tab === 'virt' && c && (
        <>
          <div className="kpi-grid">
            <Kpi label="ESXi hosts" value={c.virtualization.esxi_hosts} icon={HardDrive} tone="info" />
            <Kpi label="Hyper-V hosts" value={c.virtualization.hyperv_hosts} icon={HardDrive} tone="info" />
            <Kpi label="VMs (linked/total)" value={`${c.virtualization.vms_linked}/${c.virtualization.vms_total}`} icon={HardDrive} sub={`${c.virtualization.vms_unlinked} unlinked`} />
            <Kpi label="Datastores warn/crit" value={`${c.virtualization.datastores_warn}/${c.virtualization.datastores_crit}`} tone={c.virtualization.datastores_crit > 0 ? 'crit' : c.virtualization.datastores_warn > 0 ? 'warn' : 'default'} />
            <Kpi label="Stale virt hosts" value={c.virtualization.stale_virt_hosts} tone={c.virtualization.stale_virt_hosts > 0 ? 'warn' : 'default'} />
          </div>
          <Panel title="Virtualization hosts" icon={HardDrive} subtitle={`${c.virtualization.hosts.length}`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Host</th><th>IP</th><th>Type</th><th>VMs</th><th>Running</th><th>Health</th></tr></thead>
              <tbody>{c.virtualization.hosts.map((h) => (
                <tr key={h.id}>
                  <td className="cell-name"><Link to={`/virtual-hosts/${h.id}`}>{h.name}</Link></td>
                  <td className="mono">{h.ip}</td>
                  <td>{h.type === 'hyperv' ? 'Hyper-V' : 'ESXi'}</td>
                  <td>{h.vm_count}</td><td>{h.running}</td>
                  <td><span className={'badge ' + (h.health === 'ok' ? 'badge-up' : h.health === 'failed' ? 'badge-crit' : 'badge-unknown')}>{h.health}</span></td>
                </tr>
              ))}</tbody>
            </table>
          </Panel>
        </>
      )}

      {tab === 'export' && (
        <Panel title="Export reports (CSV)" icon={Download}>
          <div className="stack" style={{ gap: 10 }}>
            <p className="muted" style={{ fontSize: 13 }}>Download the current live reports as CSV.</p>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <a className="btn btn-sm" href={`${API}/reports/coverage/export?section=management&format=csv`}><Download size={14} /> Management coverage</a>
              <a className="btn btn-sm" href={`${API}/reports/coverage/export?section=virtualization&format=csv`}><Download size={14} /> Virtualization coverage</a>
              <a className="btn btn-sm" href={`${API}/reports/coverage/export?section=alerts&format=csv`}><Download size={14} /> Alerts coverage</a>
              <a className="btn btn-sm" href={`${API}/reports/inventory/export?format=csv`}><Download size={14} /> Full inventory</a>
              <a className="btn btn-sm" href={`${API}/reports/inventory/export?format=xlsx`}><Download size={14} /> Inventory (Excel)</a>
            </div>
          </div>
        </Panel>
      )}
    </div>
  )
}
