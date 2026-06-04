import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { FileChartColumn, Boxes, Activity, Tag, Radar, Download } from 'lucide-react'
import { api, type Device, type Location, type MonitoringOverviewRow, type DiscoveryJob, locationPaths } from '../api'
import { PageHeader, Panel, Kpi, BarList, TabBar, EmptyState, StatusPill, colorFor, timeAgo } from '../components/ui'

type View = 'inventory' | 'availability' | 'vendors' | 'discovery' | 'export'
const VIEWS: View[] = ['inventory', 'availability', 'vendors', 'discovery', 'export']

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
  ]

  return (
    <div>
      <PageHeader title="Reports" icon={FileChartColumn} subtitle="Inventory, availability, vendor and discovery analytics — exportable to CSV" />
      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as View)} />

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
        <Panel title="Export Center" icon={Download}>
          <p className="muted" style={{ marginBottom: 16 }}>Download current data as CSV for spreadsheets or external reporting.</p>
          <div className="row">
            <button className="btn btn-primary" onClick={() => downloadCSV('inventory.csv', [
              ['Name', 'IP', 'Category', 'Vendor', 'Model', 'OS', 'Status', 'VLAN', 'Class', 'Location'],
              ...devs.map((d) => [d.name, d.primary_ip, d.category, d.vendor, d.model, d.os_version, d.status, d.vlan, d.device_class, d.location_id ? (locPath[d.location_id] ?? '') : '']),
            ])}><Download size={15} /> Inventory ({devs.length})</button>
            <button className="btn" onClick={() => downloadCSV('vendors.csv', [['Vendor', 'Devices'], ...byVendor.map((v) => [v.label, v.value])])}><Download size={15} /> Vendor summary</button>
            <button className="btn" onClick={() => downloadCSV('discovery-scans.csv', [
              ['Scope', 'Status', 'Hosts', 'Found', 'Created'],
              ...jobList.map((j) => [j.scope_cidr, j.status, j.host_count, j.found_count, j.created_at]),
            ])}><Download size={15} /> Discovery scans</button>
          </div>
        </Panel>
      )}
    </div>
  )
}
