import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import {
  Route as RouteIcon, Search, MonitorSmartphone, Network, Flame, Router, Server,
  ArrowDown, CircleHelp, ShieldCheck, Clock, Share2, List, Wifi,
} from 'lucide-react'
import { api, type SearchResult } from '../api'
import { PageHeader, Panel, EmptyState, timeAgo } from '../components/ui'
import { PathGraph } from '../components/PathGraph'
import { ROLE_COLOR, roleLabel } from '../components/topologyColors'

// Device Path Finder — search by IP / MAC / hostname / device name and trace the
// Layer-2 path: endpoint → MAC → switch → port → VLAN → uplink → core →
// firewall/gateway, with source attribution (MAC table / ARP / LLDP-CDP),
// freshness and a confidence assessment. All data comes from /search (topology
// engine); nothing is fabricated.

const CONF: Record<string, { cls: string; label: string }> = {
  high: { cls: 'badge-up', label: 'High confidence' },
  medium: { cls: 'badge-warning', label: 'Medium confidence' },
  low: { cls: 'badge-down', label: 'Low confidence' },
  none: { cls: 'badge-unknown', label: 'No path found' },
}

const ROLE_META: Record<string, { icon: typeof Network; label: string; color: string }> = {
  endpoint: { icon: MonitorSmartphone, label: 'Endpoint', color: 'var(--brand)' },
  access: { icon: Network, label: 'Access switch', color: 'var(--ok)' },
  uplink: { icon: Network, label: 'Uplink switch', color: 'var(--info, #38bdf8)' },
  distribution: { icon: Network, label: 'Distribution', color: 'var(--info, #38bdf8)' },
  core: { icon: Network, label: 'Core switch', color: '#8b5cf6' },
  gateway: { icon: Router, label: 'Gateway / Router', color: 'var(--warn)' },
  firewall: { icon: Flame, label: 'Firewall', color: 'var(--crit)' },
}

function SourceChip({ source }: { source?: string | null }) {
  if (!source) return null
  const up = source.toUpperCase()
  const isTopo = source === 'lldp' || source === 'cdp'
  return (
    <span className="badge" style={{ background: isTopo ? 'rgba(56,189,248,.15)' : 'var(--surface-3)', color: isTopo ? '#38bdf8' : 'var(--text-muted)', fontSize: 11 }}>
      {up}
    </span>
  )
}

export function PathFinder() {
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') ?? '')
  const [view, setView] = useState<'visual' | 'details'>('visual')
  const search = useMutation({
    mutationFn: async (query: string) => {
      const r = await api.get<SearchResult | SearchResult[]>(`/search?q=${encodeURIComponent(query)}`)
      return Array.isArray(r) ? r : [r]
    },
  })
  const results = search.data ?? []
  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const v = q.trim()
    if (v) { setParams({ q: v }); search.mutate(v) }
  }
  // Deep link: /path-finder?q=<ip|mac|host> auto-runs the trace on load.
  useEffect(() => {
    const p = params.get('q')
    if (p) search.mutate(p)
    // run once on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div>
      <PageHeader title="Path Finder" icon={RouteIcon} subtitle="Trace any endpoint's Layer-2 path — IP, MAC, hostname or device name" />

      <Panel>
        <form onSubmit={submit} className="row" style={{ gap: 10, alignItems: 'center' }}>
          <Search size={18} className="muted" />
          <input
            className="field" style={{ flex: 1, maxWidth: 460 }} value={q} autoFocus
            onChange={(e) => setQ(e.target.value)}
            placeholder="172.21.96.44   ·   aa:bb:cc:dd:ee:ff   ·   AP-LOBBY-03"
          />
          <button className="btn btn-primary" disabled={!q.trim() || search.isPending}>
            <Search size={15} /> {search.isPending ? 'Tracing…' : 'Trace path'}
          </button>
        </form>
        {search.isError && <div className="enc-banner crit" style={{ marginTop: 12 }}>{(search.error as Error).message}</div>}
      </Panel>

      {search.isSuccess && results.length === 0 && (
        <Panel><EmptyState icon={CircleHelp} title="No match" message="Nothing resolved for that query. Try an IP, MAC, hostname or device name." /></Panel>
      )}

      {results.length > 0 && (
        <div className="row" style={{ justifyContent: 'flex-end', marginBottom: 8 }}>
          <div className="seg" role="tablist" aria-label="View mode">
            <button className={view === 'visual' ? 'active' : ''} onClick={() => setView('visual')}>
              <Share2 size={13} style={{ verticalAlign: '-2px', marginRight: 4 }} /> Visual
            </button>
            <button className={view === 'details' ? 'active' : ''} onClick={() => setView('details')}>
              <List size={13} style={{ verticalAlign: '-2px', marginRight: 4 }} /> Details
            </button>
          </div>
        </div>
      )}

      {results.map((res, i) =>
        view === 'visual' ? <PathGraphCard key={i} res={res} /> : <ResultCard key={i} res={res} />
      )}
    </div>
  )
}

// Visual mode: the traced path drawn as a graph (client/AP/controller → switch →
// uplinks → core), plus a wireless summary when the endpoint is a Wi-Fi client and
// a role legend of the hops actually shown.
function PathGraphCard({ res }: { res: SearchResult }) {
  const conf = CONF[res.confidence] ?? CONF.none
  const w = res.wireless
  const rolesShown = Array.from(new Set(res.path.map((p) => p.role)))
  return (
    <Panel
      title={res.device_name || res.mac || res.query}
      subtitle={res.query_type.toUpperCase()}
      actions={<span className={`badge ${conf.cls}`}><ShieldCheck size={13} /> {conf.label}</span>}
    >
      {w && (
        <div className="enc-banner info" style={{ marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <Wifi size={15} />
          <span>
            Wi-Fi client on <strong>{w.ap_name || 'AP'}</strong>
            {w.ssid && <> · SSID <strong>{w.ssid}</strong></>}
            {w.band && <> · {w.band}</>}
            {w.controller_name && <> · controller {w.controller_name}</>}
            {' '}— the path starts at the access point.
          </span>
        </div>
      )}
      {res.path.length === 0 ? (
        <EmptyState icon={CircleHelp} title="No path to draw" message="The endpoint was not found in any MAC/FDB table, so there is no Layer-2 path to visualize." />
      ) : (
        <>
          <PathGraph res={res} />
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, fontSize: 11, marginTop: 10 }}>
            {rolesShown.map((r) => (
              <span key={r} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                <i style={{ width: 9, height: 9, borderRadius: 9, background: ROLE_COLOR[r] ?? '#64748b', display: 'inline-block' }} /> {roleLabel(r)}
              </span>
            ))}
            <span className="muted">· click a node to open its device · drag to pan · scroll to zoom</span>
          </div>
        </>
      )}
      {res.confidence_reasons?.length > 0 && (
        <div className="card" style={{ margin: '12px 0 0' }}>
          <h3 style={{ fontSize: 13, marginBottom: 10 }}>Why this confidence</h3>
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {res.confidence_reasons.map((r, i) => <li key={i} className="muted" style={{ fontSize: 12, marginBottom: 4 }}>{r}</li>)}
          </ul>
        </div>
      )}
    </Panel>
  )
}

function ResultCard({ res }: { res: SearchResult }) {
  const conf = CONF[res.confidence] ?? CONF.none
  const acc = res.switch_port[0]
  return (
    <Panel
      title={res.device_name || res.mac || res.query}
      subtitle={res.query_type.toUpperCase()}
      actions={<span className={`badge ${conf.cls}`}><ShieldCheck size={13} /> {conf.label}</span>}
    >
      <div className="grid-2" style={{ alignItems: 'start', gap: 24 }}>
        {/* The path chain */}
        <div>
          <h3 style={{ fontSize: 13, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '.05em', marginBottom: 14 }}>Connection Path</h3>
          {res.path.length === 0 && <div className="muted">No switch-port match — the endpoint was not found in any MAC/FDB table.</div>}
          {res.path.map((step, idx) => {
            const meta = ROLE_META[step.role] ?? { icon: Server, label: step.role, color: 'var(--text-muted)' }
            const Icon = meta.icon
            return (
              <div key={idx}>
                {idx > 0 && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 0 4px 18px', color: 'var(--text-muted)' }}>
                    <ArrowDown size={16} />
                    <SourceChip source={step.source} />
                    {step.if_name && <span style={{ fontSize: 12 }} className="muted">via {step.if_name}</span>}
                  </div>
                )}
                <div className="card" style={{ margin: 0, padding: '10px 14px', borderLeft: `3px solid ${meta.color}`, display: 'flex', alignItems: 'center', gap: 12 }}>
                  <span style={{ color: meta.color }}><Icon size={20} /></span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: 700 }}>{step.device_name || step.ip || (step.role === 'endpoint' ? (res.mac || res.query) : '—')}</div>
                    <div className="muted" style={{ fontSize: 12 }}>
                      {meta.label}
                      {step.ip && <> · {step.ip}</>}
                      {step.if_name && step.role !== 'endpoint' && <> · port {step.if_name}</>}
                      {step.vlan_id != null && <> · VLAN {step.vlan_id}</>}
                      {step.port_role && <> · {step.port_role}</>}
                    </div>
                  </div>
                </div>
              </div>
            )
          })}
        </div>

        {/* Attribution + evidence */}
        <div className="stack" style={{ gap: 14 }}>
          {acc && (
            <div className="card" style={{ margin: 0 }}>
              <h3 style={{ fontSize: 13, marginBottom: 10 }}>Connected Switch</h3>
              <Row k="Switch" v={acc.switch_name} />
              <Row k="Switch IP" v={acc.switch_ip || '—'} mono />
              <Row k="Port" v={acc.if_name || (acc.if_index != null ? `ifIndex ${acc.if_index}` : '—')} mono />
              <Row k="VLAN" v={String(acc.vlan_id)} />
              <Row k="Port role" v={acc.port_role || 'unknown'} />
              <Row k="MAC-table source" v={<SourceChip source={acc.source} />} />
              <Row k="MAC last seen" v={acc.last_seen_at ? <span><Clock size={12} /> {timeAgo(acc.last_seen_at)}</span> : '—'} />
            </div>
          )}
          {res.mac && (
            <div className="card" style={{ margin: 0 }}>
              <h3 style={{ fontSize: 13, marginBottom: 10 }}>Resolution</h3>
              <Row k="MAC address" v={res.mac} mono />
              {res.arp_device_name && <Row k="ARP source device" v={res.arp_device_name} />}
              {res.arp_source && <Row k="ARP source" v={<SourceChip source={res.arp_source} />} />}
              {res.arp_last_seen && <Row k="ARP last seen" v={<span><Clock size={12} /> {timeAgo(res.arp_last_seen)}</span>} />}
            </div>
          )}
          {res.switch_port.length > 1 && (
            <div className="card" style={{ margin: 0 }}>
              <h3 style={{ fontSize: 13, marginBottom: 10 }}>Also seen on ({res.switch_port.length - 1})</h3>
              {res.switch_port.slice(1).map((sp, i) => (
                <div key={i} className="muted" style={{ fontSize: 13, padding: '3px 0' }}>
                  {sp.switch_name} · {sp.if_name || `ifIndex ${sp.if_index}`} · VLAN {sp.vlan_id} {sp.port_role ? `· ${sp.port_role}` : ''}
                </div>
              ))}
            </div>
          )}
          {res.confidence_reasons?.length > 0 && (
            <div className="card" style={{ margin: 0 }}>
              <h3 style={{ fontSize: 13, marginBottom: 10 }}>Why this confidence</h3>
              <ul style={{ margin: 0, paddingLeft: 18 }}>
                {res.confidence_reasons.map((r, i) => <li key={i} className="muted" style={{ fontSize: 12, marginBottom: 4 }}>{r}</li>)}
              </ul>
            </div>
          )}
        </div>
      </div>
    </Panel>
  )
}

function Row({ k, v, mono }: { k: string; v: React.ReactNode; mono?: boolean }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12, padding: '5px 0', borderBottom: '1px dashed var(--surface-3)' }}>
      <span className="muted" style={{ fontSize: 13 }}>{k}</span>
      <span style={{ fontSize: 13, fontFamily: mono ? 'var(--font-mono, monospace)' : undefined, textAlign: 'right' }}>{v}</span>
    </div>
  )
}
