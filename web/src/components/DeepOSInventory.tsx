import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, Cpu } from 'lucide-react'
import { api, type OSInventoryBundle, type Classification, type AuthMe } from '../api'
import { usePaged, Pager } from './ui'

function fmtBytes(n?: number | null): string {
  if (n == null || n === 0) return 'Not collected'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const val = (v?: string | number | null) => (v == null || v === '' ? <span className="muted">Not collected</span> : <>{v}</>)
function fmtUptime(s?: number | null): string {
  if (!s) return 'Not collected'
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600)
  return `${d}d ${h}h`
}

// The composable data sections the deep OS inventory exposes. A detail page can render just
// one section inside its own tab/Panel (bare, no card chrome); passing no section renders the
// legacy self-contained card (used by the generic + switch detail pages).
export type OSSection = 'summary' | 'disks' | 'network' | 'services' | 'processes' | 'software'

// useOSInventory is the shared query + on-demand collect for a device's deep OS inventory.
// Every consumer uses the same query key so react-query fetches once per device.
export function useOSInventory(deviceId: string) {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['os-inventory', deviceId], queryFn: () => api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`) })
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me') })
  const collect = useMutation({
    mutationFn: () => api.post(`/devices/${deviceId}/collect-os`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['os-inventory', deviceId] }),
  })
  const canCollect = !!(me.data?.admin || me.data?.permissions?.includes('devices.write'))
  return { bundle: q.data ?? null, isLoading: q.isLoading, collect, canCollect }
}

// CollectResult mirrors the /collect-os response (status: collected | queued | failed).
interface CollectResult { status: string; method?: string; detail?: string; agent_name?: string; counts?: Record<string, number> }

type CollectPhase = 'idle' | 'collecting' | 'waiting' | 'done' | 'error'

// CollectOSButton runs an on-demand deep OS collection (needs a bound credential + devices.write)
// and shows live PROGRESS: a spinner + elapsed timer while the host/agent is queried, then an
// honest outcome. A direct WinRM/SSH collect is synchronous (the request blocks ~30-60s); a
// relay-agent collect returns "queued" and we then poll the inventory's collected_at until the
// agent reports back, so the operator sees "waiting for agent" rather than a dead button.
// Hidden for manually-modeled virtual devices, which are never probed.
export function CollectOSButton({ deviceId, isVirtual, small }: { deviceId: string; isVirtual?: boolean; small?: boolean }) {
  const qc = useQueryClient()
  const { bundle, canCollect } = useOSInventory(deviceId)
  const [phase, setPhase] = useState<CollectPhase>('idle')
  const [elapsed, setElapsed] = useState(0)
  const [msg, setMsg] = useState<string | null>(null)

  // Tick an elapsed-seconds counter while a collection is in flight or we're polling an agent.
  useEffect(() => {
    if (phase !== 'collecting' && phase !== 'waiting') return
    const t = setInterval(() => setElapsed((e) => e + 1), 1000)
    return () => clearInterval(t)
  }, [phase])

  if (!canCollect || isVirtual) return null

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['os-inventory', deviceId] })
    qc.invalidateQueries({ queryKey: ['devices', 'all'] })
    qc.invalidateQueries({ queryKey: ['storage', deviceId] })
    qc.invalidateQueries({ queryKey: ['classification', deviceId] })
  }

  // Poll for a NEW collected_at (agent completed asynchronously) up to ~2 minutes.
  async function pollForAgent(baseline: string) {
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 3000))
      try {
        const b = await api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`)
        if (b.inventory && b.inventory.collected_at !== baseline) {
          setPhase('done'); setMsg('Updated from agent'); refresh(); return
        }
      } catch { /* keep waiting */ }
    }
    setPhase('idle'); setMsg('Dispatched — inventory will appear when the agent reports back'); refresh()
  }

  async function run() {
    setPhase('collecting'); setElapsed(0); setMsg(null)
    const baseline = bundle?.inventory?.collected_at ?? ''
    try {
      const res = await api.post<CollectResult>(`/devices/${deviceId}/collect-os`, {})
      if (res.status === 'collected') {
        const n = res.counts ? Object.values(res.counts).reduce((a, b) => a + b, 0) : 0
        setPhase('done'); setMsg(`Updated${n ? ` · ${n} items` : ''}`); refresh()
      } else if (res.status === 'queued') {
        setPhase('waiting'); setMsg(res.agent_name ? `Dispatched to ${res.agent_name}` : 'Dispatched to relay agent')
        void pollForAgent(baseline)
      } else {
        setPhase('error'); setMsg(res.detail || 'Collection failed')
      }
    } catch (e) {
      setPhase('error'); setMsg((e as Error).message)
    }
  }

  const busy = phase === 'collecting' || phase === 'waiting'
  const label = phase === 'collecting' ? `Collecting… ${elapsed}s`
    : phase === 'waiting' ? `Waiting for agent… ${elapsed}s`
      : bundle?.inventory ? 'Re-collect OS' : 'Collect OS'

  return (
    <span style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, alignItems: 'flex-end' }}>
      <button className={small ? 'btn btn-sm' : 'btn'} disabled={busy} onClick={run} title={busy ? 'A collection is in progress' : 'Gather OS, hardware, disks (incl. media type), network and software'}>
        <RefreshCw size={13} className={busy ? 'spin' : undefined} /> {label}
      </button>
      {msg && phase === 'error' && <span className="error-msg" style={{ fontSize: 11, maxWidth: 280, whiteSpace: 'normal', textAlign: 'right' }}>{msg}</span>}
      {msg && phase !== 'error' && <span className="muted" style={{ fontSize: 11, maxWidth: 280, whiteSpace: 'normal', textAlign: 'right' }}>{phase === 'done' ? '✓ ' : ''}{msg}</span>}
      {busy && <span className="muted" style={{ fontSize: 10 }}>this can take 30–90s</span>}
    </span>
  )
}

// ---- bare section renderers (embeddable in a detail-page Panel) -------------

function SummaryBlock({ b }: { b: OSInventoryBundle }) {
  const inv = b.inventory!
  return (
    <>
      <dl className="kv">
        <div><dt>OS</dt><dd>{val(inv.os_caption)}</dd></div>
        <div><dt>Version / build</dt><dd>{val(inv.os_version)}{inv.os_build ? ` (${inv.os_build})` : ''}</dd></div>
        <div><dt>Edition / arch</dt><dd>{val(inv.os_edition || inv.os_arch)}</dd></div>
        {inv.kernel && <div><dt>Kernel</dt><dd>{inv.kernel}</dd></div>}
        <div><dt>Hostname</dt><dd>{val(inv.hostname)}</dd></div>
        <div><dt>Domain / FQDN</dt><dd>{val(inv.fqdn || inv.domain || inv.workgroup)}</dd></div>
        <div><dt>Logged-on user</dt><dd>{val(inv.logged_on_user)}</dd></div>
        <div><dt>Uptime</dt><dd>{fmtUptime(inv.uptime_seconds)}</dd></div>
        <div><dt>Timezone</dt><dd>{val(inv.timezone)}</dd></div>
        <div><dt>Manufacturer / model</dt><dd>{val([inv.manufacturer, inv.model].filter(Boolean).join(' ') || null)}</dd></div>
        <div><dt>Serial</dt><dd>{val(inv.serial)}</dd></div>
        <div><dt>BIOS</dt><dd>{val(inv.bios_version)}{inv.bios_date ? ` (${inv.bios_date})` : ''}</dd></div>
        <div><dt>CPU</dt><dd>{val(inv.cpu_model)}{inv.cpu_cores ? ` · ${inv.cpu_cores} cores` : ''}{inv.cpu_sockets ? ` / ${inv.cpu_sockets} sockets` : ''}</dd></div>
        <div><dt>RAM</dt><dd>{fmtBytes(inv.ram_total_bytes)}</dd></div>
      </dl>
      {b.roles.length > 0 && (
        <div style={{ marginTop: 10 }}>
          <span className="muted">Detected roles: </span>
          {b.roles.map((r) => <span key={r.role} className="badge badge-access" style={{ marginRight: 6 }}>{r.role}</span>)}
        </div>
      )}
    </>
  )
}

// DiskTypeBadge renders the physical media type of a disk (SSD / NVMe / HDD) with a distinct
// color, or an honest "—" when the collector could not determine it (never guessed).
export function DiskTypeBadge({ media }: { media?: string | null }) {
  const m = (media || '').trim()
  if (!m) return <span className="muted" title="Media type not collected — re-collect OS to detect (a physical host reports SSD/NVMe/HDD; a VM reports Virtual)">—</span>
  const cls = m === 'SSD' ? 'badge-up' : m === 'NVMe' ? 'badge-access' : m === 'HDD' ? 'badge-unknown' : m === 'Virtual' ? 'badge-info' : 'badge-info'
  return <span className={`badge ${cls}`}>{m}</span>
}

// diskMediaRollup summarizes the media mix ("2 NVMe · 1 SSD · 1 Virtual") for a set of disks;
// empty when none are typed yet.
export function diskMediaRollup(disks: { media_type?: string | null }[]): string {
  const counts: Record<string, number> = {}
  for (const d of disks) { const m = (d.media_type || '').trim(); if (m) counts[m] = (counts[m] || 0) + 1 }
  return ['NVMe', 'SSD', 'HDD', 'Virtual'].filter((k) => counts[k]).map((k) => `${counts[k]} ${k}`).join(' · ')
}

function DisksBlock({ b }: { b: OSInventoryBundle }) {
  if (b.disks.length === 0) return <span className="muted">Not collected yet.</span>
  return (
    <table className="data-table"><thead><tr><th>Name</th><th>Type</th><th>FS</th><th>Total</th><th>Free</th><th>Model</th></tr></thead>
      <tbody>{b.disks.map((d, i) => <tr key={i}><td className="cell-name">{d.name}</td><td><DiskTypeBadge media={d.media_type} /></td><td>{d.filesystem || '—'}</td><td className="mono">{fmtBytes(d.total_bytes)}</td><td className="mono">{fmtBytes(d.free_bytes)}</td><td className="muted" style={{ fontSize: 12 }}>{d.model || '—'}</td></tr>)}</tbody>
    </table>
  )
}

function NicsBlock({ b }: { b: OSInventoryBundle }) {
  if (b.nics.length === 0) return <span className="muted">Not collected yet.</span>
  return (
    <table className="data-table"><thead><tr><th>Interface</th><th>MAC</th><th>IP(s)</th><th>Gateway</th><th>DNS</th><th>DHCP</th></tr></thead>
      <tbody>{b.nics.map((n, i) => <tr key={i}><td className="cell-name">{n.name}</td><td className="mono" style={{ fontSize: 12 }}>{n.mac || '—'}</td><td className="mono" style={{ fontSize: 12 }}>{n.ip_addresses || '—'}</td><td>{n.gateway || '—'}</td><td className="mono" style={{ fontSize: 12 }}>{n.dns_servers || '—'}</td><td>{n.dhcp_enabled ? 'yes' : 'no'}</td></tr>)}</tbody>
    </table>
  )
}

// renderSection returns the bare content for one section, for embedding in a tab.
function renderSection(section: OSSection, b: OSInventoryBundle) {
  const inv = b.inventory
  switch (section) {
    case 'summary': return <SummaryBlock b={b} />
    case 'disks': return <DisksBlock b={b} />
    case 'network': return <NicsBlock b={b} />
    case 'services': return (
      <PagedSection title="Services" items={b.services} bare
        head={<tr><th>Name</th><th>Status</th><th>Start</th><th>Description</th></tr>}
        match={(sv, q) => (sv.display_name || sv.name || '').toLowerCase().includes(q) || (sv.status || '').toLowerCase().includes(q)}
        row={(sv, i) => <tr key={i}><td className="cell-name">{sv.display_name || sv.name}</td><td>{sv.status || '—'}</td><td>{sv.start_type || '—'}</td><td className="muted" style={{ fontSize: 12 }}>{sv.description || ''}</td></tr>} />
    )
    case 'processes': return (
      <PagedSection title="Top processes" items={b.processes} bare
        head={<tr><th>Process</th><th>PID</th><th>Memory</th><th>CPU%</th></tr>}
        match={(p, q) => p.name.toLowerCase().includes(q)}
        emptyNote={inv ? `Not reported via ${inv.collection_method} collection on the last run.` : undefined}
        row={(p) => <tr key={p.pid}><td className="cell-name">{p.name}</td><td className="mono">{p.pid}</td><td className="mono">{fmtBytes(p.mem_bytes)}</td><td className="mono">{p.cpu_pct ?? '—'}</td></tr>} />
    )
    case 'software': return (
      <PagedSection title="Installed software" items={b.software} bare
        head={<tr><th>Name</th><th>Version</th><th>Publisher</th></tr>}
        match={(sw, q) => (sw.name || '').toLowerCase().includes(q) || (sw.publisher || '').toLowerCase().includes(q)}
        emptyNote={inv?.software_note ? `Software not collected — ${inv.software_note}` : inv ? `Not reported via ${inv.collection_method} collection — some hosts don't expose the installed-software registry over WMI/WinRM (e.g. legacy WSMan). Re-collect after enabling remote registry, or collect over direct WinRM.` : undefined}
        row={(sw, i) => <tr key={i}><td className="cell-name">{sw.name}</td><td className="mono">{sw.version || '—'}</td><td className="muted" style={{ fontSize: 12 }}>{sw.publisher || ''}</td></tr>} />
    )
  }
}

// EventHealth renders the 24h Windows event-log rollup as a compact strip (reused by pages).
export function EventHealth({ deviceId }: { deviceId: string }) {
  const { bundle } = useOSInventory(deviceId)
  const inv = bundle?.inventory
  if (!inv || (inv.events_error_24h == null && inv.events_warning_24h == null && inv.events_critical_24h == null)) {
    return <span className="muted" style={{ fontSize: 13 }}>No event-log data collected.</span>
  }
  return (
    <p style={{ margin: 0, fontSize: 13 }}>
      <strong>Event log (24h):</strong>{' '}
      <span className="badge badge-down">{inv.events_critical_24h ?? 0} critical</span>{' '}
      <span className="badge badge-warning">{inv.events_error_24h ?? 0} error</span>{' '}
      <span className="badge badge-unknown">{inv.events_warning_24h ?? 0} warning</span>
      {inv.last_critical_event ? <div className="muted" style={{ marginTop: 4 }}>last critical: {inv.last_critical_event}</div> : null}
    </p>
  )
}

// OSInventorySection renders ONE bare section for embedding in a detail-page tab. It shows an
// honest empty/pending message (never fabricated) when nothing is collected.
export function OSInventorySection({ deviceId, section, isVirtual }: { deviceId: string; section: OSSection; isVirtual?: boolean }) {
  const { bundle, isLoading } = useOSInventory(deviceId)
  if (isLoading) return <div className="loading">Loading…</div>
  if (!bundle || !bundle.inventory) {
    return (
      <p className="muted" style={{ margin: '6px 0' }}>
        {isVirtual
          ? 'Manually modeled virtual device — OS, hardware, disks, network and software are maintained manually. Use Edit virtual device in the header to update them.'
          : 'Not collected yet. Bind a working credential (WinRM for Windows, SSH for Linux) and Collect OS to gather this.'}
      </p>
    )
  }
  return <>{renderSection(section, bundle)}</>
}

// DeepOSInventory renders the authenticated deep OS inventory (WinRM/SSH) for a device as a
// single self-contained card (summary + disks/network/services/processes/software/events). Used
// by the generic + switch detail pages. Absent data shows "Not collected" — never fabricated.
export function DeepOSInventory({ deviceId, alwaysShow, isVirtual }: { deviceId: string; alwaysShow?: boolean; isVirtual?: boolean }) {
  const { bundle, isLoading, canCollect } = useOSInventory(deviceId)
  const cls = useQuery({ queryKey: ['classification', deviceId], queryFn: () => api.get<Classification>(`/devices/${deviceId}/classification`) })

  const b = bundle
  const inv = b?.inventory ?? null
  const osFamily = cls.data?.os_family ?? ''
  const isOSHost = osFamily === 'windows' || osFamily === 'linux'

  if (isLoading || cls.isLoading) return null
  if (!alwaysShow && !isOSHost && !inv) return null

  return (
    <div className="card">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2 style={{ margin: 0, display: 'inline-flex', gap: 8, alignItems: 'center' }}><Cpu size={17} /> Deep OS Inventory</h2>
        <span style={{ display: 'inline-flex', gap: 10, alignItems: 'center' }}>
          {inv && <span className="muted" style={{ fontSize: 12 }}>via <strong>{inv.collection_method}</strong> · {new Date(inv.collected_at).toLocaleString()}</span>}
          {canCollect && !isVirtual && <CollectOSButton deviceId={deviceId} isVirtual={isVirtual} small />}
        </span>
      </div>

      {b && !inv && (
        isVirtual ? (
          <p className="muted" style={{ marginTop: 10 }}>
            Manually modeled virtual device — OS, hardware, disks, network and software are maintained manually.
            Use <strong>Edit virtual device</strong> in the header to update them.
          </p>
        ) : (
          <p className="muted" style={{ marginTop: 10 }}>
            Not collected yet. Bind a working credential (WinRM for Windows, SSH for Linux) and click
            <strong> Collect OS</strong> to gather OS, hardware, disks, network, services, processes and software.
          </p>
        )
      )}

      {b && inv && (
        <>
          <div style={{ marginTop: 12 }}><SummaryBlock b={b} /></div>
          {(inv.events_error_24h != null || inv.events_warning_24h != null || inv.events_critical_24h != null) && (
            <div style={{ marginTop: 10 }}><EventHealth deviceId={deviceId} /></div>
          )}
          <Section title={`Disks / Volumes (${b.disks.length})`} empty={b.disks.length === 0}><DisksBlock b={b} /></Section>
          <Section title={`Network (${b.nics.length})`} empty={b.nics.length === 0}><NicsBlock b={b} /></Section>
          <div style={{ marginTop: 12 }}>{renderSection('services', b)}</div>
          <div style={{ marginTop: 12 }}>{renderSection('processes', b)}</div>
          <div style={{ marginTop: 12 }}>{renderSection('software', b)}</div>
        </>
      )}
    </div>
  )
}

function Section({ title, empty, children }: { title: string; empty: boolean; children: React.ReactNode }) {
  return (
    <details style={{ marginTop: 12 }} open={!empty}>
      <summary style={{ cursor: 'pointer', fontWeight: 600 }}>{title}</summary>
      <div style={{ marginTop: 8 }}>{empty ? <span className="muted">Not collected yet.</span> : children}</div>
    </details>
  )
}

// PagedSection renders a large collection (services/processes/software) with a filter box +
// client-side pagination so the DOM stays small. `bare` drops the <details> wrapper so the
// content can live directly inside a tab Panel; otherwise it is a collapsible section.
function PagedSection<T>({ title, items, head, row, match, pageSize = 12, emptyNote, bare }: {
  title: string
  items: T[]
  head: React.ReactNode
  row: (it: T, i: number) => React.ReactNode
  match?: (it: T, q: string) => boolean
  pageSize?: number
  emptyNote?: string
  bare?: boolean
}) {
  const [filter, setFilter] = useState('')
  const { slice, total, page, pages, setPage } = usePaged(items, { pageSize, filter, match })
  const body = (
    <div style={{ marginTop: bare ? 0 : 8 }}>
      {items.length === 0 ? <span className="muted">{emptyNote || 'Not collected yet.'}</span> : (
        <>
          {match && (
            <input
              placeholder={`Filter ${title.toLowerCase()}…`}
              value={filter}
              onChange={(e) => { setFilter(e.target.value); setPage(0) }}
              style={{ marginBottom: 8, padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, width: 280, maxWidth: '100%', background: 'var(--surface)', color: 'inherit' }}
            />
          )}
          <table className="data-table"><thead>{head}</thead><tbody>{slice.map(row)}</tbody></table>
          <Pager page={page} pages={pages} total={total} pageSize={pageSize} onPage={setPage} />
        </>
      )}
    </div>
  )
  if (bare) return body
  return (
    <details style={{ marginTop: 12 }} open={items.length > 0}>
      <summary style={{ cursor: 'pointer', fontWeight: 600 }}>{title} ({items.length})</summary>
      {body}
    </details>
  )
}
