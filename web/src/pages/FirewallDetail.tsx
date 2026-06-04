import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Flame, Shield, Activity, Cpu, KeyRound, Cable } from 'lucide-react'
import { api, type FirewallStatus, type VpnTunnel, type HAMember, type License, type DeviceFact, type Interface } from '../api'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceHeader } from '../components/DeviceHeader'
import { Panel, Kpi, DefList, EmptyState, StatusPill, Meter } from '../components/ui'

function fmtBytes(n?: number | null): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const HA_LABEL: Record<string, string> = {
  standalone: 'Standalone', 'active-active': 'Active-Active',
  'active-passive': 'Active-Passive', unknown: 'Unknown',
}
const operLabel = (s?: number | null) => (s === 1 ? 'up' : s === 2 ? 'down' : 'unknown')

// Firewall Intelligence (#14, FortiGate): HA + resource KPIs, VPN tunnels with
// up/down summary, cluster members, licenses, and interfaces.
export function FirewallDetail() {
  const { id } = useParams<{ id: string }>()
  const status = useQuery({ queryKey: ['fwstatus', id], queryFn: () => api.get<FirewallStatus>(`/devices/${id}/firewall-status`), retry: false })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const tunnels = useQuery({ queryKey: ['vpn', id], queryFn: () => api.get<VpnTunnel[]>(`/devices/${id}/vpn-tunnels`) })
  const members = useQuery({ queryKey: ['ha', id], queryFn: () => api.get<HAMember[]>(`/devices/${id}/ha-members`) })
  const lics = useQuery({ queryKey: ['lic', id], queryFn: () => api.get<License[]>(`/devices/${id}/licenses`) })
  const ifaces = useQuery({ queryKey: ['fw-ifaces', id], queryFn: () => api.get<Interface[]>(`/devices/${id}/interfaces`) })

  const fm = new Map((facts.data ?? []).map((f) => [f.key, f.value ?? '']))
  const s = status.data
  const tun = tunnels.data ?? []
  const tunUp = tun.filter((t) => t.status === 'up').length
  const num = (k: string) => { const v = Number(fm.get(k)); return Number.isFinite(v) ? v : null }
  const cpu = num('cpu.load_pct'), mem = num('memory.used_pct'), disk = num('disk.used_pct')

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Flame} />

      <div className="kpi-grid">
        <Kpi label="HA Mode" value={s ? (HA_LABEL[s.ha_mode] ?? s.ha_mode) : '—'} icon={Shield} tone="info" sub={s?.ha_group_name || undefined} />
        <Kpi label="Active Sessions" value={s?.session_count?.toLocaleString() ?? '—'} icon={Activity} />
        <Kpi label="VPN Tunnels" value={tun.length ? `${tunUp}/${tun.length}` : '—'} icon={KeyRound} tone={tun.length && tunUp < tun.length ? 'warn' : 'default'} sub="up / total" />
        <Kpi label="Cluster Members" value={s?.ha_member_count ?? members.data?.length ?? '—'} icon={Cpu} />
      </div>

      <div className="grid-2" style={{ alignItems: 'start' }}>
        <Panel title="Firewall Status" icon={Shield}>
          {!s && <div className="muted">No firewall status collected yet. Bind a working SNMP credential.</div>}
          {s && (
            <DefList items={[
              { label: 'HA mode', value: <span className="badge badge-access">{HA_LABEL[s.ha_mode] ?? s.ha_mode}</span> },
              { label: 'HA group', value: s.ha_group_name || '—' },
              { label: 'Cluster members', value: s.ha_member_count },
              { label: 'Active sessions', value: s.session_count?.toLocaleString() ?? '—' },
              { label: 'Disk used', value: fmtBytes(Number(fm.get('disk.used_bytes')) || null) },
            ]} />
          )}
        </Panel>
        <Panel title="Resources" icon={Cpu}>
          {(cpu == null && mem == null && disk == null)
            ? <div className="muted">CPU/memory/disk not collected for this device.</div>
            : (
              <div className="stack" style={{ gap: 12 }}>
                {cpu != null && <Meter label="CPU" value={cpu} />}
                {mem != null && <Meter label="Memory" value={mem} />}
                {disk != null && <Meter label="Disk" value={disk} />}
              </div>
            )}
        </Panel>
      </div>

      <Panel title="VPN Tunnels" icon={KeyRound} subtitle={tun.length ? `${tunUp} up / ${tun.length} total` : undefined} pad={false}>
        {tunnels.data && tun.length === 0 && <EmptyState icon={KeyRound} title="No IPsec tunnels reported" message="FortiGate IPsec phase-1/2 tunnels appear here when collected." />}
        {tun.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Tunnel</th><th>Status</th><th>Remote GW</th><th>In</th><th>Out</th></tr></thead>
            <tbody>
              {tun.map((t) => (
                <tr key={t.id}>
                  <td className="cell-name">{t.tunnel_name}{t.p1_name && <small className="muted"> ({t.p1_name})</small>}</td>
                  <td><StatusPill status={t.status === 'up' ? 'up' : 'down'} label={t.status} /></td>
                  <td className="mono">{t.remote_gw ?? '—'}</td>
                  <td>{fmtBytes(t.in_octets)}</td>
                  <td>{fmtBytes(t.out_octets)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {members.data && members.data.length > 0 && (
        <Panel title="Cluster Members" icon={Cpu} subtitle={`${members.data.length}`} pad={false}>
          <table className="data-table">
            <thead><tr><th>Serial</th><th>Hostname</th><th>CPU</th><th>Mem</th><th>Sync</th></tr></thead>
            <tbody>
              {members.data.map((m) => (
                <tr key={m.id}>
                  <td className="mono">{m.serial}</td>
                  <td>{m.hostname ?? '—'}</td>
                  <td>{m.cpu_pct != null ? `${m.cpu_pct}%` : '—'}</td>
                  <td>{m.mem_pct != null ? `${m.mem_pct}%` : '—'}</td>
                  <td><StatusPill status={m.sync_status === 'synchronized' ? 'up' : m.sync_status === 'unsynchronized' ? 'down' : 'unknown'} label={m.sync_status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}

      <div className="grid-2" style={{ alignItems: 'start' }}>
        <Panel title="License / Subscription" icon={KeyRound} subtitle={lics.data?.length ? `${lics.data.length}` : undefined} pad={false}>
          {lics.data && lics.data.length === 0 && <EmptyState icon={KeyRound} title="No contracts reported" message="FortiGuard / support contracts appear here when collected." />}
          {lics.data && lics.data.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Contract</th><th>Expiry</th></tr></thead>
              <tbody>{lics.data.map((l) => <tr key={l.id}><td className="cell-name">{l.contract}</td><td>{l.expiry ?? '—'}</td></tr>)}</tbody>
            </table>
          )}
        </Panel>
        <Panel title="Interfaces" icon={Cable} subtitle={ifaces.data?.length ? `${ifaces.data.length}` : undefined} pad={false}>
          {ifaces.data && ifaces.data.length === 0 && <EmptyState icon={Cable} title="No interfaces collected" message="Bind a working SNMP credential and collect this firewall." />}
          {ifaces.data && ifaces.data.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Interface</th><th>Oper</th><th>Speed</th></tr></thead>
              <tbody>
                {[...ifaces.data].sort((a, b) => a.if_index - b.if_index).slice(0, 50).map((i) => (
                  <tr key={i.id}>
                    <td className="cell-name">{i.if_name ?? i.if_descr ?? `if ${i.if_index}`}</td>
                    <td><StatusPill status={operLabel(i.oper_status)} label={operLabel(i.oper_status)} /></td>
                    <td className="mono">{i.speed_mbps ? (i.speed_mbps >= 1000 ? `${i.speed_mbps / 1000} Gb/s` : `${i.speed_mbps} Mb/s`) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>

      <DeviceOps deviceId={id!} />
    </div>
  )
}
