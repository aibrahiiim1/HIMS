import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { HardDrive, Database, Network, Thermometer, Activity, Server, Cpu, MemoryStick, LayoutDashboard, Clock, Gauge } from 'lucide-react'
import { api, type NASData, type Interface } from '../api'
import { Panel, Kpi, StatusPill, EmptyState, DefList, TabBar } from '../components/ui'
import { DeviceHeader } from '../components/DeviceHeader'

// NasDetail is the dedicated storage/NAS template for category=storage devices. It is
// organized into tabs (Overview, Storage, Network, Health) and renders ONLY real device
// data collected over the bound SNMP community: physical disk bays (vendor/model/serial/
// capacity/temp/health), logical data volumes (usage), network interfaces, and live
// system health. Before the first collect it shows an honest, actionable gate — never
// fabricated inventory.

function fmtBytes(n?: number | null): string {
  if (!n || n <= 0) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i <= 1 ? 0 : 1)} ${u[i]}`
}
function fmtUptime(sec?: number | null): string {
  if (!sec || sec <= 0) return '—'
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600)
  if (d > 0) return `${d}d ${h}h`
  const m = Math.floor((sec % 3600) / 60)
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}
const healthTone = (h?: string | null): 'ok' | 'crit' | 'warn' | 'default' => {
  const s = (h ?? '').toLowerCase()
  if (!s || s === 'unknown') return 'default'
  if (s.includes('ok') || s === 'good' || s === 'normal') return 'ok'
  if (s.includes('crit') || s.includes('fail') || s.includes('error')) return 'crit'
  if (s.includes('warn') || s.includes('degrad')) return 'warn'
  return 'default'
}
const pillTone = (h?: string | null): string => {
  const t = healthTone(h)
  return t === 'ok' ? 'up' : t === 'crit' ? 'down' : t === 'warn' ? 'warning' : 'unknown'
}
const tempTone = (c?: number | null): 'ok' | 'warn' | 'crit' | 'default' => {
  if (c == null) return 'default'
  if (c >= 60) return 'crit'
  if (c >= 50) return 'warn'
  return 'ok'
}
const operLabel = (o?: number | null) => (o === 1 ? 'up' : o === 2 ? 'down' : 'unknown')
// Physical NICs first (ethX / bondX); loopback and container bridges kept but sorted last.
const isRealNic = (i: Interface) => /^(eth|bond|lan|nic|em|en|p\d)/i.test(i.if_name || i.if_descr || '')

type Tab = 'overview' | 'storage' | 'network' | 'health'

export function NasDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  const [tab, setTab] = useState<Tab>('overview')
  const nasQ = useQuery({ queryKey: ['nas', id], queryFn: () => api.get<NASData>(`/devices/${id}/nas`) })
  const ifQ = useQuery({ queryKey: ['interfaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })

  const nas = nasQ.data
  const info = nas?.info
  const disks = nas?.disks ?? []
  const volumes = nas?.volumes ?? []
  const collected = !!nas?.collected
  const ifaces = [...(ifQ.data ?? [])].sort((a, b) => (isRealNic(b) ? 1 : 0) - (isRealNic(a) ? 1 : 0) || a.if_index - b.if_index)
  const nicCount = ifaces.filter(isRealNic).length
  const totalCap = disks.reduce((s, d) => s + (d.capacity_bytes || 0), 0)
  const volTotal = volumes.reduce((s, v) => s + (v.total_bytes || 0), 0)
  const volUsed = volumes.reduce((s, v) => s + (v.used_bytes || 0), 0)
  const badDisks = disks.filter((d) => healthTone(d.health) === 'crit' || healthTone(d.health) === 'warn').length

  if (nasQ.isLoading) return <div className="loading">Loading…</div>

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'storage', label: 'Storage', icon: HardDrive, count: collected ? disks.length + volumes.length : undefined },
    { key: 'network', label: 'Network', icon: Network, count: nicCount || undefined },
    { key: 'health', label: 'Health', icon: Thermometer },
  ]

  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Database} showCredential={false} />

      {/* Collect gate — shown above the tabs until an SNMP snapshot exists. */}
      {!collected && (
        <Panel title="NAS deep inventory" icon={Database} className="bmc-gate">
          <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
            <StatusPill status="unknown" label="Not collected" />
            <div style={{ flex: 1, minWidth: 260 }}>
              <p style={{ margin: 0, fontSize: 13 }}>No NAS inventory has been collected yet. Collect pulls physical disks, logical volumes, network interfaces, and live system health over the device's bound SNMP community — no credential spray.</p>
              <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>If the device has no bound SNMP v2c community, collection returns an honest credential-required error — bind a community first.</p>
            </div>
            <NasCollect deviceId={deviceId} label="Collect now…" />
          </div>
        </Panel>
      )}

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {/* ============================ OVERVIEW ============================ */}
      {tab === 'overview' && (
        <>
          <div className="kpi-grid kpi-8">
            <Kpi label="Health" value={info?.health || (collected ? 'OK' : '—')} icon={Activity} tone={healthTone(info?.health)} sub={badDisks > 0 ? `${badDisks} disk(s) not OK` : (disks.length ? 'all disks OK' : 'no disks')} />
            <Kpi label="Disks" value={collected ? disks.length : '—'} icon={HardDrive} tone={disks.length ? 'ok' : 'default'} sub={totalCap ? fmtBytes(totalCap) + ' raw' : '—'} />
            <Kpi label="Volumes" value={collected ? volumes.length : '—'} icon={Database} tone="default" sub={volTotal ? fmtBytes(volTotal) : '—'} />
            <Kpi label="Volume used" value={volTotal ? `${Math.round((volUsed / volTotal) * 100)}%` : '—'} icon={Gauge} tone={volTotal && volUsed / volTotal >= 0.9 ? 'crit' : volTotal && volUsed / volTotal >= 0.75 ? 'warn' : 'default'} sub={volTotal ? `${fmtBytes(volUsed)} / ${fmtBytes(volTotal)}` : '—'} />
            <Kpi label="CPU" value={info?.cpu_pct != null ? `${info.cpu_pct.toFixed(1)}%` : '—'} icon={Cpu} tone={info?.cpu_pct != null && info.cpu_pct >= 90 ? 'crit' : 'default'} />
            <Kpi label="Memory" value={info?.mem_total_bytes ? `${Math.round(((info.mem_used_bytes || 0) / info.mem_total_bytes) * 100)}%` : '—'} icon={MemoryStick} tone="default" sub={info?.mem_total_bytes ? `${fmtBytes(info.mem_used_bytes)} / ${fmtBytes(info.mem_total_bytes)}` : '—'} />
            <Kpi label="System temp" value={info?.sys_temp_c != null ? `${info.sys_temp_c}°C` : '—'} icon={Thermometer} tone={tempTone(info?.sys_temp_c)} sub={info?.cpu_temp_c != null ? `CPU ${info.cpu_temp_c}°C` : undefined} />
            <Kpi label="Uptime" value={fmtUptime(info?.uptime_seconds)} icon={Clock} tone="default" />
          </div>

          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="Appliance" icon={Server} actions={<StatusPill status={pillTone(info?.health)} label={info?.health || (collected ? 'OK' : 'not collected')} />}>
              <DefList items={[
                { label: 'Vendor', value: info?.vendor || '—' },
                { label: 'Model', value: info?.model || '—' },
                { label: 'Firmware', value: info?.firmware || '—' },
                { label: 'Hostname', value: info?.hostname || '—' },
                { label: 'Serial', value: <span className="mono">{info?.serial || 'not exposed over SNMP'}</span> },
                { label: 'Disks', value: collected ? `${disks.length} disk(s) · ${fmtBytes(totalCap)} raw` : '—' },
                { label: 'Volumes', value: collected ? `${volumes.length} volume(s) · ${fmtBytes(volTotal)}` : '—' },
                { label: 'Collected', value: info?.last_seen_at ? new Date(info.last_seen_at).toLocaleString() : '—' },
              ]} />
            </Panel>
            <Panel title="System" icon={Cpu} actions={collected ? <NasCollect deviceId={deviceId} label="Re-collect" /> : undefined}>
              <DefList items={[
                { label: 'CPU usage', value: info?.cpu_pct != null ? `${info.cpu_pct.toFixed(1)}%` : '—' },
                { label: 'Memory', value: info?.mem_total_bytes ? `${fmtBytes(info.mem_used_bytes)} / ${fmtBytes(info.mem_total_bytes)}` : '—' },
                { label: 'CPU temperature', value: info?.cpu_temp_c != null ? `${info.cpu_temp_c}°C` : '—' },
                { label: 'System temperature', value: info?.sys_temp_c != null ? `${info.sys_temp_c}°C` : '—' },
                { label: 'Uptime', value: fmtUptime(info?.uptime_seconds) },
                { label: 'Network interfaces', value: `${nicCount} physical NIC(s)` },
                { label: 'Source', value: info?.collection_source ? `SNMP (${info.collection_source})` : 'SNMP' },
              ]} />
            </Panel>
          </div>
        </>
      )}

      {/* ============================ STORAGE ============================ */}
      {tab === 'storage' && (
        collected ? (
          <>
            <Panel title="Physical disks" icon={HardDrive} subtitle={`${disks.length} bay(s) · ${fmtBytes(totalCap)} raw`} pad={false}>
              {disks.length === 0 ? <div style={{ padding: 14 }} className="muted">No physical disks were reported by this appliance.</div> : (
                <table className="data-table">
                  <thead><tr><th>Bay</th><th>Vendor</th><th>Model</th><th>Serial</th><th>Interface</th><th>Capacity</th><th>Temp</th><th>Health</th></tr></thead>
                  <tbody>{disks.map((d) => (
                    <tr key={d.slot}>
                      <td className="cell-name">#{d.slot}</td>
                      <td>{d.vendor || '—'}</td>
                      <td>{d.model || '—'}</td>
                      <td className="mono" style={{ fontSize: 12 }}>{d.serial || '—'}</td>
                      <td>{d.interface_type ? <span className="badge badge-unknown">{d.interface_type}</span> : '—'}</td>
                      <td className="mono">{fmtBytes(d.capacity_bytes)}</td>
                      <td className="mono" style={{ color: tempTone(d.temp_c) === 'crit' ? 'var(--danger)' : tempTone(d.temp_c) === 'warn' ? 'var(--warning)' : undefined }}>{d.temp_c != null ? `${d.temp_c}°C` : '—'}</td>
                      <td><StatusPill status={pillTone(d.health)} label={d.health || '—'} /></td>
                    </tr>
                  ))}</tbody>
                </table>
              )}
            </Panel>
            <Panel title="Logical volumes" icon={Database} subtitle={`${volumes.length} volume(s)`} pad={false}>
              {volumes.length === 0 ? <div style={{ padding: 14 }} className="muted">No data volumes were reported. Only operator data volumes/pools are shown (OS/system mounts are filtered out).</div> : (
                <table className="data-table">
                  <thead><tr><th>Volume</th><th>Type</th><th>Total</th><th>Used</th><th>Free</th><th>Usage</th></tr></thead>
                  <tbody>{volumes.map((v) => {
                    const total = v.total_bytes || 0, used = v.used_bytes || 0
                    const pct = total ? Math.round((used / total) * 100) : 0
                    return (
                      <tr key={v.idx}>
                        <td className="cell-name mono" style={{ fontSize: 12 }}>{v.name}</td>
                        <td>{v.fs_type ? <span className="badge badge-unknown">{v.fs_type}</span> : '—'}</td>
                        <td className="mono">{fmtBytes(total)}</td>
                        <td className="mono">{fmtBytes(used)}</td>
                        <td className="mono">{fmtBytes(total - used)}</td>
                        <td>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                            <div style={{ flex: 1, minWidth: 60, height: 6, background: 'var(--border)', borderRadius: 4, overflow: 'hidden' }}>
                              <div style={{ width: `${pct}%`, height: '100%', background: pct >= 90 ? 'var(--danger)' : pct >= 75 ? 'var(--warning)' : 'var(--accent)' }} />
                            </div>
                            <span className="mono" style={{ fontSize: 12 }}>{pct}%</span>
                          </div>
                        </td>
                      </tr>
                    )
                  })}</tbody>
                </table>
              )}
            </Panel>
          </>
        ) : <GatedTab deviceId={deviceId} what="physical disks & logical volumes" />
      )}

      {/* ============================ NETWORK ============================ */}
      {tab === 'network' && (
        <Panel title="Network interfaces" icon={Network} subtitle={`${ifaces.length} interface(s) · ${nicCount} physical`} pad={false}>
          {ifQ.isLoading ? <div style={{ padding: 14 }} className="muted">Loading interfaces…</div> :
            ifaces.length === 0 ? <div style={{ padding: 14 }} className="muted">No interfaces collected yet. Run a collect to pull the IF-MIB interface table.</div> : (
              <table className="data-table">
                <thead><tr><th>Interface</th><th>MAC</th><th>Speed</th><th>Admin</th><th>Status</th></tr></thead>
                <tbody>{ifaces.map((i) => (
                  <tr key={i.id} style={{ opacity: isRealNic(i) ? 1 : 0.55 }}>
                    <td className="cell-name">{i.if_name || i.if_descr || `if${i.if_index}`}</td>
                    <td className="mono" style={{ fontSize: 12 }}>{i.mac || '—'}</td>
                    <td className="mono">{i.speed_mbps ? (i.speed_mbps >= 1000 ? `${(i.speed_mbps / 1000).toFixed(0)} Gbps` : `${i.speed_mbps} Mbps`) : '—'}</td>
                    <td><StatusPill status={i.admin_status === 1 ? 'up' : 'unknown'} label={i.admin_status === 1 ? 'up' : i.admin_status === 2 ? 'down' : '—'} /></td>
                    <td><StatusPill status={operLabel(i.oper_status)} label={operLabel(i.oper_status)} /></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
        </Panel>
      )}

      {/* ============================ HEALTH ============================ */}
      {tab === 'health' && (
        collected ? (
          <>
            <div className="kpi-grid kpi-4">
              <Kpi label="Overall" value={info?.health || 'OK'} icon={Activity} tone={healthTone(info?.health)} />
              <Kpi label="CPU temp" value={info?.cpu_temp_c != null ? `${info.cpu_temp_c}°C` : '—'} icon={Thermometer} tone={tempTone(info?.cpu_temp_c)} />
              <Kpi label="System temp" value={info?.sys_temp_c != null ? `${info.sys_temp_c}°C` : '—'} icon={Thermometer} tone={tempTone(info?.sys_temp_c)} />
              <Kpi label="CPU load" value={info?.cpu_pct != null ? `${info.cpu_pct.toFixed(1)}%` : '—'} icon={Cpu} tone={info?.cpu_pct != null && info.cpu_pct >= 90 ? 'crit' : 'default'} />
            </div>
            <Panel title="Disk health & temperature" icon={HardDrive} pad={false}>
              <table className="data-table">
                <thead><tr><th>Bay</th><th>Model</th><th>Serial</th><th>Temp</th><th>Health</th></tr></thead>
                <tbody>{disks.map((d) => (
                  <tr key={d.slot}>
                    <td className="cell-name">#{d.slot}</td>
                    <td>{d.model || '—'}</td>
                    <td className="mono" style={{ fontSize: 12 }}>{d.serial || '—'}</td>
                    <td className="mono" style={{ color: tempTone(d.temp_c) === 'crit' ? 'var(--danger)' : tempTone(d.temp_c) === 'warn' ? 'var(--warning)' : undefined }}>{d.temp_c != null ? `${d.temp_c}°C` : '—'}</td>
                    <td><StatusPill status={pillTone(d.health)} label={d.health || '—'} /></td>
                  </tr>
                ))}</tbody>
              </table>
            </Panel>
          </>
        ) : <GatedTab deviceId={deviceId} what="system & disk health" />
      )}
    </div>
  )
}

// GatedTab: honest empty state shown on data tabs before the first collect.
function GatedTab({ deviceId, what }: { deviceId: string; what: string }) {
  return (
    <EmptyState
      icon={Database}
      title="Not collected yet"
      message={`No ${what} has been collected. Collect over the bound SNMP community to populate this tab — no fabricated data is shown.`}
      action={<NasCollect deviceId={deviceId} label="Collect now…" />}
    />
  )
}

// NasCollect: one-click SNMP deep collect. Refreshes the NAS + interface queries on success.
function NasCollect({ deviceId, label }: { deviceId: string; label: string }) {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<string | null>(null)
  async function run() {
    setBusy(true); setMsg(null)
    try {
      const res = await api.post<{ ok: boolean; disk_count: number; volume_count: number }>(`/devices/${deviceId}/collect-nas`, {})
      setMsg(`✓ ${res.disk_count} disk(s), ${res.volume_count} volume(s)`)
      qc.invalidateQueries({ queryKey: ['nas', deviceId] })
      qc.invalidateQueries({ queryKey: ['interfaces', deviceId] })
    } catch (e) { setMsg('✗ ' + (e as Error).message) } finally { setBusy(false) }
  }
  const btn = { fontSize: 12, padding: '4px 10px', border: '1px solid var(--border)', borderRadius: 6, background: 'var(--surface)', color: 'inherit', cursor: 'pointer' } as const
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, alignItems: 'flex-start' }}>
      <button style={btn} disabled={busy} onClick={run}>{busy ? 'Collecting…' : label}</button>
      {msg && <span className="muted" style={{ fontSize: 11, maxWidth: 320, whiteSpace: 'normal' }}>{msg}</span>}
    </div>
  )
}
