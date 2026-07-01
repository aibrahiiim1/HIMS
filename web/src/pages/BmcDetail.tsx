import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Cpu, MemoryStick, HardDrive, Thermometer, Activity, Server, KeyRound, ShieldCheck, Gauge, Fan, Zap, Database } from 'lucide-react'
import { api, type BMCInfo, type BMCSensor, type BMCComponent, type Credential } from '../api'
import { Panel, Kpi, StatusPill, EmptyState, DefList } from '../components/ui'
import { DeviceHeader } from '../components/DeviceHeader'

// The /inventory/bmc row carries the DERIVED states (reachability, snmp vs redfish health,
// redfish_status, bmc_status, link) computed server-side — we reuse it so the dashboard's
// status logic is identical to the list, not re-derived on the client.
interface BmcRow {
  id: string
  ip: string
  vendor: string
  model: string
  serial: string
  firmware: string
  controller_kind: string
  reachability: string
  snmp_health: string
  health_summary: string
  redfish_status: string // collected | credential_required | credential_failed | not_collected
  bmc_status: string
  management: string
  managed_by?: string[]
  link_state: string
  linked_server: string
  linked_server_id?: string
  link_confidence: number
  link_evidence: string
  evidence: string
  power_state: string
}

const healthTone = (h?: string | null): 'ok' | 'crit' | 'warn' | 'default' => {
  const s = (h ?? '').toLowerCase()
  if (!s) return 'default'
  if (s.includes('ok') || s === 'good') return 'ok'
  if (s.includes('fail') || s.includes('crit')) return 'crit'
  if (s.includes('warn') || s.includes('degrad')) return 'warn'
  return 'default'
}
const pillTone = (h?: string | null): string => {
  const t = healthTone(h)
  return t === 'ok' ? 'up' : t === 'crit' ? 'down' : t === 'warn' ? 'warning' : 'unknown'
}
function fmtBytes(n?: number | null): string {
  if (!n || n <= 0) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i <= 1 ? 0 : 1)} ${u[i]}`
}

// redfishStateLabel maps the honest redfish_status to a human label + tone.
function redfishState(r?: BmcRow): { label: string; tone: string; help: string } {
  switch (r?.redfish_status) {
    case 'collected': return { label: 'Collected', tone: 'up', help: 'Full hardware inventory collected over authenticated Redfish.' }
    case 'credential_failed': return { label: 'Credential rejected', tone: 'down', help: 'A Redfish credential was rejected by this controller — it needs its own valid iLO Administrator / Redfish login.' }
    case 'credential_required': return { label: 'Credential required', tone: 'warning', help: 'Redfish is reachable but no valid credential is bound. Use Collect Redfish to gather full inventory.' }
    default: return { label: 'Not collected', tone: 'unknown', help: 'No Redfish inventory. Reachability + SNMP health are collected separately.' }
  }
}

// RaidBadge / MediaBadge / ProtoBadge render clear storage badges.
const RaidBadge = ({ v }: { v?: string }) => v ? <span className="badge badge-info">{v}</span> : <span className="muted">—</span>
const MediaBadge = ({ v }: { v?: string }) => {
  if (!v) return <span className="muted">—</span>
  const s = v.toLowerCase()
  const cls = s === 'ssd' ? 'badge-up' : s === 'nvme' ? 'badge-access' : 'badge-unknown'
  return <span className={`badge ${cls}`}>{v}</span>
}
const ProtoBadge = ({ v }: { v?: string }) => v ? <span className="badge badge-unknown">{v}</span> : <span className="muted">—</span>

// BmcDetail is the dedicated iLO/iDRAC hardware dashboard for category=bmc devices — NOT
// the generic server template. It presents controller identity, health (SNMP vs Redfish
// kept separate), and the full authenticated Redfish inventory (CPU, memory, storage/RAID,
// drives, grouped sensors) with honest empty/credential states — real API data only.
export function BmcDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const inv = useQuery({ queryKey: ['inventory-bmc'], queryFn: () => api.get<BmcRow[]>('/inventory/bmc') })
  const bmc = useQuery({ queryKey: ['bmc', id], queryFn: () => api.get<BMCInfo>(`/devices/${id}/bmc`) })
  const sensors = useQuery({ queryKey: ['bmc-sensors', id], queryFn: () => api.get<BMCSensor[]>(`/devices/${id}/bmc-sensors`) })
  const components = useQuery({ queryKey: ['bmc-components', id], queryFn: () => api.get<BMCComponent[]>(`/devices/${id}/bmc-components`) })

  const row = (inv.data ?? []).find((r) => r.id === deviceId)
  const b = bmc.data
  const comps = components.data ?? []
  const compsOf = (k: string) => comps.filter((c) => c.kind === k)
  const cpus = compsOf('cpu'), dimms = compsOf('memory'), controllers = compsOf('controller'), volumes = compsOf('volume'), drives = compsOf('drive')
  const sensorList = sensors.data ?? []
  const sensorsOf = (k: string) => sensorList.filter((s) => s.kind === k)
  const fans = sensorsOf('fan'), temps = sensorsOf('temperature'), psus = sensorsOf('psu')
  const otherSensors = sensorList.filter((s) => !['fan', 'temperature', 'psu'].includes(s.kind))
  const badSensors = sensorList.filter((s) => s.status && s.status.toLowerCase() !== 'ok').length
  const badStorage = [...volumes, ...drives, ...controllers].filter((c) => c.status && c.status.toLowerCase() !== 'ok').length
  const rf = redfishState(row)
  const collected = row?.redfish_status === 'collected'
  const isBmc = !!b && !!b.device_id

  if (inv.isLoading && bmc.isLoading) return <div className="loading">Loading…</div>

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Cpu} showCredential={false} />

      {/* Credential gate — only when inventory is NOT collected. Honest, actionable. */}
      {row && !collected && (
        <Panel title="Full hardware inventory" icon={ShieldCheck} className="bmc-gate">
          <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
            <StatusPill status={rf.tone} label={rf.label} />
            <div style={{ flex: 1, minWidth: 260 }}>
              <p style={{ margin: 0, fontSize: 13 }}>{rf.help}</p>
              {row.redfish_status === 'credential_failed' && (
                <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>Blocker: a valid device-specific iLO Administrator / Redfish credential is required for <span className="mono">{row.ip}</span>. This is an actionable credential gate — not deferred.</p>
              )}
            </div>
            <RedfishCollect deviceId={deviceId} label={collected ? 'Re-collect' : 'Collect Redfish…'} />
          </div>
        </Panel>
      )}

      {/* B. Health summary cards */}
      <div className="kpi-grid kpi-8">
        <Kpi label="Reachability" value={cap(row?.reachability)} icon={Activity} tone={row?.reachability === 'online' ? 'ok' : row?.reachability === 'offline' ? 'crit' : 'default'} />
        <Kpi label="SNMP health" value={row?.snmp_health || '—'} icon={Gauge} tone={row?.snmp_health ? healthTone(row.snmp_health) : 'default'} sub="controller (SNMP)" />
        <Kpi label="Redfish health" value={collected ? (b?.health || row?.health_summary || '—') : '—'} icon={ShieldCheck} tone={collected ? healthTone(b?.health || row?.health_summary) : 'default'} sub={collected ? 'hardware (Redfish)' : 'not collected'} />
        <Kpi label="Redfish inventory" value={rf.label} icon={Database} tone={rf.tone === 'up' ? 'ok' : rf.tone === 'down' ? 'crit' : rf.tone === 'warning' ? 'warn' : 'default'} />
        <Kpi label="Sensors" value={sensorList.length || '—'} icon={Thermometer} tone={badSensors > 0 ? 'warn' : sensorList.length ? 'ok' : 'default'} sub={badSensors > 0 ? `${badSensors} not OK` : (sensorList.length ? 'all OK' : 'none')} />
        <Kpi label="Storage / RAID" value={collected ? `${volumes.length}v · ${drives.length}d` : '—'} icon={HardDrive} tone={badStorage > 0 ? 'crit' : (drives.length ? 'ok' : 'default')} sub={collected ? `${controllers.length} controller(s)` : '—'} />
        <Kpi label="CPU" value={b?.cpu_count ? `${b.cpu_count}× · ${b.cpu_cores ?? '?'}c` : '—'} icon={Cpu} tone="default" sub={b?.cpu_model ? shortCpu(b.cpu_model) : '—'} />
        <Kpi label="Memory" value={b?.memory_gib ? `${b.memory_gib} GiB` : '—'} icon={MemoryStick} tone="default" sub={dimms.length ? `${dimms.length} DIMM(s)` : '—'} />
      </div>

      {/* A2. BMC identity strip + hardware overview cards */}
      <div className="grid-2" style={{ alignItems: 'start' }}>
        <Panel title="Controller" icon={Server} actions={<StatusPill status={pillTone(b?.health || row?.health_summary)} label={collected ? (b?.health || 'unknown') : rf.label} />}>
          <DefList items={[
            { label: 'Vendor', value: row?.vendor || b?.vendor || '—' },
            { label: 'Controller', value: `${row?.vendor ?? b?.vendor ?? ''} ${row?.controller_kind ?? b?.controller_kind ?? ''}`.trim() || '—' },
            { label: 'Host model', value: b?.model || row?.model || '—' },
            { label: 'Serial / ServiceTag', value: <span className="mono">{row?.serial || b?.serial || '—'}</span> },
            { label: 'Firmware', value: b?.firmware_version || row?.firmware || '—' },
            { label: 'Redfish inventory', value: <StatusPill status={rf.tone} label={rf.label} /> },
            { label: 'Management', value: `${cap(row?.management)}${row?.managed_by?.length ? ` · ${row.managed_by.join(', ')}` : ''}` },
            { label: 'Linked server', value: row?.linked_server ? <span title={row.link_evidence}>{row.linked_server} <span className="muted">({row.link_confidence}%)</span></span> : <span className="muted" title={row?.link_evidence}>{cap(row?.link_state) || 'unlinked'}</span> },
          ]} />
        </Panel>
        <Panel title="Compute & firmware" icon={Cpu}>
          <DefList items={[
            { label: 'CPU', value: b?.cpu_model ? `${b.cpu_model}${b.cpu_count ? ` × ${b.cpu_count}` : ''}` : notExposed(collected) },
            { label: 'Cores', value: b?.cpu_cores ? `${b.cpu_cores} cores` : notExposed(collected) },
            { label: 'Memory', value: b?.memory_gib ? `${b.memory_gib} GiB · ${dimms.length} DIMM(s)` : notExposed(collected) },
            { label: 'BIOS', value: b?.bios_version || notExposed(collected) },
            { label: 'Power state', value: b?.power_state || row?.power_state || '—' },
            { label: 'Storage', value: collected ? `${controllers.length} controller(s) · ${volumes.length} volume(s) · ${drives.length} drive(s)` : notExposed(collected) },
          ]} />
        </Panel>
      </div>

      {/* C. Processors */}
      {collected && (
        <Panel title="Processors" icon={Cpu} subtitle={`${cpus.length}`} pad={false}>
          {cpus.length === 0 ? <NoData reason="not_exposed_by_device" /> : (
            <table className="data-table">
              <thead><tr><th>Socket</th><th>Model</th><th>Cores / Threads</th><th>Max speed</th><th>Arch</th><th>Status</th></tr></thead>
              <tbody>{cpus.map((c) => (
                <tr key={c.id}><td className="cell-name">{c.name}</td><td>{c.model || '—'}</td>
                  <td className="mono">{c.detail.cores || '—'}{c.detail.threads ? ` / ${c.detail.threads}` : ''}</td>
                  <td className="mono">{c.detail.max_speed_mhz ? `${c.detail.max_speed_mhz} MHz` : '—'}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{c.detail.arch || '—'}</td>
                  <td><StatusPill status={pillTone(c.status)} label={c.status ?? '—'} /></td></tr>
              ))}</tbody>
            </table>
          )}
        </Panel>
      )}

      {/* C. Memory DIMMs */}
      {collected && (
        <Panel title="Memory" icon={MemoryStick} subtitle={`${dimms.length} DIMM(s)${b?.memory_gib ? ` · ${b.memory_gib} GiB` : ''}`} pad={false}>
          {dimms.length === 0 ? <NoData reason="not_exposed_by_device" /> : (
            <table className="data-table">
              <thead><tr><th>Slot</th><th>Size</th><th>Type</th><th>Speed</th><th>Part / Manufacturer</th><th>Status</th></tr></thead>
              <tbody>{dimms.map((c) => (
                <tr key={c.id}><td className="cell-name">{c.name}</td><td className="mono">{fmtBytes(c.capacity_bytes)}</td>
                  <td>{c.detail.type ? <span className="badge badge-unknown">{c.detail.type}</span> : '—'}</td>
                  <td className="mono">{c.detail.speed_mhz ? `${c.detail.speed_mhz} MHz` : '—'}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{[c.model, c.detail.manufacturer].filter(Boolean).join(' · ') || '—'}</td>
                  <td><StatusPill status={pillTone(c.status)} label={c.status ?? '—'} /></td></tr>
              ))}</tbody>
            </table>
          )}
        </Panel>
      )}

      {/* D. Storage / RAID — controllers, volumes, drives kept SEPARATE */}
      {collected && (controllers.length > 0 || volumes.length > 0 || drives.length > 0) && (
        <>
          <Panel title="RAID controllers" icon={ShieldCheck} subtitle={`${controllers.length}`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Name</th><th>Model</th><th>Firmware</th><th>Supported RAID</th><th>Status</th></tr></thead>
              <tbody>{controllers.map((c) => (
                <tr key={c.id}><td className="cell-name">{c.name}</td><td>{c.model || '—'}</td>
                  <td className="mono">{c.detail.firmware || '—'}</td>
                  <td>{c.detail.raid_types ? c.detail.raid_types.split(',').map((r) => <RaidBadge key={r} v={r} />) : <span className="muted">—</span>}</td>
                  <td><StatusPill status={pillTone(c.status)} label={c.status ?? '—'} /></td></tr>
              ))}</tbody>
            </table>
          </Panel>
          <Panel title="Logical volumes (RAID)" icon={Database} subtitle={`${volumes.length}`} pad={false}>
            {volumes.length === 0 ? <NoData reason="not_exposed_by_device" /> : (
              <table className="data-table">
                <thead><tr><th>Name</th><th>RAID level</th><th>Capacity</th><th>Status</th></tr></thead>
                <tbody>{volumes.map((c) => (
                  <tr key={c.id}><td className="cell-name">{c.name}</td><td><RaidBadge v={c.detail.raid} /></td>
                    <td className="mono">{fmtBytes(c.capacity_bytes)}</td>
                    <td><StatusPill status={pillTone(c.status)} label={c.status ?? '—'} /></td></tr>
                ))}</tbody>
              </table>
            )}
          </Panel>
          <Panel title="Physical drives" icon={HardDrive} subtitle={`${drives.length}`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Bay / Name</th><th>Model</th><th>Serial</th><th>Capacity</th><th>Media</th><th>Protocol</th><th>Status</th></tr></thead>
              <tbody>{drives.map((c) => (
                <tr key={c.id}><td className="cell-name">{c.name}</td><td>{c.model || '—'}</td>
                  <td className="mono" style={{ fontSize: 12 }}>{c.serial || '—'}</td>
                  <td className="mono">{fmtBytes(c.capacity_bytes)}</td>
                  <td><MediaBadge v={c.detail.media} /></td><td><ProtoBadge v={c.detail.protocol} /></td>
                  <td><StatusPill status={pillTone(c.status)} label={c.status ?? '—'} /></td></tr>
              ))}</tbody>
            </table>
          </Panel>
        </>
      )}

      {/* D. Sensors — grouped */}
      {collected && (
        <div className="grid-2" style={{ alignItems: 'start' }}>
          <SensorPanel title="Fans" icon={Fan} rows={fans} unitDefault="RPM" />
          <SensorPanel title="Temperatures" icon={Thermometer} rows={temps} unitDefault="C" />
          <SensorPanel title="Power supplies" icon={Zap} rows={psus} unitDefault="W" />
          {otherSensors.length > 0 && <SensorPanel title="Other sensors" icon={Activity} rows={otherSensors} unitDefault="" />}
        </div>
      )}

      {/* Evidence / collection state */}
      <Panel title="Evidence & collection state" icon={KeyRound}>
        <DefList items={[
          { label: 'Redfish inventory', value: <StatusPill status={rf.tone} label={rf.label} /> },
          { label: 'BMC collection', value: cap(row?.bmc_status) || '—' },
          { label: 'Classification evidence', value: <span className="muted" style={{ fontSize: 12 }}>{row?.evidence || '—'}</span> },
          { label: 'Link evidence', value: <span className="muted" style={{ fontSize: 12 }}>{row?.link_evidence || '—'}</span> },
        ]} />
        {!collected && <div style={{ marginTop: 10 }}><RedfishCollect deviceId={deviceId} label="Collect Redfish…" /></div>}
      </Panel>

      {!isBmc && !row && (
        <EmptyState icon={Cpu} title="Not a BMC device" message="This page is for out-of-band controllers (iLO / iDRAC / Redfish)." />
      )}
    </div>
  )
}

// SensorPanel renders one grouped sensor category with an honest empty state.
function SensorPanel({ title, icon, rows, unitDefault }: { title: string; icon: typeof Fan; rows: BMCSensor[]; unitDefault: string }) {
  const bad = rows.filter((s) => s.status && s.status.toLowerCase() !== 'ok').length
  return (
    <Panel title={title} icon={icon} subtitle={rows.length ? `${rows.length}${bad ? ` · ${bad} not OK` : ''}` : undefined} pad={false}>
      {rows.length === 0 ? <NoData reason="not_exposed_by_device" /> : (
        <table className="data-table">
          <thead><tr><th>Name</th><th>Reading</th><th>Status</th></tr></thead>
          <tbody>{[...rows].sort((a, b) => (a.status === 'OK' ? 1 : 0) - (b.status === 'OK' ? 1 : 0)).map((s) => (
            <tr key={s.id}><td className="cell-name">{s.name}</td>
              <td className="mono">{s.has_reading ? `${s.reading} ${s.unit || unitDefault}` : '—'}</td>
              <td><StatusPill status={pillTone(s.status)} label={s.status ?? '—'} /></td></tr>
          ))}</tbody>
        </table>
      )}
    </Panel>
  )
}

// NoData is the honest "why is this empty" row shown inside a section.
function NoData({ reason }: { reason: string }) {
  const msg: Record<string, string> = {
    credential_required: 'Bind a Redfish credential and Collect to gather this.',
    credential_failed: 'The Redfish credential was rejected — provide a valid one.',
    not_collected: 'Not collected yet.',
    not_exposed_by_device: 'Not exposed by this controller.',
    unsupported_by_device: 'Not supported by this controller.',
    not_available: 'Not available.',
  }
  return <div style={{ padding: 14 }} className="muted" >{msg[reason] ?? 'Not available.'}</div>
}

// RedfishCollect: pick ONE credential → Test / Collect (no spray). Refreshes on success.
function RedfishCollect({ deviceId, label }: { deviceId: string; label: string }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [cred, setCred] = useState('')
  const [msg, setMsg] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials'), enabled: open })
  const loginCreds = (creds.data ?? []).filter((c) => c.kind === 'http_basic' || c.kind === 'vendor_api')
  async function run(kind: 'test-redfish' | 'collect-bmc-redfish') {
    if (!cred) { setMsg('Pick a credential first'); return }
    setBusy(true); setMsg(null)
    try {
      const res = await api.post<{ ok: boolean; state: string; detail: string }>(`/devices/${deviceId}/${kind}`, { credential_id: cred })
      setMsg((res.ok ? '✓ ' : '✗ ') + (res.detail || res.state))
      if (res.ok && kind === 'collect-bmc-redfish') {
        qc.invalidateQueries({ queryKey: ['inventory-bmc'] }); qc.invalidateQueries({ queryKey: ['bmc', deviceId] })
        qc.invalidateQueries({ queryKey: ['bmc-sensors', deviceId] }); qc.invalidateQueries({ queryKey: ['bmc-components', deviceId] })
      }
    } catch (e) { setMsg('✗ ' + (e as Error).message) } finally { setBusy(false) }
  }
  const btn = { fontSize: 12, padding: '4px 10px', border: '1px solid var(--border)', borderRadius: 6, background: 'var(--surface)', color: 'inherit', cursor: 'pointer' } as const
  if (!open) return <button style={btn} onClick={() => setOpen(true)}>{label}</button>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 220 }}>
      <select value={cred} onChange={(e) => setCred(e.target.value)} style={{ fontSize: 12, maxWidth: 260 }}>
        <option value="">— pick http_basic / Redfish credential —</option>
        {loginCreds.map((c) => <option key={c.id} value={c.id}>{c.name} · {c.kind}</option>)}
      </select>
      <div style={{ display: 'flex', gap: 6 }}>
        <button style={btn} disabled={busy} onClick={() => run('test-redfish')}>Test</button>
        <button style={btn} disabled={busy} onClick={() => run('collect-bmc-redfish')}>Collect</button>
        <button style={btn} onClick={() => { setOpen(false); setMsg(null) }}>✕</button>
      </div>
      {msg && <span className="muted" style={{ fontSize: 11, maxWidth: 300, whiteSpace: 'normal' }}>{msg}</span>}
    </div>
  )
}

const cap = (s?: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : '')
const notExposed = (collected: boolean) => <span className="muted">{collected ? 'Not exposed' : 'Not collected'}</span>
const shortCpu = (m: string) => m.replace(/\(R\)|\(TM\)|CPU|@.*/gi, '').replace(/\s+/g, ' ').trim()
