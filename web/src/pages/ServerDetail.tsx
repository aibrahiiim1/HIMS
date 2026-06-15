import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Server, Cpu, HardDrive, Cable, Activity, Settings, LayoutDashboard, Gauge, Thermometer, MemoryStick, KeyRound } from 'lucide-react'
import { api, type ServerStorage, type DeviceFact, type DeviceRole, type Interface, type BMCInfo, type BMCSensor } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { DeepOSInventory } from '../components/DeepOSInventory'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'
import { CredentialBindSelect } from '../components/CredentialBindSelect'
import { Panel, Kpi, DefList, EmptyState, StatusPill, Meter, TabBar } from '../components/ui'

type Tab = 'overview' | 'storage' | 'interfaces' | 'hardware' | 'operations'

const healthTone = (h?: string | null): 'up' | 'down' | 'warning' | 'unknown' =>
  h === 'OK' ? 'up' : h === 'Critical' ? 'down' : h === 'Warning' ? 'warning' : 'unknown'
// KPI cards use a different tone vocabulary (ok/warn/crit/info/default) than StatusPill.
const healthKpiTone = (h?: string | null): 'ok' | 'crit' | 'warn' | 'default' =>
  h === 'OK' ? 'ok' : h === 'Critical' ? 'crit' : h === 'Warning' ? 'warn' : 'default'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const speedLabel = (mbps?: number | null) => (mbps ? (mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`) : '—')

// ServerDetail — enterprise server console (HOST-RESOURCES-MIB + Redfish/iLO/iDRAC
// + deep OS inventory). Tabbed: Overview (resources + deep OS), Storage, Interfaces,
// Hardware/BMC, Operations. Re-scan / Repair check / credential binding come from
// the shared DeviceHeader (identical to every other device-detail page).
export function ServerDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const [tab, setTab] = useState<Tab>('overview')

  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const roles = useQuery({ queryKey: ['roles', id], queryFn: () => api.get<DeviceRole[]>(`/devices/${id}/roles`) })
  const storage = useQuery({ queryKey: ['storage', id], queryFn: () => api.get<ServerStorage[]>(`/devices/${id}/storage`) })
  const ifaces = useQuery({ queryKey: ['interfaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })
  const bmc = useQuery({ queryKey: ['bmc', id], queryFn: () => api.get<BMCInfo>(`/devices/${id}/bmc`) })
  const sensors = useQuery({ queryKey: ['bmc-sensors', id], queryFn: () => api.get<BMCSensor[]>(`/devices/${id}/bmc-sensors`) })

  const fm = useMemo(() => new Map((facts.data ?? []).map((f) => [f.key, f.value ?? ''])), [facts.data])
  const num = (k: string) => { const v = Number(fm.get(k)); return Number.isFinite(v) && fm.has(k) ? v : null }
  const cpu = num('cpu.load_pct')
  const memUsed = num('memory.used_bytes'), memTotal = num('memory.total_bytes')
  const memPct = memUsed != null && memTotal ? Math.round((memUsed / memTotal) * 100) : num('memory.used_pct')

  const vols = storage.data ?? []
  const volPct = (s: ServerStorage) => (s.total_bytes && s.used_bytes ? Math.round((s.used_bytes / s.total_bytes) * 100) : null)
  const worstVol = vols.reduce<number | null>((acc, s) => { const p = volPct(s); return p != null && (acc == null || p > acc) ? p : acc }, null)
  const totalCap = vols.reduce((a, s) => a + (s.total_bytes ?? 0), 0)

  const ifList = ifaces.data ?? []
  const roleList = roles.data ?? []
  const hasBMC = !!(bmc.data && bmc.data.device_id)
  const sensorList = sensors.data ?? []
  const badSensors = sensorList.filter((s) => s.status && s.status !== 'OK').length

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'storage', label: 'Storage', icon: HardDrive, count: vols.length || undefined },
    { key: 'interfaces', label: 'Interfaces', icon: Cable, count: ifList.length || undefined },
    { key: 'hardware', label: 'Hardware / BMC', icon: Thermometer, count: hasBMC ? (sensorList.length || undefined) : undefined },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Server} showCredential={false} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {/* ── OVERVIEW ──────────────────────────────────────────────────────── */}
      {tab === 'overview' && (
        <>
          <div className="kpi-grid kpi-6">
            <Kpi label="CPU load" value={cpu != null ? `${cpu}%` : '—'} icon={Cpu} tone={cpu != null && cpu >= 90 ? 'crit' : cpu != null && cpu >= 75 ? 'warn' : 'default'} sub="utilisation" />
            <Kpi label="Memory" value={memPct != null ? `${memPct}%` : '—'} icon={MemoryStick} tone={memPct != null && memPct >= 90 ? 'crit' : memPct != null && memPct >= 75 ? 'warn' : 'default'} sub={memTotal ? fmtBytes(memTotal) : 'used'} />
            <Kpi label="Storage used" value={worstVol != null ? `${worstVol}%` : '—'} icon={HardDrive} tone={worstVol != null && worstVol >= 90 ? 'crit' : worstVol != null && worstVol >= 75 ? 'warn' : 'default'} sub={totalCap ? `${fmtBytes(totalCap)} total` : 'busiest volume'} onClick={vols.length ? () => setTab('storage') : undefined} />
            <Kpi label="Volumes" value={vols.length} icon={HardDrive} tone="default" onClick={vols.length ? () => setTab('storage') : undefined} />
            <Kpi label="Interfaces" value={ifList.length} icon={Cable} tone="default" onClick={ifList.length ? () => setTab('interfaces') : undefined} />
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
                ? <EmptyState icon={Gauge} title="No resource gauges" message="CPU / memory / storage come from SNMP (HOST-RESOURCES-MIB) or deep OS inventory." />
                : (
                  <div className="stack" style={{ gap: 14 }}>
                    {cpu != null && <Meter label="CPU load" value={cpu} />}
                    {memPct != null && <Meter label="Memory used" value={memPct} />}
                    {worstVol != null && <Meter label="Busiest volume" value={worstVol} />}
                  </div>
                )}
            </Panel>
            <Panel title="System Facts" icon={Server}>
              <DefList items={[
                { label: 'CPU load', value: cpu != null ? `${cpu}%` : '—' },
                { label: 'Memory total', value: fmtBytes(memTotal) },
                { label: 'Memory used', value: fmtBytes(memUsed) },
                { label: 'Storage total', value: totalCap ? fmtBytes(totalCap) : '—' },
                { label: 'Volumes', value: vols.length },
                { label: 'Interfaces', value: ifList.length },
              ]} />
            </Panel>
          </div>

          <DeepOSInventory deviceId={deviceId} alwaysShow />
        </>
      )}

      {/* ── STORAGE ───────────────────────────────────────────────────────── */}
      {tab === 'storage' && (
        <Panel title="Storage Volumes" icon={HardDrive} subtitle={vols.length ? `${vols.length} · ${fmtBytes(totalCap)} total` : undefined} pad={false}>
          {storage.isLoading && <div className="loading">Loading…</div>}
          {storage.data && vols.length === 0 && <EmptyState icon={HardDrive} title="No storage collected" message="Volumes come from HOST-RESOURCES-MIB (SNMP) or deep OS inventory." />}
          {vols.length > 0 && (
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
          )}
        </Panel>
      )}

      {/* ── INTERFACES ────────────────────────────────────────────────────── */}
      {tab === 'interfaces' && (
        <Panel title="Network Interfaces" icon={Cable} subtitle={ifList.length ? `${ifList.length}` : undefined} pad={false}>
          {ifaces.data && ifList.length === 0 && <EmptyState icon={Cable} title="No interfaces collected" message="Bind a working credential and re-scan to collect interfaces." />}
          {ifList.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Index</th><th>Name</th><th>MAC</th><th>Speed</th></tr></thead>
              <tbody>
                {[...ifList].sort((a, b) => a.if_index - b.if_index).map((i) => (
                  <tr key={i.id}>
                    <td>{i.if_index}</td>
                    <td className="cell-name">{i.if_name ?? i.if_descr ?? '—'}</td>
                    <td className="mono">{i.mac ?? '—'}</td>
                    <td className="mono">{speedLabel(i.speed_mbps)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {/* ── HARDWARE / BMC ────────────────────────────────────────────────── */}
      {tab === 'hardware' && (
        <>
          {!hasBMC && <EmptyState icon={Thermometer} title="No out-of-band controller" message="iLO / iDRAC / Redfish hardware health appears here when a BMC is discovered and a credential is bound." />}
          {hasBMC && (
            <>
              <Panel title="Baseboard Management Controller" icon={Thermometer} actions={<StatusPill status={healthTone(bmc.data?.health)} label={bmc.data?.health ?? 'unknown'} />}>
                <DefList items={[
                  { label: 'Controller', value: `${bmc.data?.vendor ?? '—'} ${bmc.data?.controller_kind ?? ''}`.trim() },
                  { label: 'Model', value: bmc.data?.model ?? '—' },
                  { label: 'Serial', value: bmc.data?.serial ?? '—' },
                  { label: 'Firmware', value: bmc.data?.firmware_version ?? '—' },
                  { label: 'Power state', value: bmc.data?.power_state ?? '—' },
                ]} />
              </Panel>
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
