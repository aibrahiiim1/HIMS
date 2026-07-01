import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { Server, Cpu, HardDrive, Cable, Activity, Settings, LayoutDashboard, Gauge, Thermometer, MemoryStick, KeyRound, Boxes, Box, MonitorSmartphone } from 'lucide-react'
import { api, type ServerStorage, type DeviceFact, type DeviceRole, type Interface, type BMCInfo, type BMCSensor, type BMCComponent, type Device } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { ConnectivityPanel } from '../components/ConnectivityPanel'
import { ClassificationEvidencePanel } from '../components/ClassificationEvidence'
import { OSInventorySection, CollectOSButton, useOSInventory, DiskTypeBadge, diskMediaRollup } from '../components/DeepOSInventory'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'
import { CredentialBindSelect } from '../components/CredentialBindSelect'
import { Panel, Kpi, DefList, EmptyState, StatusPill, Meter, TabBar } from '../components/ui'

type Tab = 'overview' | 'hardware' | 'storage' | 'network' | 'software' | 'operations'

const healthTone = (h?: string | null): 'up' | 'down' | 'warning' | 'unknown' =>
  h === 'OK' ? 'up' : h === 'Critical' ? 'down' : h === 'Warning' ? 'warning' : 'unknown'
const healthKpiTone = (h?: string | null): 'ok' | 'crit' | 'warn' | 'default' =>
  h === 'OK' ? 'ok' : h === 'Critical' ? 'crit' : h === 'Warning' ? 'warn' : 'default'

// Virtualization role → operator-facing badge (drives the "Physical vs VM" strip). A guest VM
// links to its parent hypervisor when known (hosted_on).
const ROLE_META: Record<string, { label: string; icon: typeof Server; bg: string }> = {
  physical_server: { label: 'Physical Server', icon: Server, bg: '#0e7490' },
  virtual_machine: { label: 'Virtual Machine', icon: Box, bg: '#7c3aed' },
  virtual_host_esxi: { label: 'ESXi Host', icon: Boxes, bg: '#166534' },
  virtual_host_hyperv: { label: 'Hyper-V Host', icon: Boxes, bg: '#1e3a8a' },
  unknown_server: { label: 'Unknown Server', icon: Server, bg: '#6b7280' },
}

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const speedLabel = (mbps?: number | null) => (mbps ? (mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`) : '—')
const fmtGiB = (bytes?: number | null) => (bytes && bytes > 0 ? fmtBytes(bytes) : '—')

// ServerDetail — enterprise server console (HOST-RESOURCES-MIB + Redfish/iLO/iDRAC + deep OS
// inventory). Tabbed: Overview (identity/virtualization + resources), Hardware/BMC, Storage,
// Network (interfaces + switch port map), Software (deep OS), Operations. A physical-vs-VM strip
// makes virtualization first-class and links a guest to its hypervisor. Re-scan / Repair check /
// credential binding come from the shared DeviceHeader.
export function ServerDetail({ initialTab }: { initialTab?: Tab } = {}) {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const [tab, setTab] = useState<Tab>(initialTab ?? 'overview')

  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const roles = useQuery({ queryKey: ['roles', id], queryFn: () => api.get<DeviceRole[]>(`/devices/${id}/roles`) })
  const storage = useQuery({ queryKey: ['storage', id], queryFn: () => api.get<ServerStorage[]>(`/devices/${id}/storage`) })
  const ifaces = useQuery({ queryKey: ['interfaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })
  const bmc = useQuery({ queryKey: ['bmc', id], queryFn: () => api.get<BMCInfo>(`/devices/${id}/bmc`) })
  const sensors = useQuery({ queryKey: ['bmc-sensors', id], queryFn: () => api.get<BMCSensor[]>(`/devices/${id}/bmc-sensors`) })
  const components = useQuery({ queryKey: ['bmc-components', id], queryFn: () => api.get<BMCComponent[]>(`/devices/${id}/bmc-components`) })
  const dev = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const row = (dev.data ?? []).find((d) => d.id === deviceId)
  // Deep OS inventory (shared query key) — the fallback source for CPU/RAM/storage on hosts
  // collected over WinRM/SSH, which have no SNMP HOST-RESOURCES facts.
  const { bundle: osb } = useOSInventory(deviceId)
  const osInv = osb?.inventory ?? null
  const osDisks = osb?.disks ?? []

  const fm = useMemo(() => new Map((facts.data ?? []).map((f) => [f.key, f.value ?? ''])), [facts.data])
  const num = (k: string) => { const v = Number(fm.get(k)); return Number.isFinite(v) && fm.has(k) ? v : null }
  const cpu = num('cpu.load_pct')
  const memUsed = num('memory.used_bytes'), memTotalSnmp = num('memory.total_bytes')
  const memPct = memUsed != null && memTotalSnmp ? Math.round((memUsed / memTotalSnmp) * 100) : num('memory.used_pct')
  const memTotal = memTotalSnmp ?? osInv?.ram_total_bytes ?? null

  const vols = storage.data ?? []
  const volPct = (s: ServerStorage) => (s.total_bytes && s.used_bytes ? Math.round((s.used_bytes / s.total_bytes) * 100) : null)
  // Storage effective values fall back to OS-inventory volumes when SNMP HOST-RESOURCES is absent.
  const osDiskTotal = osDisks.reduce((a, d) => a + (d.total_bytes ?? 0), 0)
  const osDiskFree = osDisks.reduce((a, d) => a + (d.free_bytes ?? 0), 0)
  const osWorst = osDisks.reduce<number | null>((acc, d) => {
    if (d.total_bytes && d.free_bytes != null) { const p = Math.round((1 - d.free_bytes / d.total_bytes) * 100); return acc == null || p > acc ? p : acc }
    return acc
  }, null)
  const worstVol = vols.reduce<number | null>((acc, s) => { const p = volPct(s); return p != null && (acc == null || p > acc) ? p : acc }, null) ?? osWorst
  const totalCap = vols.reduce((a, s) => a + (s.total_bytes ?? 0), 0) || osDiskTotal
  const diskCount = vols.length || osDisks.length
  const mediaRollup = diskMediaRollup(osDisks)

  const ifList = ifaces.data ?? []
  const roleList = roles.data ?? []
  const hasBMC = !!(bmc.data && bmc.data.device_id)
  const sensorList = sensors.data ?? []
  const badSensors = sensorList.filter((s) => s.status && s.status !== 'OK').length
  const comps = components.data ?? []
  const compsOf = (kind: string) => comps.filter((c) => c.kind === kind)
  const cpus = compsOf('cpu'), dimms = compsOf('memory'), controllers = compsOf('controller'), volumes = compsOf('volume'), drives = compsOf('drive')

  const rm = ROLE_META[row?.server_role ?? ''] ?? null
  const isVM = row?.server_role === 'virtual_machine'

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'hardware', label: 'Hardware', icon: Thermometer, count: hasBMC ? (sensorList.length || undefined) : undefined },
    { key: 'storage', label: 'Storage', icon: HardDrive, count: vols.length || undefined },
    { key: 'network', label: 'Network', icon: Cable, count: ifList.length || undefined },
    { key: 'software', label: 'Software', icon: Boxes },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={isVM ? Box : Server} showCredential={false} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {/* ── OVERVIEW ──────────────────────────────────────────────────────── */}
      {tab === 'overview' && (
        <>
          {/* Virtualization / role strip — physical vs VM is first-class; a guest links to its host. */}
          {rm && (
            <div className="card" style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', padding: '12px 16px' }}>
              <span className="badge" style={{ background: rm.bg, color: '#fff', display: 'inline-flex', gap: 6, alignItems: 'center', padding: '4px 10px', fontSize: 13 }}>
                <rm.icon size={14} /> {rm.label}
              </span>
              {(row?.vendor || row?.model) && <span className="muted" style={{ fontSize: 13 }}>{[row?.vendor, row?.model].filter(Boolean).join(' · ')}</span>}
              {isVM && (row?.hosted_on
                ? <span style={{ fontSize: 13 }}>hosted on <Link to={`/virtual-hosts/${row.hosted_on.id}`}>{row.hosted_on.name || row.hosted_on.ip}</Link></span>
                : <span className="muted" style={{ fontSize: 13 }}>parent hypervisor not yet linked</span>)}
              {row?.server_role === 'unknown_server' && <span className="muted" style={{ fontSize: 12 }}>OS not yet identified — bind a credential and collect to classify physical vs virtual.</span>}
            </div>
          )}

          <div className="kpi-grid kpi-6">
            <Kpi label="CPU load" value={cpu != null ? `${cpu}%` : '—'} icon={Cpu} tone={cpu != null && cpu >= 90 ? 'crit' : cpu != null && cpu >= 75 ? 'warn' : 'default'} sub="utilisation" />
            <Kpi label="Memory" value={memPct != null ? `${memPct}%` : '—'} icon={MemoryStick} tone={memPct != null && memPct >= 90 ? 'crit' : memPct != null && memPct >= 75 ? 'warn' : 'default'} sub={memTotal ? fmtBytes(memTotal) : 'used'} />
            <Kpi label="Storage used" value={worstVol != null ? `${worstVol}%` : '—'} icon={HardDrive} tone={worstVol != null && worstVol >= 90 ? 'crit' : worstVol != null && worstVol >= 75 ? 'warn' : 'default'} sub={totalCap ? `${fmtBytes(totalCap)} total` : 'busiest volume'} onClick={diskCount ? () => setTab('storage') : undefined} />
            <Kpi label="Volumes" value={diskCount} icon={HardDrive} tone="default" sub={mediaRollup || undefined} onClick={diskCount ? () => setTab('storage') : undefined} />
            <Kpi label="Interfaces" value={ifList.length} icon={Cable} tone="default" onClick={ifList.length ? () => setTab('network') : undefined} />
            <Kpi label="Hardware" value={hasBMC ? (bmc.data?.health ?? 'unknown') : '—'} icon={Thermometer} tone={hasBMC ? healthKpiTone(bmc.data?.health) : 'default'} sub={hasBMC ? (badSensors > 0 ? `${badSensors} sensor alerts` : 'BMC OK') : 'no BMC'} onClick={hasBMC ? () => setTab('hardware') : undefined} />
          </div>

          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="Resource Summary" icon={Gauge}>
              {roleList.length > 0 && (
                <div style={{ marginBottom: 12 }}>
                  <span className="muted" style={{ fontSize: 12 }}>Roles: </span>
                  {roleList.map((r) => <span key={r.role} className="badge badge-access" style={{ marginRight: 6 }}>{r.role}</span>)}
                </div>
              )}
              {(cpu == null && memPct == null && worstVol == null)
                ? <EmptyState icon={Gauge} title="No resource gauges" message="CPU / memory utilisation come from SNMP (HOST-RESOURCES-MIB); storage also from deep OS inventory. Bind a credential and collect to populate them." />
                : (
                  <div className="stack" style={{ gap: 14 }}>
                    {cpu != null && <Meter label="CPU load" value={cpu} />}
                    {memPct != null && <Meter label="Memory used" value={memPct} />}
                    {worstVol != null && <Meter label={vols.length ? 'Busiest volume' : 'Busiest disk (OS inventory)'} value={worstVol} />}
                  </div>
                )}
            </Panel>
            <Panel title="System Facts" icon={Server}>
              <DefList items={[
                { label: 'Type', value: rm ? rm.label : '—' },
                { label: 'Vendor / model', value: [row?.vendor, row?.model].filter(Boolean).join(' · ') || '—' },
                { label: 'Operating system', value: osInv?.os_caption || '—' },
                { label: 'CPU', value: osInv?.cpu_model ? `${osInv.cpu_model}${osInv.cpu_cores ? ` · ${osInv.cpu_cores} cores` : ''}` : (cpu != null ? `${cpu}% load` : '—') },
                { label: 'Memory total', value: fmtBytes(memTotal) },
                { label: 'Storage total', value: totalCap ? fmtBytes(totalCap) : '—' },
                { label: 'Disks', value: mediaRollup ? `${diskCount} · ${mediaRollup}` : (diskCount || '—') },
                { label: 'Interfaces', value: ifList.length },
              ]} />
            </Panel>
          </div>

          <ClassificationEvidencePanel deviceId={deviceId} />
        </>
      )}

      {/* ── HARDWARE / BMC ────────────────────────────────────────────────── */}
      {tab === 'hardware' && (
        <>
          {!hasBMC && (
            <>
              <EmptyState icon={Thermometer} title="No out-of-band controller" message="iLO / iDRAC / Redfish hardware health appears here when a BMC is discovered and a credential is bound. CPU / memory below come from OS inventory when collected." />
              <Panel title="Compute (from OS inventory)" icon={Cpu} actions={<CollectOSButton deviceId={deviceId} small />}>
                <OSInventorySection deviceId={deviceId} section="summary" />
              </Panel>
            </>
          )}
          {hasBMC && (
            <>
              <Panel title="Baseboard Management Controller" icon={Thermometer} actions={<StatusPill status={healthTone(bmc.data?.health)} label={bmc.data?.health ?? 'unknown'} />}>
                <DefList items={[
                  { label: 'Controller', value: `${bmc.data?.vendor ?? '—'} ${bmc.data?.controller_kind ?? ''}`.trim() },
                  { label: 'Model', value: bmc.data?.model ?? '—' },
                  { label: 'Serial', value: bmc.data?.serial ?? '—' },
                  { label: 'Firmware', value: bmc.data?.firmware_version ?? '—' },
                  { label: 'BIOS', value: bmc.data?.bios_version || '—' },
                  { label: 'Power state', value: bmc.data?.power_state ?? '—' },
                  { label: 'CPU', value: bmc.data?.cpu_model ? `${bmc.data.cpu_model}${bmc.data.cpu_count ? ` × ${bmc.data.cpu_count}` : ''}${bmc.data.cpu_cores ? ` · ${bmc.data.cpu_cores} cores` : ''}` : '—' },
                  { label: 'Memory', value: bmc.data?.memory_gib ? `${bmc.data.memory_gib} GiB` : '—' },
                ]} />
              </Panel>

              {cpus.length > 0 && (
                <Panel title="Processors" icon={Cpu} subtitle={`${cpus.length}`} pad={false}>
                  <table className="data-table">
                    <thead><tr><th>Socket</th><th>Model</th><th>Cores / Threads</th><th>Max speed</th><th>Status</th></tr></thead>
                    <tbody>{cpus.map((c) => (
                      <tr key={c.id}><td className="cell-name">{c.name}</td><td>{c.model || '—'}</td>
                        <td className="mono">{c.detail.cores || '—'}{c.detail.threads ? ` / ${c.detail.threads}` : ''}</td>
                        <td className="mono">{c.detail.max_speed_mhz ? `${c.detail.max_speed_mhz} MHz` : '—'}</td>
                        <td><StatusPill status={healthTone(c.status)} label={c.status ?? '—'} /></td></tr>
                    ))}</tbody>
                  </table>
                </Panel>
              )}

              {dimms.length > 0 && (
                <Panel title="Memory" icon={MemoryStick} subtitle={`${dimms.length} DIMM(s)${bmc.data?.memory_gib ? ` · ${bmc.data.memory_gib} GiB` : ''}`} pad={false}>
                  <table className="data-table">
                    <thead><tr><th>Slot</th><th>Size</th><th>Type</th><th>Speed</th><th>Part / Mfr</th><th>Status</th></tr></thead>
                    <tbody>{dimms.map((c) => (
                      <tr key={c.id}><td className="cell-name">{c.name}</td><td className="mono">{fmtGiB(c.capacity_bytes)}</td>
                        <td>{c.detail.type || '—'}</td><td className="mono">{c.detail.speed_mhz ? `${c.detail.speed_mhz} MHz` : '—'}</td>
                        <td className="muted" style={{ fontSize: 12 }}>{[c.model, c.detail.manufacturer].filter(Boolean).join(' · ') || '—'}</td>
                        <td><StatusPill status={healthTone(c.status)} label={c.status ?? '—'} /></td></tr>
                    ))}</tbody>
                  </table>
                </Panel>
              )}

              {(controllers.length > 0 || volumes.length > 0 || drives.length > 0) && (
                <Panel title="Storage / RAID" icon={HardDrive} subtitle={`${controllers.length} controller(s) · ${volumes.length} volume(s) · ${drives.length} drive(s)`} pad={false}>
                  <table className="data-table">
                    <thead><tr><th>Type</th><th>Name</th><th>Model / RAID</th><th>Capacity</th><th>Detail</th><th>Status</th></tr></thead>
                    <tbody>
                      {controllers.map((c) => (
                        <tr key={c.id}><td>controller</td><td className="cell-name">{c.name}</td><td>{c.model || '—'}</td><td className="mono">—</td>
                          <td className="muted" style={{ fontSize: 12 }}>{[c.detail.firmware ? `fw ${c.detail.firmware}` : '', c.detail.raid_types ? `RAID: ${c.detail.raid_types}` : ''].filter(Boolean).join(' · ') || '—'}</td>
                          <td><StatusPill status={healthTone(c.status)} label={c.status ?? '—'} /></td></tr>
                      ))}
                      {volumes.map((c) => (
                        <tr key={c.id}><td>volume</td><td className="cell-name">{c.name}</td><td><span className="badge badge-info">{c.detail.raid || '—'}</span></td>
                          <td className="mono">{fmtGiB(c.capacity_bytes)}</td><td className="muted" style={{ fontSize: 12 }}>—</td>
                          <td><StatusPill status={healthTone(c.status)} label={c.status ?? '—'} /></td></tr>
                      ))}
                      {drives.map((c) => (
                        <tr key={c.id}><td>drive</td><td className="cell-name">{c.name}</td><td>{c.model || '—'}{c.serial ? <span className="muted"> · {c.serial}</span> : null}</td>
                          <td className="mono">{fmtGiB(c.capacity_bytes)}</td>
                          <td className="muted" style={{ fontSize: 12 }}>{[c.detail.media, c.detail.protocol, c.detail.rpm ? `${c.detail.rpm} rpm` : ''].filter(Boolean).join(' · ') || '—'}</td>
                          <td><StatusPill status={healthTone(c.status)} label={c.status ?? '—'} /></td></tr>
                      ))}
                    </tbody>
                  </table>
                </Panel>
              )}

              <Panel title="Sensors" icon={Activity} subtitle={sensorList.length ? `${sensorList.length} · ${badSensors} alerting` : undefined} pad={false}>
                {sensorList.length === 0 && <EmptyState icon={Activity} title="No sensors reported" message="Thermal / power / fan sensors appear here when the BMC exposes them." />}
                {sensorList.length > 0 && (
                  <table className="data-table">
                    <thead><tr><th>Kind</th><th>Name</th><th>Status</th><th>Reading</th></tr></thead>
                    <tbody>
                      {[...sensorList].sort((a, b) => (a.status === 'OK' ? 1 : 0) - (b.status === 'OK' ? 1 : 0)).map((s) => (
                        <tr key={s.id}>
                          <td>{s.kind}</td>
                          <td className="cell-name">{s.name}</td>
                          <td><StatusPill status={healthTone(s.status)} label={s.status ?? '—'} /></td>
                          <td className="mono">{s.has_reading ? `${s.reading} ${s.unit ?? ''}` : '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </Panel>
            </>
          )}
        </>
      )}

      {/* ── STORAGE (SNMP volumes, else OS-inventory disks with media type) ─── */}
      {tab === 'storage' && (
        vols.length > 0 ? (
          <Panel title="Storage Volumes" icon={HardDrive} subtitle={`${vols.length} · ${fmtBytes(totalCap)} total (SNMP)`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Volume</th><th>Type</th><th>Total</th><th>Used</th><th>Utilisation</th></tr></thead>
              <tbody>
                {vols.map((s) => {
                  const pct = volPct(s)
                  return (
                    <tr key={s.id}>
                      <td className="cell-name">{s.descr ?? '—'}</td>
                      <td><span className="badge badge-unknown">{s.storage_type}</span></td>
                      <td className="mono">{fmtBytes(s.total_bytes)}</td>
                      <td className="mono">{fmtBytes(s.used_bytes)}</td>
                      <td style={{ minWidth: 180 }}>{pct != null ? <Meter value={pct} /> : '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </Panel>
        ) : osDisks.length > 0 ? (
          <Panel title="Disks / Volumes" icon={HardDrive} pad={false}
            subtitle={`${osDisks.length} · ${fmtBytes(osDiskFree)} free of ${fmtBytes(osDiskTotal)}${mediaRollup ? ` · ${mediaRollup}` : ''} (OS inventory)`}
            actions={<CollectOSButton deviceId={deviceId} small />}>
            <table className="data-table">
              <thead><tr><th>Volume</th><th>Type</th><th>FS</th><th>Total</th><th>Free</th><th>Utilisation</th></tr></thead>
              <tbody>
                {osDisks.map((d, i) => {
                  const pct = d.total_bytes && d.free_bytes != null ? Math.round((1 - d.free_bytes / d.total_bytes) * 100) : null
                  return (
                    <tr key={i}>
                      <td className="cell-name">{d.name}</td>
                      <td><DiskTypeBadge media={d.media_type} /></td>
                      <td>{d.filesystem || '—'}</td>
                      <td className="mono">{fmtBytes(d.total_bytes)}</td>
                      <td className="mono">{fmtBytes(d.free_bytes)}</td>
                      <td style={{ minWidth: 180 }}>{pct != null ? <Meter value={pct} /> : '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </Panel>
        ) : (
          <Panel title="Storage" icon={HardDrive}>
            {storage.isLoading ? <div className="loading">Loading…</div> : <EmptyState icon={HardDrive} title="No storage collected" message="Volumes come from HOST-RESOURCES-MIB (SNMP) or deep OS inventory. Bind a credential and collect to populate them — disk media type (SSD/NVMe/HDD) is detected during OS collection." />}
          </Panel>
        )
      )}

      {/* ── NETWORK (interfaces + switch port map) ────────────────────────── */}
      {tab === 'network' && (
        <>
          <Panel title="Network Interfaces" icon={Cable} subtitle={ifList.length ? `${ifList.length}` : undefined} pad={false}>
            {ifaces.data && ifList.length === 0 && <EmptyState icon={Cable} title="No interfaces collected" message="Bind a working credential and re-scan to collect interfaces." />}
            {ifList.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Index</th><th>Name</th><th>MAC</th><th>Speed</th></tr></thead>
                <tbody>
                  {[...ifList].sort((a, b) => a.if_index - b.if_index).map((i) => (
                    <tr key={i.id}>
                      <td className="mono">{i.if_index}</td>
                      <td className="cell-name">{i.if_name ?? i.if_descr ?? '—'}</td>
                      <td className="mono">{i.mac ?? '—'}</td>
                      <td className="mono">{speedLabel(i.speed_mbps)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
          <ConnectivityPanel deviceId={deviceId} />
        </>
      )}

      {/* ── SOFTWARE (deep OS inventory) ──────────────────────────────────── */}
      {tab === 'software' && (
        <>
          <Panel title="Operating system" icon={MonitorSmartphone} actions={<CollectOSButton deviceId={deviceId} small />}>
            <OSInventorySection deviceId={deviceId} section="summary" />
          </Panel>
          <Panel title="Installed software" icon={Boxes}>
            <OSInventorySection deviceId={deviceId} section="software" />
          </Panel>
          <Panel title="Services" icon={Settings}>
            <OSInventorySection deviceId={deviceId} section="services" />
          </Panel>
          <Panel title="Top processes" icon={Activity}>
            <OSInventorySection deviceId={deviceId} section="processes" />
          </Panel>
        </>
      )}

      {/* ── OPERATIONS ────────────────────────────────────────────────────── */}
      {tab === 'operations' && (
        <>
          <Panel title="Collection Credential" icon={KeyRound}>
            <CredentialBindSelect deviceId={deviceId} />
            <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
              The credential HIMS uses to collect from this server (SNMP for HOST-RESOURCES, WinRM/SSH for deep OS, Redfish for BMC). After binding, use <strong>Re-scan this device</strong> in the header — or run a collection — to apply it.
            </p>
          </Panel>
          <DeviceCredentialHealth deviceId={deviceId} category="server" />
          <DeviceOps deviceId={deviceId} />
        </>
      )}
    </div>
  )
}
