import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { FileChartColumn, Boxes, Activity, Tag, Radar, Download, Printer, FileSpreadsheet, CalendarClock, Plus, Trash2, Play } from 'lucide-react'
import { api, type Device, type Location, type MonitoringOverviewRow, type DiscoveryJob, type ReportSchedule, type NotificationChannel, locationPaths } from '../api'
import { PageHeader, Panel, Kpi, BarList, TabBar, EmptyState, StatusPill, colorFor, timeAgo } from '../components/ui'

const API_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'
const exportHref = (type: string, format: 'xlsx' | 'csv') => `${API_BASE}/reports/${type}/export?format=${format}`

type View = 'inventory' | 'availability' | 'vendors' | 'discovery' | 'export' | 'scheduled'
const VIEWS: View[] = ['inventory', 'availability', 'vendors', 'discovery', 'export', 'scheduled']

function groupCount<T>(items: T[], key: (t: T) => string): { label: string; value: number; color: string }[] {
  const m: Record<string, number> = {}
  for (const it of items) { const k = key(it); m[k] = (m[k] ?? 0) + 1 }
  return Object.entries(m).sort((a, b) => b[1] - a[1]).map(([label, value]) => ({ label, value, color: colorFor(label) }))
}

function downloadCSV(filename: string, rows: (string | number | null | undefined)[][]) {
  const esc = (v: string | number | null | undefined) => {
    const s = v == null ? '' : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const csv = rows.map((r) => r.map(esc).join(',')).join('\r\n')
  const blob = new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url; a.download = filename; a.click()
  URL.revokeObjectURL(url)
}

export function Reports() {
  const { view } = useParams<{ view: string }>()
  const initial = (VIEWS.includes(view as View) ? view : 'inventory') as View
  const [tab, setTab] = useState<View>(initial)

  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const mon = useQuery({ queryKey: ['mon-overview'], queryFn: () => api.get<MonitoringOverviewRow[]>('/monitoring/overview') })
  const jobs = useQuery({ queryKey: ['discovery-jobs'], queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  const devs = devices.data ?? []
  const byCategory = groupCount(devs, (d) => d.category.replace(/_/g, ' '))
  const byStatus = groupCount(devs, (d) => (d.status || 'unknown'))
  const byVendor = groupCount(devs, (d) => d.vendor || 'Unknown')
  const byLocation = groupCount(devs, (d) => (d.location_id ? (locPath[d.location_id] ?? '—') : '(unassigned)'))

  const monMap = new Map((mon.data ?? []).map((r) => [r.status, r.count]))
  const jobList = jobs.data ?? []

  const tabs = [
    { key: 'inventory', label: 'Inventory', icon: Boxes },
    { key: 'availability', label: 'Availability', icon: Activity },
    { key: 'vendors', label: 'Vendors', icon: Tag },
    { key: 'discovery', label: 'Discovery', icon: Radar },
    { key: 'export', label: 'Export Center', icon: Download },
    { key: 'scheduled', label: 'Scheduled', icon: CalendarClock },
  ]

  return (
    <div className="report-root">
      <PageHeader title="Reports" icon={FileChartColumn} subtitle="Inventory, availability, vendor and discovery analytics — export to Excel/CSV, print to PDF, or schedule by email"
        actions={<button className="btn btn-sm no-print" onClick={() => window.print()}><Printer size={14} /> Print / PDF</button>} />
      <div className="no-print"><TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as View)} /></div>

      {tab === 'inventory' && (
        <div>
          <div className="kpi-grid">
            <Kpi label="Total Devices" value={devs.length} icon={Boxes} tone="info" />
            <Kpi label="Categories" value={byCategory.length} icon={Tag} tone="default" />
            <Kpi label="Locations" value={byLocation.length} icon={Boxes} tone="default" />
            <Kpi label="Vendors" value={byVendor.length} icon={Tag} tone="default" />
          </div>
          <div className="grid-2">
            <Panel title="By Category"><BarList rows={byCategory} /></Panel>
            <Panel title="By Location"><BarList rows={byLocation} /></Panel>
          </div>
          <Panel title="By Status"><BarList rows={byStatus.map((r) => ({ ...r, color: ({ up: '#16a34a', down: '#dc2626', warning: '#d97706', needs_attention: '#d97706' } as Record<string, string>)[r.label] ?? '#94a3b8' }))} /></Panel>
        </div>
      )}

      {tab === 'availability' && (
        <div>
          <div className="kpi-grid">
            <Kpi label="Online" value={monMap.get('up') ?? 0} icon={Activity} tone="ok" />
            <Kpi label="Warning" value={monMap.get('warning') ?? 0} icon={Activity} tone="warn" />
            <Kpi label="Offline" value={monMap.get('down') ?? 0} icon={Activity} tone={(monMap.get('down') ?? 0) > 0 ? 'crit' : 'default'} />
            <Kpi label="Unknown" value={monMap.get('unknown') ?? 0} icon={Activity} tone="default" />
          </div>
          <Panel title="Device Availability" pad={false}>
            {devs.length === 0 ? <EmptyState icon={Activity} title="No devices" /> : (
              <table className="data-table">
                <thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Status</th></tr></thead>
                <tbody>
                  {devs.slice(0, 200).map((d) => (
                    <tr key={d.id}><td className="cell-name">{d.name}</td><td className="mono">{d.primary_ip ?? '—'}</td><td>{d.category.replace(/_/g, ' ')}</td><td><StatusPill status={d.status} /></td></tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </div>
      )}

      {tab === 'vendors' && (
        <Panel title="Devices by Vendor" icon={Tag} pad={false}>
          {byVendor.length === 0 ? <EmptyState icon={Tag} title="No devices" /> : (
            <table className="data-table">
              <thead><tr><th>Vendor</th><th>Devices</th><th>Share</th></tr></thead>
              <tbody>
                {byVendor.map((v) => (
                  <tr key={v.label}><td className="cell-name">{v.label}</td><td>{v.value}</td><td>{devs.length ? Math.round((v.value / devs.length) * 100) : 0}%</td></tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {tab === 'discovery' && (
        <div>
          <div className="kpi-grid">
            <Kpi label="Scans" value={jobList.length} icon={Radar} tone="info" />
            <Kpi label="Devices Found" value={jobList.reduce((a, j) => a + j.found_count, 0)} icon={Boxes} tone="default" />
            <Kpi label="Hosts Probed" value={jobList.reduce((a, j) => a + j.host_count, 0)} icon={Radar} tone="default" />
            <Kpi label="Failed" value={jobList.filter((j) => j.status === 'failed').length} icon={Radar} tone={jobList.some((j) => j.status === 'failed') ? 'crit' : 'default'} />
          </div>
          <Panel title="Scan History" pad={false}>
            {jobList.length === 0 ? <EmptyState icon={Radar} title="No scans run yet" /> : (
              <table className="data-table">
                <thead><tr><th>Scope</th><th>Status</th><th>Hosts</th><th>Found</th><th>When</th></tr></thead>
                <tbody>
                  {jobList.map((j) => (
                    <tr key={j.id}><td className="mono">{j.scope_cidr || '—'}</td><td><StatusPill status={j.status === 'completed' ? 'up' : j.status === 'failed' ? 'down' : 'warning'} label={j.status} /></td><td>{j.host_count}</td><td>{j.found_count}</td><td className="muted">{timeAgo(j.created_at)}</td></tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </div>
      )}

      {tab === 'export' && (
        <div>
          <Panel title="Server Reports — Excel & CSV" icon={FileSpreadsheet}>
            <p className="muted" style={{ marginBottom: 16 }}>Server-generated, multi-sheet reports built from live data. Excel files include a bold header + auto-filter on each sheet.</p>
            <table className="data-table">
              <thead><tr><th>Report</th><th>Contents</th><th>Download</th></tr></thead>
              <tbody>
                {[
                  { type: 'inventory', label: 'Inventory', desc: 'Devices + by category / vendor / status' },
                  { type: 'availability', label: 'Availability', desc: 'Monitoring status rollup' },
                  { type: 'vendors', label: 'Vendors', desc: 'Device count per vendor' },
                  { type: 'all', label: 'Full Report', desc: 'Inventory + availability combined' },
                ].map((r) => (
                  <tr key={r.type}>
                    <td className="cell-name">{r.label}</td>
                    <td className="muted">{r.desc}</td>
                    <td className="cell-actions">
                      <a className="btn btn-primary btn-xs" href={exportHref(r.type, 'xlsx')}><FileSpreadsheet size={12} /> Excel</a>
                      <a className="btn btn-ghost btn-xs" href={exportHref(r.type, 'csv')}><Download size={12} /> CSV</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
          <Panel title="Quick CSV (client-side)" icon={Download}>
            <div className="row">
              <button className="btn" onClick={() => downloadCSV('inventory.csv', [
                ['Name', 'IP', 'Category', 'Vendor', 'Model', 'OS', 'Status', 'VLAN', 'Class', 'Location'],
                ...devs.map((d) => [d.name, d.primary_ip, d.category, d.vendor, d.model, d.os_version, d.status, d.vlan, d.device_class, d.location_id ? (locPath[d.location_id] ?? '') : '']),
              ])}><Download size={15} /> Inventory ({devs.length})</button>
              <button className="btn" onClick={() => downloadCSV('discovery-scans.csv', [
                ['Scope', 'Status', 'Hosts', 'Found', 'Created'],
                ...jobList.map((j) => [j.scope_cidr, j.status, j.host_count, j.found_count, j.created_at]),
              ])}><Download size={15} /> Discovery scans</button>
            </div>
          </Panel>
        </div>
      )}

      {tab === 'scheduled' && <ScheduledReports />}
    </div>
  )
}

function ScheduledReports() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const [msg, setMsg] = useState('')
  const schedules = useQuery({ queryKey: ['report-schedules'], queryFn: () => api.get<ReportSchedule[]>('/report-schedules') })
  const channels = useQuery({ queryKey: ['notification-channels'], queryFn: () => api.get<NotificationChannel[]>('/notification-channels') })
  const inv = () => qc.invalidateQueries({ queryKey: ['report-schedules'] })

  const toggle = useMutation({ mutationFn: (s: ReportSchedule) => api.patch(`/report-schedules/${s.id}`, { enabled: !s.enabled }), onSuccess: inv })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/report-schedules/${id}`), onSuccess: inv })
  const run = useMutation({
    mutationFn: (id: string) => api.post<{ status: string }>(`/report-schedules/${id}/run`, {}),
    onSuccess: (r) => { setMsg('Run: ' + r.status); inv() },
    onError: (e) => setMsg((e as Error).message),
  })

  const list = schedules.data ?? []
  const emailChannels = (channels.data ?? []).filter((c) => c.type === 'email')

  return (
    <div className="no-print">
      <Panel title="Scheduled Reports" icon={CalendarClock} subtitle={`${list.length}`} pad={false}
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShow((v) => !v)}><Plus size={14} /> {show ? 'Cancel' : 'New schedule'}</button>}>
        {msg && <div className="enc-banner info" style={{ margin: 12 }}>{msg}</div>}
        {show && <ScheduleForm channels={emailChannels} onDone={() => { setShow(false); inv() }} />}
        {schedules.data && list.length === 0 && !show && (
          <EmptyState icon={CalendarClock} title="No scheduled reports"
            message="Schedule a report to be generated daily/weekly/monthly and emailed via an email notification channel. Configure an email channel under Administration → Notifications first." />
        )}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Report</th><th>Frequency</th><th>Hour (UTC)</th><th>Channel</th><th>Last run</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.id}>
                  <td className="cell-name">{s.name}</td>
                  <td>{s.report_type}</td>
                  <td>{s.frequency}</td>
                  <td>{String(s.hour_utc).padStart(2, '0')}:00</td>
                  <td>{s.channel_id ? <span className="badge badge-up">email</span> : <span className="badge badge-unknown">generate only</span>}</td>
                  <td className="muted">{s.last_run_at ? `${timeAgo(s.last_run_at)} · ${s.last_status}` : 'never'}</td>
                  <td>{s.enabled ? <span className="badge badge-up">on</span> : <span className="badge badge-disabled">off</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" disabled={run.isPending} onClick={() => { setMsg(''); run.mutate(s.id) }}><Play size={12} /> Run now</button>
                    <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(s)}>{s.enabled ? 'Disable' : 'Enable'}</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(s.id)}><Trash2 size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}

function ScheduleForm({ channels, onDone }: { channels: NotificationChannel[]; onDone: () => void }) {
  const [name, setName] = useState('')
  const [reportType, setReportType] = useState('inventory')
  const [frequency, setFrequency] = useState('weekly')
  const [hourUTC, setHourUTC] = useState(6)
  const [channelID, setChannelID] = useState('')
  const m = useMutation({
    mutationFn: () => api.post('/report-schedules', { name, report_type: reportType, frequency, hour_utc: hourUTC, channel_id: channelID || null }),
    onSuccess: onDone,
  })
  return (
    <div style={{ padding: 12, borderBottom: '1px solid var(--border)' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(160px,1fr))', gap: 10 }}>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} placeholder="Weekly inventory" /></label>
        <label className="form-field">Report
          <select className="field" value={reportType} onChange={(e) => setReportType(e.target.value)}>
            <option value="inventory">Inventory</option><option value="availability">Availability</option><option value="vendors">Vendors</option><option value="all">Full report</option>
          </select>
        </label>
        <label className="form-field">Frequency
          <select className="field" value={frequency} onChange={(e) => setFrequency(e.target.value)}>
            <option value="daily">Daily</option><option value="weekly">Weekly</option><option value="monthly">Monthly</option>
          </select>
        </label>
        <label className="form-field">Hour (UTC)<input className="field" type="number" min={0} max={23} value={hourUTC} onChange={(e) => setHourUTC(Number(e.target.value))} /></label>
        <label className="form-field">Email channel
          <select className="field" value={channelID} onChange={(e) => setChannelID(e.target.value)}>
            <option value="">(generate only)</option>
            {channels.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </label>
      </div>
      <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Emails the report summary via the chosen email channel; the full file stays downloadable in Export Center.</p>
      <button className="btn btn-primary" style={{ marginTop: 8 }} disabled={!name || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create schedule'}</button>
      {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
    </div>
  )
}
