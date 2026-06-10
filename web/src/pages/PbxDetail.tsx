import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { Phone, PhoneCall, Server, Hash, Cpu } from 'lucide-react'
import { api, type DeviceFact, type PhoneExtension } from '../api'
import { DeviceHeader } from '../components/DeviceHeader'
import {
  Panel, Kpi, EmptyState, TabBar, usePaged, Pager, Donut, Legend, BarList, colorFor, timeAgo,
} from '../components/ui'

// Voice / PBX Intelligence: the subscriber/phone registry. Cisco CUCM is pulled
// over AXL (executeSQLQuery → directory number + MAC from the SEP name); Alcatel
// OmniPCX over the mgr telnet CLI (directory numbers). Tabbed view: searchable
// directory + breakdowns by model and device pool.
const SOURCE_LABEL: Record<string, string> = { axl: 'Cisco CUCM (AXL)', omnipcx: 'Alcatel OmniPCX (mgr)' }

export function PbxDetail() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState('directory')
  const [q, setQ] = useState('')

  const phones = useQuery({ queryKey: ['phones', id], queryFn: () => api.get<PhoneExtension[]>(`/devices/${id}/phones`) })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })

  const list = useMemo(() => phones.data ?? [], [phones.data])
  const f = new Map((facts.data ?? []).map((x) => [x.key, x.value ?? '']))
  const withExt = list.filter((p) => (p.extension ?? '').trim() !== '').length
  const withIP = list.filter((p) => (p.ip_address ?? '').trim() !== '').length
  const pools = useMemo(() => countBy(list, (p) => p.device_pool), [list])
  const models = useMemo(() => countBy(list, (p) => p.model), [list])
  const source = list.find((p) => p.collection_source)?.collection_source ?? ''
  const lastSeen = list.reduce<string | null>((a, p) => (p.last_seen_at && (!a || p.last_seen_at > a) ? p.last_seen_at : a), null)

  const paged = usePaged(list, {
    pageSize: 25, filter: q,
    match: (p, s) =>
      (p.extension ?? '').toLowerCase().includes(s) || p.name.toLowerCase().includes(s) ||
      (p.mac_address ?? '').toLowerCase().includes(s) || (p.description ?? '').toLowerCase().includes(s) ||
      (p.model ?? '').toLowerCase().includes(s) || (p.device_pool ?? '').toLowerCase().includes(s) ||
      (p.ip_address ?? '').toLowerCase().includes(s),
  })

  const showIP = withIP > 0
  const tabs = [
    { key: 'directory', label: 'Directory', icon: PhoneCall, count: list.length },
    { key: 'models', label: 'By Model', icon: Cpu, count: models.length },
    { key: 'pools', label: 'By Device Pool', icon: Server, count: pools.length },
  ]

  return (
    <div>
      <DeviceHeader deviceId={id!} icon={Phone} />

      <div className="kpi-grid">
        <Kpi label="Phones / Subscribers" value={list.length || (f.get('phone_count') ?? '—')} icon={PhoneCall} tone="info" />
        <Kpi label="With Directory No." value={list.length ? `${withExt}` : '—'} sub={list.length ? `${pct(withExt, list.length)}%` : undefined} icon={Hash} />
        <Kpi label="Device Pools" value={pools.length || '—'} icon={Server} />
        <Kpi label="Phone Models" value={models.length || '—'} icon={Cpu} />
      </div>

      {list.length > 0 && (
        <p className="muted" style={{ margin: '2px 2px 12px', fontSize: 12 }}>
          Collected via <strong>{SOURCE_LABEL[source] ?? source ?? 'voice API'}</strong>
          {lastSeen ? ` · ${timeAgo(lastSeen)}` : ''} · {withExt} of {list.length} have a directory number
          {showIP ? ` · ${withIP} with IP` : ''}
        </p>
      )}

      {phones.data && list.length === 0 ? (
        <Panel title="Phones" icon={PhoneCall} pad={false}>
          <EmptyState icon={PhoneCall} title="No phones collected"
            message="CUCM is pulled over AXL (directory number + MAC); OmniPCX over the mgr telnet CLI. Bind the voice credential and run collection to populate." />
        </Panel>
      ) : (
        <>
          <TabBar tabs={tabs} active={tab} onChange={setTab} />

          {tab === 'directory' && (
            <Panel title="Phone Directory" icon={PhoneCall} pad={false}
              actions={
                <input className="input input-sm" placeholder="Search ext / name / MAC / pool…" value={q}
                  onChange={(e) => { setQ(e.target.value); paged.setPage(0) }} style={{ minWidth: 240 }} />
              }>
              <table className="data-table">
                <thead><tr>
                  <th>Directory No.</th><th>Device / MAC</th><th>Model</th>
                  <th>Description</th><th>Device Pool</th>{showIP && <th>IP</th>}
                </tr></thead>
                <tbody>
                  {paged.slice.map((p) => (
                    <tr key={p.id}>
                      <td className="cell-name">{p.extension || '—'}</td>
                      <td>
                        <div>{p.name}</div>
                        {p.mac_address && <div className="muted" style={{ fontSize: 11, fontFamily: 'var(--mono, monospace)' }}>{p.mac_address}</div>}
                      </td>
                      <td>{p.model ?? '—'}</td>
                      <td>{p.description ?? '—'}</td>
                      <td>{p.device_pool ?? '—'}</td>
                      {showIP && <td>{p.ip_address ?? '—'}</td>}
                    </tr>
                  ))}
                  {paged.total === 0 && (
                    <tr><td colSpan={showIP ? 6 : 5} className="muted" style={{ textAlign: 'center', padding: 20 }}>No phones match “{q}”.</td></tr>
                  )}
                </tbody>
              </table>
              <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
            </Panel>
          )}

          {tab === 'models' && (
            <Panel title="Phones by Model" icon={Cpu}>
              {models.length === 0 ? <EmptyState icon={Cpu} title="No model data" message="Model is reported by CUCM per phone." /> : (
                <div style={{ display: 'flex', gap: 28, flexWrap: 'wrap', alignItems: 'center' }}>
                  <Donut data={donutData(models)} centerLabel="phones" centerValue={String(list.length)} />
                  <div style={{ flex: 1, minWidth: 260 }}>
                    <Legend data={donutData(models)} total={list.length} />
                  </div>
                </div>
              )}
            </Panel>
          )}

          {tab === 'pools' && (
            <Panel title="Phones by Device Pool" icon={Server}>
              {pools.length === 0 ? <EmptyState icon={Server} title="No device-pool data" message="Device pool groups phones by site/region in CUCM." /> : (
                <BarList rows={pools.map((m) => ({ label: m.label, value: m.value }))} />
              )}
            </Panel>
          )}
        </>
      )}
    </div>
  )
}

function countBy(list: PhoneExtension[], key: (p: PhoneExtension) => string | null | undefined) {
  const m = new Map<string, number>()
  for (const p of list) {
    const k = (key(p) ?? '').trim()
    if (!k) continue
    m.set(k, (m.get(k) ?? 0) + 1)
  }
  return [...m.entries()].map(([label, value]) => ({ label, value })).sort((a, b) => b.value - a.value)
}

function donutData(rows: { label: string; value: number }[]) {
  return rows.slice(0, 8).map((r) => ({ label: r.label, value: r.value, color: colorFor(r.label) }))
}

function pct(n: number, total: number) {
  return total ? Math.round((n / total) * 100) : 0
}
