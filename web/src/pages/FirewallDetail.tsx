import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import {
  Flame, Shield, Activity, Cpu, KeyRound, Cable, LayoutDashboard, Settings,
  Server as ServerIcon, AlertTriangle, Gauge, ArrowDownToLine, ArrowUpFromLine,
} from 'lucide-react'
import { api, type FirewallStatus, type VpnTunnel, type HAMember, type License, type DeviceFact, type Interface } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'
import { CredentialBindSelect } from '../components/CredentialBindSelect'
import { Panel, Kpi, DefList, EmptyState, StatusPill, Meter, TabBar } from '../components/ui'
import { useIsVirtual } from '../components/useIsVirtual'

type Tab = 'overview' | 'vpn' | 'ha' | 'licenses' | 'interfaces' | 'operations'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const HA_LABEL: Record<string, string> = {
  standalone: 'Standalone', 'active-active': 'Active-Active',
  'active-passive': 'Active-Passive', unknown: 'Unknown',
}
const operLabel = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')
const speedLabel = (mbps?: number | null) => (mbps ? (mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`) : '—')

// expiryState classifies a license/contract expiry into a tone + human label.
function expiryState(expiry?: string | null): { tone: 'up' | 'warning' | 'down' | 'unknown'; label: string; days: number | null } {
  if (!expiry) return { tone: 'unknown', label: '—', days: null }
  const t = Date.parse(expiry)
  if (Number.isNaN(t)) return { tone: 'unknown', label: expiry, days: null }
  const days = Math.floor((t - Date.now()) / 86400000)
  if (days < 0) return { tone: 'down', label: `expired ${-days}d ago`, days }
  if (days <= 30) return { tone: 'warning', label: `${days}d left`, days }
  if (days <= 90) return { tone: 'warning', label: `${days}d left`, days }
  return { tone: 'up', label: `${days}d left`, days }
}

// FirewallDetail — enterprise firewall console (FortiGate + generic SNMP). Tabbed:
//   Overview   → posture KPIs, HA + resources, VPN/license health-at-a-glance
//   VPN        → IPsec tunnel roster with up/down + traffic
//   HA Cluster → cluster members, sync state, per-member load
//   Licenses   → support/FortiGuard contracts with expiry warnings
//   Interfaces → SNMP interface table
//   Operations → credential health + monitoring checks
// Re-scan / Repair check / credential binding live in the shared DeviceHeader
// (identical to the wireless controller page).
export function FirewallDetail() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState<Tab>('overview')
  const isVirtual = useIsVirtual(id)

  const status = useQuery({ queryKey: ['fwstatus', id], queryFn: () => api.get<FirewallStatus>(`/devices/${id}/firewall-status`), retry: false })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const tunnels = useQuery({ queryKey: ['vpn', id], queryFn: () => api.get<VpnTunnel[]>(`/devices/${id}/vpn-tunnels`) })
  const members = useQuery({ queryKey: ['ha', id], queryFn: () => api.get<HAMember[]>(`/devices/${id}/ha-members`) })
  const lics = useQuery({ queryKey: ['lic', id], queryFn: () => api.get<License[]>(`/devices/${id}/licenses`) })
  const ifaces = useQuery({ queryKey: ['fw-ifaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })

  const fm = useMemo(() => new Map((facts.data ?? []).map((f) => [f.key, f.value ?? ''])), [facts.data])
  const num = (k: string) => { const v = Number(fm.get(k)); return Number.isFinite(v) && fm.has(k) ? v : null }
  const s = status.data
  const tun = tunnels.data ?? []
  const tunUp = tun.filter((t) => t.status === 'up').length
  const tunDown = tun.length - tunUp
  const mem = members.data ?? []
  const unsynced = mem.filter((m) => m.sync_status && m.sync_status !== 'synchronized').length
  const licList = lics.data ?? []
  const ifList = ifaces.data ?? []
  const ifUp = ifList.filter((i) => i.oper_status === 1).length
  const cpu = num('cpu.load_pct'), memPct = num('memory.used_pct'), disk = num('disk.used_pct')

  // Soonest-expiring contract drives the licenses banner/KPI.
  const soonest = useMemo(() => {
    let best: { l: License; st: ReturnType<typeof expiryState> } | null = null
    for (const l of licList) {
      const st = expiryState(l.expiry)
      if (st.days == null) continue
      if (!best || st.days < (best.st.days ?? Infinity)) best = { l, st }
    }
    return best
  }, [licList])
  const expiredCount = licList.filter((l) => (expiryState(l.expiry).days ?? 1) < 0).length
  const expiringSoon = licList.filter((l) => { const d = expiryState(l.expiry).days; return d != null && d >= 0 && d <= 30 }).length

  const totalIn = tun.reduce((a, t) => a + (t.in_octets ?? 0), 0)
  const totalOut = tun.reduce((a, t) => a + (t.out_octets ?? 0), 0)

  const tabs = [
    { key: 'overview', label: 'Overview', icon: LayoutDashboard },
    { key: 'vpn', label: 'VPN Tunnels', icon: KeyRound, count: tun.length || undefined },
    { key: 'ha', label: 'HA Cluster', icon: ServerIcon, count: mem.length || undefined },
    { key: 'licenses', label: 'Licenses', icon: Shield, count: licList.length || undefined },
    { key: 'interfaces', label: 'Interfaces', icon: Cable, count: ifList.length || undefined },
    { key: 'operations', label: 'Operations', icon: Settings },
  ]

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Flame} showCredential={false} />

      <TabBar tabs={tabs} active={tab} onChange={(k) => setTab(k as Tab)} />

      {/* ── OVERVIEW ──────────────────────────────────────────────────────── */}
      {tab === 'overview' && (
        <>
          {isVirtual && (
            <div className="enc-banner info" style={{ marginBottom: 12 }}>
              <Flame size={14} style={{ verticalAlign: -2 }} /> This is a manually modeled virtual firewall. Its data is maintained manually — use <strong>Edit virtual device</strong> in the header to update interfaces, HA, VPN tunnels and licenses.
            </div>
          )}
          {!s && !status.isLoading && !isVirtual && (
            <div className="enc-banner warn" style={{ marginBottom: 12 }}>
              <AlertTriangle size={14} style={{ verticalAlign: -2 }} /> No firewall status collected yet. Bind a working SNMP credential and re-scan this device to populate HA, sessions, VPN and resource data.
              <button className="btn btn-ghost btn-xs" style={{ marginLeft: 8 }} onClick={() => setTab('operations')}>Open Operations</button>
            </div>
          )}
          {(expiredCount > 0 || expiringSoon > 0) && (
            <div className={'enc-banner ' + (expiredCount > 0 ? 'crit' : 'warn')} style={{ marginBottom: 12 }}>
              <Shield size={14} style={{ verticalAlign: -2 }} />{' '}
              {expiredCount > 0 && <strong>{expiredCount} contract{expiredCount > 1 ? 's' : ''} expired.</strong>}{' '}
              {expiringSoon > 0 && <>{expiringSoon} expiring within 30 days.</>}
              <button className="btn btn-ghost btn-xs" style={{ marginLeft: 8 }} onClick={() => setTab('licenses')}>Review licenses</button>
            </div>
          )}

          <div className="kpi-grid kpi-6">
            <Kpi label="HA Mode" value={s ? (HA_LABEL[s.ha_mode] ?? s.ha_mode) : '—'} icon={Shield} tone="info" sub={s?.ha_group_name || 'cluster role'} />
            <Kpi label="Active Sessions" value={s?.session_count?.toLocaleString() ?? '—'} icon={Activity} tone="default" sub="live flows" />
            <Kpi label="VPN Tunnels" value={tun.length ? `${tunUp}/${tun.length}` : '—'} icon={KeyRound} tone={tunDown > 0 ? 'crit' : tun.length ? 'ok' : 'default'} sub={tun.length ? `${tunDown} down` : 'none reported'} onClick={tun.length ? () => setTab('vpn') : undefined} />
            <Kpi label="Cluster Members" value={s?.ha_member_count ?? mem.length ?? '—'} icon={ServerIcon} tone={unsynced > 0 ? 'warn' : 'default'} sub={unsynced > 0 ? `${unsynced} unsynced` : (mem.length ? 'in sync' : undefined)} onClick={mem.length ? () => setTab('ha') : undefined} />
            <Kpi label="CPU" value={cpu != null ? `${cpu}%` : '—'} icon={Cpu} tone={cpu != null && cpu >= 90 ? 'crit' : cpu != null && cpu >= 75 ? 'warn' : 'default'} sub="utilisation" />
            <Kpi label="Memory" value={memPct != null ? `${memPct}%` : '—'} icon={Gauge} tone={memPct != null && memPct >= 90 ? 'crit' : memPct != null && memPct >= 75 ? 'warn' : 'default'} sub="utilisation" />
          </div>

          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="Firewall Status" icon={Shield}>
              {!s && <EmptyState icon={Shield} title="Not collected" message={isVirtual ? 'Manually modeled — HA / status is maintained manually via Edit virtual device.' : 'Bind a working SNMP credential and re-scan to populate firewall status.'} />}
              {s && (
                <DefList items={[
                  { label: 'HA mode', value: <span className="badge badge-access">{HA_LABEL[s.ha_mode] ?? s.ha_mode}</span> },
                  { label: 'HA group', value: s.ha_group_name || '—' },
                  { label: 'Cluster members', value: s.ha_member_count },
                  { label: 'Active sessions', value: s.session_count?.toLocaleString() ?? '—' },
                  { label: 'Disk used', value: fmtBytes(Number(fm.get('disk.used_bytes')) || null) },
                  { label: 'Memory total', value: fmtBytes(Number(fm.get('memory.total_bytes')) || null) },
                  { label: 'Last status seen', value: s.last_seen_at ? new Date(s.last_seen_at).toLocaleString() : '—' },
                ]} />
              )}
            </Panel>
            <Panel title="Resources" icon={Cpu} subtitle="SNMP system gauges">
              {(cpu == null && memPct == null && disk == null)
                ? <EmptyState icon={Cpu} title="No resource gauges" message="CPU / memory / disk are collected over SNMP when supported by the device." />
                : (
                  <div className="stack" style={{ gap: 14 }}>
                    {cpu != null && <Meter label="CPU load" value={cpu} />}
                    {memPct != null && <Meter label="Memory used" value={memPct} />}
                    {disk != null && <Meter label="Disk used" value={disk} />}
                  </div>
                )}
            </Panel>
          </div>

          <div className="grid-2" style={{ alignItems: 'start' }}>
            <Panel title="VPN Health" icon={KeyRound} subtitle={tun.length ? `${tunUp} up · ${tunDown} down` : undefined}>
              {tun.length === 0
                ? <EmptyState icon={KeyRound} title="No IPsec tunnels" message="FortiGate IPsec phase-1/2 tunnels appear here once collected." />
                : (
                  <>
                    <div className="row" style={{ gap: 8, marginBottom: 12, flexWrap: 'wrap' }}>
                      <span className="badge badge-up">{tunUp} up</span>
                      {tunDown > 0 && <span className="badge badge-down">{tunDown} down</span>}
                      <span className="badge badge-muted">{tun.length} total</span>
                    </div>
                    <div className="stack" style={{ gap: 6 }}>
                      <div className="row" style={{ justifyContent: 'space-between' }}>
                        <span className="muted" style={{ fontSize: 12 }}><ArrowDownToLine size={12} style={{ verticalAlign: -2 }} /> Aggregate in</span>
                        <strong className="mono">{fmtBytes(totalIn)}</strong>
                      </div>
                      <div className="row" style={{ justifyContent: 'space-between' }}>
                        <span className="muted" style={{ fontSize: 12 }}><ArrowUpFromLine size={12} style={{ verticalAlign: -2 }} /> Aggregate out</span>
                        <strong className="mono">{fmtBytes(totalOut)}</strong>
                      </div>
                    </div>
                    <button className="btn btn-ghost btn-sm" style={{ marginTop: 12 }} onClick={() => setTab('vpn')}>View all tunnels →</button>
                  </>
                )}
            </Panel>
            <Panel title="Licenses & Contracts" icon={Shield} subtitle={licList.length ? `${licList.length} tracked` : undefined}>
              {licList.length === 0
                ? <EmptyState icon={Shield} title="No contracts reported" message="FortiGuard / support contracts appear here when collected." />
                : (
                  <>
                    {soonest && (
                      <DefList items={[
                        { label: 'Soonest expiry', value: <><strong>{soonest.l.contract}</strong> <StatusPill status={soonest.st.tone} label={soonest.st.label} /></> },
                        { label: 'Expired', value: expiredCount },
                        { label: 'Expiring ≤30d', value: expiringSoon },
                      ]} />
                    )}
                    <button className="btn btn-ghost btn-sm" style={{ marginTop: 12 }} onClick={() => setTab('licenses')}>View all contracts →</button>
                  </>
                )}
            </Panel>
          </div>
        </>
      )}

      {/* ── VPN TUNNELS ───────────────────────────────────────────────────── */}
      {tab === 'vpn' && (
        <Panel title="IPsec VPN Tunnels" icon={KeyRound} subtitle={tun.length ? `${tunUp} up / ${tun.length} total · in ${fmtBytes(totalIn)} · out ${fmtBytes(totalOut)}` : undefined} pad={false}>
          {tunnels.data && tun.length === 0 && <EmptyState icon={KeyRound} title="No IPsec tunnels reported" message="FortiGate IPsec phase-1/2 tunnels appear here when collected." />}
          {tun.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Tunnel</th><th>Status</th><th>Remote GW</th><th>In</th><th>Out</th><th>Last seen</th></tr></thead>
              <tbody>
                {[...tun].sort((a, b) => (a.status === b.status ? a.tunnel_name.localeCompare(b.tunnel_name) : a.status === 'up' ? 1 : -1)).map((t) => (
                  <tr key={t.id}>
                    <td className="cell-name">{t.tunnel_name}{t.p1_name && <small className="muted"> ({t.p1_name})</small>}</td>
                    <td><StatusPill status={t.status === 'up' ? 'up' : 'down'} label={t.status} /></td>
                    <td className="mono">{t.remote_gw ?? '—'}</td>
                    <td className="mono">{fmtBytes(t.in_octets)}</td>
                    <td className="mono">{fmtBytes(t.out_octets)}</td>
                    <td className="muted">{t.last_seen_at ? new Date(t.last_seen_at).toLocaleString() : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {/* ── HA CLUSTER ────────────────────────────────────────────────────── */}
      {tab === 'ha' && (
        <>
          <div className="kpi-grid">
            <Kpi label="HA Mode" value={s ? (HA_LABEL[s.ha_mode] ?? s.ha_mode) : '—'} icon={Shield} tone="info" sub={s?.ha_group_name || undefined} />
            <Kpi label="Members" value={mem.length || (s?.ha_member_count ?? '—')} icon={ServerIcon} tone="default" />
            <Kpi label="In sync" value={mem.length ? `${mem.length - unsynced}/${mem.length}` : '—'} icon={Activity} tone={unsynced > 0 ? 'crit' : 'ok'} sub={unsynced > 0 ? `${unsynced} unsynced` : undefined} />
          </div>
          <Panel title="Cluster Members" icon={ServerIcon} subtitle={mem.length ? `${mem.length}` : undefined} pad={false}>
            {members.data && mem.length === 0 && <EmptyState icon={ServerIcon} title="No cluster members" message="HA member details appear here for clustered firewalls." />}
            {mem.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Serial</th><th>Hostname</th><th>Sync</th><th>CPU</th><th>Memory</th><th>Sessions</th></tr></thead>
                <tbody>
                  {mem.map((m) => (
                    <tr key={m.id}>
                      <td className="mono">{m.serial}</td>
                      <td>{m.hostname ?? '—'}</td>
                      <td><StatusPill status={m.sync_status === 'synchronized' ? 'up' : m.sync_status === 'unsynchronized' ? 'down' : 'unknown'} label={m.sync_status} /></td>
                      <td>{m.cpu_pct != null ? `${m.cpu_pct}%` : '—'}</td>
                      <td>{m.mem_pct != null ? `${m.mem_pct}%` : '—'}</td>
                      <td>{m.session_count?.toLocaleString() ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </>
      )}

      {/* ── LICENSES ──────────────────────────────────────────────────────── */}
      {tab === 'licenses' && (
        <Panel title="License / Subscription Contracts" icon={Shield} subtitle={licList.length ? `${licList.length} · ${expiredCount} expired · ${expiringSoon} expiring ≤30d` : undefined} pad={false}>
          {lics.data && licList.length === 0 && <EmptyState icon={Shield} title="No contracts reported" message="FortiGuard / support contracts appear here when collected." />}
          {licList.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Contract</th><th>Expiry</th><th>Status</th></tr></thead>
              <tbody>
                {[...licList].sort((a, b) => (expiryState(a.expiry).days ?? Infinity) - (expiryState(b.expiry).days ?? Infinity)).map((l) => {
                  const st = expiryState(l.expiry)
                  return (
                    <tr key={l.id}>
                      <td className="cell-name">{l.contract}</td>
                      <td className="mono">{l.expiry ?? '—'}</td>
                      <td><StatusPill status={st.tone} label={st.label} /></td>
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
        <Panel title="Interfaces" icon={Cable} subtitle={ifList.length ? `${ifList.length} · ${ifUp} up` : undefined} pad={false}>
          {ifaces.data && ifList.length === 0 && <EmptyState icon={Cable} title={isVirtual ? 'No interfaces entered' : 'No interfaces collected'} message={isVirtual ? 'Add interfaces via Edit virtual device.' : 'Bind a working SNMP credential and collect this firewall.'} />}
          {ifList.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Interface</th><th>Alias</th><th>Oper</th><th>Speed</th><th>MAC</th></tr></thead>
              <tbody>
                {[...ifList].sort((a, b) => a.if_index - b.if_index).map((i) => (
                  <tr key={i.id}>
                    <td className="cell-name">{i.if_name ?? i.if_descr ?? `if ${i.if_index}`}</td>
                    <td>{i.if_alias ?? '—'}</td>
                    <td><StatusPill status={operLabel(i.oper_status)} label={operLabel(i.oper_status)} /></td>
                    <td className="mono">{speedLabel(i.speed_mbps)}</td>
                    <td className="mono">{i.mac ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}

      {/* ── OPERATIONS ────────────────────────────────────────────────────── */}
      {tab === 'operations' && (
        <>
          <Panel title="Collection Credential" icon={KeyRound}>
            <CredentialBindSelect deviceId={id!} />
            <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
              The credential HIMS uses to collect from this firewall (SNMP for status/VPN/HA). After binding, use <strong>Re-scan this device</strong> in the header — or run a collection — to apply it.
            </p>
          </Panel>
          <DeviceCredentialHealth deviceId={id!} category="firewall" />
          <DeviceOps deviceId={id!} />
        </>
      )}
    </div>
  )
}
