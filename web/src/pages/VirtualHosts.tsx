import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { HardDrive, Server, Boxes, Database, Network } from 'lucide-react'
import { api, type VHost } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager } from '../components/ui'
import { AddDeviceButton } from '../components/AddDeviceButton'
import { ManagementBadge } from '../components/StatusBadges'

function fmtBytes(n?: number): string {
  if (!n) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i <= 1 ? 0 : 1)} ${u[i]}`
}
const typeBadge = (t: string) => (t === 'hyperv'
  ? <span className="badge" style={{ background: '#1e3a8a', color: '#fff' }}>Hyper-V</span>
  : <span className="badge" style={{ background: '#166534', color: '#fff' }}>ESXi</span>)
const healthBadge = (h: string) => {
  const m: Record<string, string> = { ok: 'badge-up', partial: 'badge-warn', failed: 'badge-crit', none: 'badge-unknown' }
  return <span className={'badge ' + (m[h] ?? 'badge-unknown')}>{h}</span>
}

// Virtual Hosts — all virtualization hosts (ESXi + Hyper-V) with VM/storage/network
// rollups, management state, and collection health. Data from /virtualization/hosts
// (never the raw DB). Click a host for the full detail tabs.
export function VirtualHosts() {
  const { data, isLoading, error } = useQuery({ queryKey: ['vhosts'], queryFn: () => api.get<VHost[]>('/virtualization/hosts') })
  const [q, setQ] = useState('')
  const all = data ?? []
  const esxi = all.filter((h) => h.hypervisor_type === 'esxi').length
  const hyperv = all.filter((h) => h.hypervisor_type === 'hyperv').length
  const totalVMs = all.reduce((a, h) => a + h.vm_count, 0)
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    if (!t) return all
    return all.filter((h) => h.name.toLowerCase().includes(t) || h.ip.includes(t) || h.hypervisor_type.includes(t))
  }, [data, q])
  const paged = usePaged(filtered, { pageSize: 15 })

  return (
    <div>
      <PageHeader title="Virtual Hosts" subtitle="ESXi and Hyper-V hypervisors — VMs, storage, networks and collection health" icon={HardDrive}
        actions={<AddDeviceButton defaultType="virtual_host_esxi" label="Add Virtual Host" />} />
      <div className="kpi-grid">
        <Kpi label="Hosts" value={all.length} icon={HardDrive} tone="info" />
        <Kpi label="ESXi" value={esxi} icon={Server} tone="default" />
        <Kpi label="Hyper-V" value={hyperv} icon={Server} tone="default" />
        <Kpi label="Virtual machines" value={totalVMs} icon={Boxes} tone="default" />
      </div>

      <Panel title="Hypervisor hosts" subtitle={`${all.length} host(s)`} pad={false}>
        {isLoading && <div className="loading">Loading virtual hosts…</div>}
        {error && <div style={{ padding: 16 }}><div className="error-msg">Failed to load: {(error as Error).message}</div></div>}
        {data && all.length === 0 && <EmptyState icon={HardDrive} title="No virtual hosts" message="ESXi hosts are detected via port 902 + vSphere; Hyper-V hosts via the Windows agent. Run discovery to populate." />}
        {all.length > 0 && (
          <>
          <div style={{ padding: '8px 10px' }}>
            <input placeholder="Filter by name / IP / type…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }}
              style={{ padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13, width: 320, maxWidth: '100%' }} />
          </div>
          <table className="data-table">
            <thead><tr>
              <th>Host</th><th>IP</th><th>Type</th><th>Management</th><th>VMs</th>
              <th>CPU</th><th>Memory</th><th><Database size={13} /> Storage</th><th><Network size={13} /> Nets</th><th>Health</th><th>Last collected</th>
            </tr></thead>
            <tbody>
              {paged.slice.map((h) => (
                <tr key={h.id}>
                  <td><Link className="cell-name" to={`/virtual-hosts/${h.id}`}>{h.name}</Link></td>
                  <td className="mono">{h.ip || '—'}</td>
                  <td>{typeBadge(h.hypervisor_type)}</td>
                  <td><ManagementBadge value={h.management} /></td>
                  <td title={`${h.running} running / ${h.stopped} stopped`}>{h.vm_count} <small className="muted">({h.running}▶ {h.stopped}⏹)</small></td>
                  <td className="muted" style={{ fontSize: 12 }}>{h.cpu_cores ? `${h.cpu_cores} cores` : '—'}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{h.mem_total_bytes ? fmtBytes(h.mem_total_bytes) : '—'}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{h.datastore_count ? `${h.datastore_count} · ${fmtBytes(h.datastore_capacity)}` : (h.hypervisor_type === 'hyperv' ? 'local' : '—')}</td>
                  <td>{h.network_count || '—'}</td>
                  <td>{healthBadge(h.health)}</td>
                  <td className="muted" style={{ fontSize: 11 }}>{h.last_collected ? new Date(h.last_collected).toLocaleString() : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
          </>
        )}
      </Panel>
    </div>
  )
}
