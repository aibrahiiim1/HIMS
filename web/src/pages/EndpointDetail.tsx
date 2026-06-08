import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Laptop, MonitorSmartphone, Cpu, MemoryStick, HardDrive, Boxes, AlertTriangle, Settings, LayoutDashboard, Clock } from 'lucide-react'
import { api, type OSInventoryBundle } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { ClassificationCard } from '../components/ClassificationCard'
import { DeepOSInventory } from '../components/DeepOSInventory'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'
import { Panel, Kpi, EmptyState, TabBar } from '../components/ui'

type Tab = 'overview' | 'operations'

function fmtBytes(n?: number | null): string {
  if (n == null || n === 0) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
function fmtUptime(s?: number | null): string {
  if (!s) return '—'
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600)
  return d > 0 ? `${d}d ${h}h` : `${h}h`
}
// shortOS trims "Microsoft Windows 11 Pro" → "Windows 11 Pro" for the KPI tile.
const shortOS = (s?: string | null) => (s ? s.replace(/^Microsoft\s+/i, '') : '—')

// EndpointDetail — the user-computer / workstation console (category "endpoint").
// Unlike the switch/server templates it has no ports/VLANs — a workstation's value
// is its deep OS inventory (OS, hardware, disks, services, software, event health)
// plus classification and operations. Re-scan / Repair check / credential binding
// come from the shared DeviceHeader (identical to every other device-detail page).
export function EndpointDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const [tab, setTab] = useState<Tab>('overview')

  // Shares the query key with DeepOSInventory → react-query fetches once.
  const osq = useQuery({ queryKey: ['os-inventory', deviceId], queryFn: () => api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`) })
  const b = osq.data
  const inv = b?.inventory ?? null
  const diskFree = (b?.disks ?? []).reduce((a, d) => a + (d.free_bytes ?? 0), 0)
  const crit = inv?.events_critical_24h ?? null

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Laptop} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {tab === 'overview' && (
        <>
          <div className="kpi-grid kpi-6">
            <Kpi label="Operating system" value={shortOS(inv?.os_caption)} icon={MonitorSmartphone} tone="info" sub={inv?.os_build ? `build ${inv.os_build}` : (inv?.os_version || undefined)} />
            <Kpi label="CPU" value={inv?.cpu_cores ? `${inv.cpu_cores} cores` : '—'} icon={Cpu} tone="default" sub={inv?.cpu_model || undefined} />
            <Kpi label="Memory" value={fmtBytes(inv?.ram_total_bytes)} icon={MemoryStick} tone="default" sub="installed RAM" />
            <Kpi label="Disk free" value={fmtBytes(diskFree)} icon={HardDrive} tone="default" sub={b ? `${b.disks.length} volume${b.disks.length === 1 ? '' : 's'}` : undefined} onClick={b?.disks.length ? () => setTab('overview') : undefined} />
            <Kpi label="Software" value={b ? b.software.length : '—'} icon={Boxes} tone="default" sub="installed packages" />
            <Kpi label="Critical events" value={crit ?? '—'} icon={AlertTriangle} tone={crit && crit > 0 ? 'crit' : 'default'} sub="last 24h" />
          </div>

          <div className="grid-2" style={{ alignItems: 'start', marginBottom: 16 }}>
            <ClassificationCard deviceId={deviceId} />
            <Panel title="At a glance" icon={Clock}>
              {!inv
                ? <EmptyState icon={MonitorSmartphone} title="No OS inventory yet" message="Bind a working credential (WinRM for Windows, SSH for Linux) and collect below to populate OS, hardware, disks, services and software." />
                : (
                  <div className="stack" style={{ gap: 8, fontSize: 13 }}>
                    <Row label="Hostname" value={inv.hostname || '—'} />
                    <Row label="Domain / FQDN" value={inv.fqdn || inv.domain || inv.workgroup || '—'} />
                    <Row label="Manufacturer / model" value={[inv.manufacturer, inv.model].filter(Boolean).join(' ') || '—'} />
                    <Row label="Serial" value={inv.serial || '—'} />
                    <Row label="Uptime" value={fmtUptime(inv.uptime_seconds)} />
                    <Row label="Collected via" value={`${inv.collection_method} · ${new Date(inv.collected_at).toLocaleString()}`} />
                  </div>
                )}
            </Panel>
          </div>

          <DeepOSInventory deviceId={deviceId} alwaysShow />
        </>
      )}

      {tab === 'operations' && (
        <>
          <DeviceCredentialHealth deviceId={deviceId} category="endpoint" />
          <DeviceOps deviceId={deviceId} />
        </>
      )}
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="row" style={{ justifyContent: 'space-between', gap: 12 }}>
      <span className="muted">{label}</span>
      <span style={{ textAlign: 'right', wordBreak: 'break-word' }}>{value}</span>
    </div>
  )
}
