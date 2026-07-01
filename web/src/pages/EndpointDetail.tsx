import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { MonitorSmartphone, Cpu, MemoryStick, HardDrive, Boxes, AlertTriangle, Settings, LayoutDashboard, Clock, KeyRound, Cable, Server, ShieldCheck, Activity, Network, Laptop } from 'lucide-react'
import { api, type OSInventoryBundle, type Device } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import { ConnectivityPanel } from '../components/ConnectivityPanel'
import { ClassificationCard } from '../components/ClassificationCard'
import { OSInventorySection, EventHealth, CollectOSButton, diskMediaRollup } from '../components/DeepOSInventory'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'
import { CredentialBindSelect } from '../components/CredentialBindSelect'
import { Panel, Kpi, EmptyState, TabBar, DefList } from '../components/ui'
import { useIsVirtual } from '../components/useIsVirtual'

type Tab = 'overview' | 'hardware' | 'storage' | 'network' | 'software' | 'operations'

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
const shortOS = (s?: string | null) => (s ? s.replace(/^Microsoft\s+/i, '') : '—')

// EndpointDetail — the user-computer / workstation console (category "endpoint"). A
// workstation's value is its deep OS inventory (OS, hardware, disks, network, software,
// services, event health) plus classification and operations. Fully tabbed:
// Overview / Hardware / Storage / Network / Software / Operations. Re-scan / Repair check /
// credential binding come from the shared DeviceHeader (identical to every device-detail page).
export function EndpointDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const [tab, setTab] = useState<Tab>('overview')
  const isVirtual = useIsVirtual(deviceId)

  // Shares the query key with DeepOSInventory / useOSInventory → react-query fetches once.
  const osq = useQuery({ queryKey: ['os-inventory', deviceId], queryFn: () => api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`) })
  const dev = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const row = (dev.data ?? []).find((d) => d.id === deviceId)
  const b = osq.data
  const inv = b?.inventory ?? null
  const diskTotal = (b?.disks ?? []).reduce((a, d) => a + (d.total_bytes ?? 0), 0)
  const diskFree = (b?.disks ?? []).reduce((a, d) => a + (d.free_bytes ?? 0), 0)
  const crit = inv?.events_critical_24h ?? null
  const err24 = inv?.events_error_24h ?? null

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'hardware', label: 'Hardware', icon: Cpu },
    { key: 'storage', label: 'Storage', icon: HardDrive, count: b?.disks.length || undefined },
    { key: 'network', label: 'Network', icon: Cable, count: b?.nics.length || undefined },
    { key: 'software', label: 'Software', icon: Boxes, count: b?.software.length || undefined },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  const collectAction = <CollectOSButton deviceId={deviceId} isVirtual={isVirtual} small />

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Laptop} showCredential={false} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {/* ── OVERVIEW ──────────────────────────────────────────────────────── */}
      {tab === 'overview' && (
        <>
          <div className="kpi-grid kpi-6">
            <Kpi label="Operating system" value={shortOS(inv?.os_caption)} icon={MonitorSmartphone} tone="info" sub={inv?.os_build ? `build ${inv.os_build}` : (inv?.os_version || undefined)} onClick={() => setTab('hardware')} />
            <Kpi label="CPU" value={inv?.cpu_cores ? `${inv.cpu_cores} cores` : '—'} icon={Cpu} tone="default" sub={inv?.cpu_model || undefined} onClick={() => setTab('hardware')} />
            <Kpi label="Memory" value={fmtBytes(inv?.ram_total_bytes)} icon={MemoryStick} tone="default" sub="installed RAM" onClick={() => setTab('hardware')} />
            <Kpi label="Disk free" value={fmtBytes(diskFree)} icon={HardDrive} tone={diskTotal && diskFree / diskTotal < 0.1 ? 'crit' : diskTotal && diskFree / diskTotal < 0.2 ? 'warn' : 'default'} sub={b ? `${b.disks.length} volume${b.disks.length === 1 ? '' : 's'} · ${fmtBytes(diskTotal)}` : undefined} onClick={() => setTab('storage')} />
            <Kpi label="Software" value={b ? b.software.length : '—'} icon={Boxes} tone="default" sub="installed packages" onClick={() => setTab('software')} />
            <Kpi label="Critical events" value={crit ?? '—'} icon={AlertTriangle} tone={crit && crit > 0 ? 'crit' : err24 && err24 > 0 ? 'warn' : 'default'} sub="last 24h" />
          </div>

          <div className="grid-2" style={{ alignItems: 'start', marginBottom: 16 }}>
            <ClassificationCard deviceId={deviceId} />
            <Panel title="System" icon={Clock} actions={collectAction}>
              {!inv
                ? <EmptyState icon={MonitorSmartphone} title={isVirtual ? 'Manually modeled workstation' : 'No OS inventory yet'} message={isVirtual ? 'OS, NICs, disks and software are maintained manually — use Edit virtual device to update them.' : 'Bind a working credential (WinRM for Windows, SSH for Linux) and Collect OS to populate OS, hardware, disks, services and software.'} />
                : (
                  <DefList items={[
                    { label: 'Hostname', value: inv.hostname || '—' },
                    { label: 'Domain / FQDN', value: inv.fqdn || inv.domain || inv.workgroup || '—' },
                    { label: 'Logged-on user', value: inv.logged_on_user || '—' },
                    { label: 'Manufacturer / model', value: [inv.manufacturer, inv.model].filter(Boolean).join(' ') || '—' },
                    { label: 'Serial', value: <span className="mono">{inv.serial || '—'}</span> },
                    { label: 'Uptime', value: fmtUptime(inv.uptime_seconds) },
                    { label: 'Collected via', value: <span className="muted" style={{ fontSize: 12 }}>{inv.collection_method} · {new Date(inv.collected_at).toLocaleString()}</span> },
                  ]} />
                )}
            </Panel>
          </div>

          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="Event health (24h)" icon={ShieldCheck}>
              <EventHealth deviceId={deviceId} />
            </Panel>
            <Panel title="Connectivity" icon={Network} subtitle="switch / port / VLAN">
              <ConnectivityPanel deviceId={deviceId} bare />
            </Panel>
          </div>
        </>
      )}

      {/* ── HARDWARE ──────────────────────────────────────────────────────── */}
      {tab === 'hardware' && (
        <>
          <div className="kpi-grid kpi-4">
            <Kpi label="CPU" value={inv?.cpu_cores ? `${inv.cpu_cores} cores` : '—'} icon={Cpu} sub={inv?.cpu_sockets ? `${inv.cpu_sockets} socket(s)` : undefined} />
            <Kpi label="Memory" value={fmtBytes(inv?.ram_total_bytes)} icon={MemoryStick} sub={inv?.ram_slots ? `${inv.ram_slots} slot(s)` : 'installed'} />
            <Kpi label="Model" value={row?.model || inv?.model || '—'} icon={Server} sub={inv?.manufacturer || row?.vendor || undefined} />
            <Kpi label="BIOS" value={inv?.bios_version || '—'} icon={Activity} sub={inv?.bios_date || undefined} />
          </div>
          <Panel title="System & OS details" icon={Cpu} actions={collectAction}>
            <OSInventorySection deviceId={deviceId} section="summary" isVirtual={isVirtual} />
          </Panel>
        </>
      )}

      {/* ── STORAGE ───────────────────────────────────────────────────────── */}
      {tab === 'storage' && (
        <Panel title="Disks / Volumes" icon={HardDrive} subtitle={b?.disks.length ? `${b.disks.length} · ${fmtBytes(diskFree)} free of ${fmtBytes(diskTotal)}${diskMediaRollup(b.disks) ? ` · ${diskMediaRollup(b.disks)}` : ''}` : undefined} pad={false} actions={collectAction}>
          <div style={{ padding: 14 }}><OSInventorySection deviceId={deviceId} section="disks" isVirtual={isVirtual} /></div>
        </Panel>
      )}

      {/* ── NETWORK ───────────────────────────────────────────────────────── */}
      {tab === 'network' && (
        <>
          <Panel title="Network adapters" icon={Cable} subtitle={b?.nics.length ? `${b.nics.length}` : undefined} pad={false} actions={collectAction}>
            <div style={{ padding: 14 }}><OSInventorySection deviceId={deviceId} section="network" isVirtual={isVirtual} /></div>
          </Panel>
          <ConnectivityPanel deviceId={deviceId} />
        </>
      )}

      {/* ── SOFTWARE ──────────────────────────────────────────────────────── */}
      {tab === 'software' && (
        <>
          <Panel title="Installed software" icon={Boxes} subtitle={b?.software.length ? `${b.software.length}` : undefined} actions={collectAction}>
            <OSInventorySection deviceId={deviceId} section="software" isVirtual={isVirtual} />
          </Panel>
          <Panel title="Services" icon={Settings}>
            <OSInventorySection deviceId={deviceId} section="services" isVirtual={isVirtual} />
          </Panel>
          <Panel title="Top processes" icon={Activity}>
            <OSInventorySection deviceId={deviceId} section="processes" isVirtual={isVirtual} />
          </Panel>
        </>
      )}

      {/* ── OPERATIONS ────────────────────────────────────────────────────── */}
      {tab === 'operations' && (
        <>
          <Panel title="Collection Credential" icon={KeyRound}>
            <CredentialBindSelect deviceId={deviceId} />
            <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
              The credential HIMS uses for deep OS inventory (WinRM for Windows, SSH for Linux). After binding, use <strong>Collect OS</strong> below — or <strong>Re-scan this device</strong> in the header — to apply it.
            </p>
          </Panel>
          <DeviceCredentialHealth deviceId={deviceId} category="endpoint" />
          <DeviceOps deviceId={deviceId} />
        </>
      )}
    </div>
  )
}
