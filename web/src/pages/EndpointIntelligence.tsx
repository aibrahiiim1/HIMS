import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ChartLine, Cpu, HardDrive, Boxes, Activity, Server, MonitorSmartphone,
  Network, Wrench, ShieldCheck, AppWindow, Gauge,
} from 'lucide-react'
import { api } from '../api'
import {
  PageHeader, Panel, Kpi, TabBar, Donut, Legend, BarList, HealthRing,
  colorFor, timeAgo, type Tone,
} from '../components/ui'
import { DataTable, type DataCol } from '../components/DataTable'

// Endpoint Intelligence — a technical analytics + reporting center over the
// OS-inventoried fleet. Every figure comes from the Phase 1 backend
// (/endpoint-intelligence/*), which aggregates only real persisted inventory;
// missing values render as "—"/"not collected", never fabricated. Each tab
// lazily fetches its own endpoint so the page stays responsive.

type Row = Record<string, unknown>
const S = (v: unknown) => (v == null ? '' : String(v))
const N = (v: unknown) => (typeof v === 'number' ? v : Number(v ?? 0))
const GIB = 1073741824
const gb = (v: unknown) => { const n = N(v); return v == null ? '—' : (n / GIB).toFixed(1) + ' GB' }
const num = (v: unknown) => (v == null ? '—' : N(v).toLocaleString())

// Normalise a distribution array to {label,value}; the backend uses varying key
// names (label/bucket/site/service/serial/mac), so accept any string field.
function dist(rows: Row[] | undefined, labelKey = 'label', valKey = 'count'): { label: string; value: number }[] {
  return (rows ?? []).map((r) => ({ label: S(r[labelKey] ?? r.label ?? r.bucket ?? r.site ?? ''), value: N(r[valKey]) }))
}
function donutData(rows: Row[] | undefined, labelKey = 'label', valKey = 'count') {
  return dist(rows, labelKey, valKey).map((d) => ({ ...d, color: colorFor(d.label) }))
}

/** Generic fetch hook (React Query) keyed by path+query; refetches on change. */
function useEI<T = Row>(path: string | null, query: string): { data: T | null; loading: boolean; err: string } {
  const q = useQuery({
    queryKey: ['ei', path, query],
    queryFn: () => api.get<T>(`${path}${query}`),
    enabled: !!path,
    retry: 0,
  })
  return { data: q.data ?? null, loading: q.isLoading, err: q.error ? String((q.error as Error).message) : '' }
}

const TABS = [
  { key: 'overview', label: 'Overview', icon: Gauge },
  { key: 'hardware', label: 'Hardware', icon: Cpu },
  { key: 'disks', label: 'Disk', icon: HardDrive },
  { key: 'software', label: 'Software', icon: AppWindow },
  { key: 'processes', label: 'Processes', icon: Activity },
  { key: 'services', label: 'Services', icon: Wrench },
  { key: 'os', label: 'OS', icon: Server },
  { key: 'network', label: 'Network', icon: Network },
  { key: 'collection-health', label: 'Collection Health', icon: ShieldCheck },
]

export function EndpointIntelligence() {
  const [tab, setTab] = useState('overview')
  // Operator-tunable thresholds (shared across tabs that use them).
  const [lowRam, setLowRam] = useState(8)
  const [diskGb, setDiskGb] = useState(10)
  const [diskPct, setDiskPct] = useState(15)
  const [staleDays, setStaleDays] = useState(7)

  const query = useMemo(() => {
    const p = new URLSearchParams()
    p.set('low_ram_gb', String(lowRam))
    p.set('disk_free_gb', String(diskGb))
    p.set('disk_free_pct', String(diskPct))
    p.set('stale_days', String(staleDays))
    return `?${p.toString()}`
  }, [lowRam, diskGb, diskPct, staleDays])

  return (
    <div className="page">
      <PageHeader
        title="Endpoint Intelligence"
        subtitle="Technical analytics & reporting across all OS-inventoried endpoints, servers and peripherals — grounded only in real collected inventory."
        icon={ChartLine}
      />

      <Panel className="ei-thresholds">
        <div className="ei-threshold-row">
          <ThresholdInput label="Low RAM <" unit="GB" value={lowRam} onChange={setLowRam} />
          <ThresholdInput label="Low disk free <" unit="GB" value={diskGb} onChange={setDiskGb} />
          <ThresholdInput label="Low disk free <" unit="%" value={diskPct} onChange={setDiskPct} />
          <ThresholdInput label="Stale after" unit="days" value={staleDays} onChange={setStaleDays} />
          <span className="muted" style={{ fontSize: 12, alignSelf: 'center' }}>
            Thresholds apply to the cards, disk risk, and health score.
          </span>
        </div>
      </Panel>

      <div style={{ margin: '12px 0' }}>
        <TabBar tabs={TABS} active={tab} onChange={setTab} />
      </div>

      {tab === 'overview' && <OverviewTab query={query} onJump={setTab} />}
      {tab === 'hardware' && <HardwareTab query={query} />}
      {tab === 'disks' && <DiskTab query={query} />}
      {tab === 'software' && <SoftwareTab query={query} />}
      {tab === 'processes' && <ProcessesTab query={query} />}
      {tab === 'services' && <ServicesTab query={query} />}
      {tab === 'os' && <OSTab query={query} />}
      {tab === 'network' && <NetworkTab query={query} />}
      {tab === 'collection-health' && <CollectionHealthTab query={query} />}
    </div>
  )
}

function ThresholdInput({ label, unit, value, onChange }: { label: string; unit: string; value: number; onChange: (n: number) => void }) {
  return (
    <label className="ei-threshold">
      <span className="muted" style={{ fontSize: 12 }}>{label}</span>
      <input type="number" min={0} value={value} onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
        style={{ width: 64 }} />
      <span className="muted" style={{ fontSize: 12 }}>{unit}</span>
    </label>
  )
}

/* ---- shared render helpers ----------------------------------------------- */

function Loading() { return <div className="muted" style={{ padding: 16 }}>Loading…</div> }
function Err({ msg }: { msg: string }) { return <div className="badge badge-down" style={{ margin: 12 }}>Failed to load: {msg}</div> }

function ChartPanel({ title, rows, kind = 'bar', icon }: { title: string; rows: Row[] | undefined; kind?: 'bar' | 'donut'; icon?: typeof Cpu }) {
  const d = donutData(rows)
  return (
    <Panel title={title} icon={icon}>
      {d.length === 0 ? <div className="muted">No data.</div> : kind === 'donut' ? (
        <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
          <Donut data={d} />
          <div style={{ flex: 1, minWidth: 160 }}><Legend data={d} /></div>
        </div>
      ) : (
        <BarList rows={d.map((x) => ({ label: x.label, value: x.value }))} />
      )}
    </Panel>
  )
}

const dev = {
  name: { key: 'name', label: 'Device', render: (r: Row) => S(r.name) || S(r.hostname) || '—', sortVal: (r: Row) => S(r.name) },
  ip: { key: 'ip', label: 'IP', render: (r: Row) => S(r.ip) || '—', mono: true },
  site: { key: 'site_name', label: 'Site', render: (r: Row) => S(r.site_name) || '—' },
} as const

function deviceSearch(r: Row) { return [r.name, r.hostname, r.ip, r.site_name, r.os_caption, r.eff_model].map(S).join(' ') }

/* ---- 1. Overview --------------------------------------------------------- */

function OverviewTab({ query, onJump }: { query: string; onJump: (t: string) => void }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/overview', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const c = (data?.cards ?? {}) as Row
  const worst = (data?.worst_devices ?? []) as Row[]
  const best = (data?.best_devices ?? []) as Row[]
  const scoreDist = donutData((data?.health_score_distribution ?? []) as Row[], 'bucket')

  const cards: { label: string; key: string; tone?: Tone; jump?: string }[] = [
    { label: 'Total endpoints', key: 'total' },
    { label: 'Managed', key: 'managed', tone: 'ok' },
    { label: 'Unmanaged', key: 'unmanaged', tone: 'warn', jump: 'collection-health' },
    { label: 'Servers', key: 'servers' },
    { label: 'Workstations', key: 'workstations' },
    { label: 'Laptops', key: 'laptops' },
    { label: 'Low RAM', key: 'low_ram', tone: 'warn', jump: 'hardware' },
    { label: 'Low free disk', key: 'low_free_disk', tone: 'crit', jump: 'disks' },
    { label: 'Missing software', key: 'missing_software', tone: 'warn', jump: 'software' },
    { label: 'Missing processes', key: 'missing_processes', tone: 'warn', jump: 'processes' },
    { label: 'Stale collection', key: 'stale_collected', tone: 'warn', jump: 'collection-health' },
    { label: 'Old OS', key: 'old_os', tone: 'crit', jump: 'os' },
    { label: 'Many stopped services', key: 'many_stopped_services', tone: 'warn', jump: 'services' },
    { label: 'Duplicate hostnames', key: 'duplicate_hostnames', tone: 'warn' },
    { label: 'Missing serial/model', key: 'missing_serial_model', tone: 'warn', jump: 'hardware' },
    { label: 'Collection warnings', key: 'collection_warnings', tone: 'warn', jump: 'collection-health' },
  ]

  const scoreCol: DataCol<Row> = {
    key: 'health_score', label: 'Score', sortVal: (r) => N(r.health_score),
    render: (r) => <span className="badge" style={{ background: scoreColor(N(r.health_score)) }}>{num(r.health_score)}</span>,
  }
  const worstCols: DataCol<Row>[] = [
    dev.name, dev.ip, dev.site, scoreCol,
    { key: 'os_caption', label: 'OS', render: (r) => S(r.os_caption) || '—' },
    { key: 'ram', label: 'RAM', sortVal: (r) => N(r.ram_total_bytes), render: (r) => gb(r.ram_total_bytes) },
    { key: 'disk', label: 'Min free', sortVal: (r) => N(r.min_free), render: (r) => gb(r.min_free) },
    { key: 'method', label: 'Method', render: (r) => S(r.collection_method) || 'not collected' },
  ]

  return (
    <>
      <div className="kpi-grid">
        {cards.map((cd) => (
          <Kpi key={cd.key} label={cd.label} value={num(c[cd.key])} tone={cd.tone}
            onClick={cd.jump ? () => onJump(cd.jump!) : undefined} />
        ))}
      </div>

      <div className="grid-2" style={{ marginTop: 12 }}>
        <Panel title="Endpoint health score distribution" icon={Gauge}>
          {scoreDist.length === 0 ? <div className="muted">No data.</div> : (
            <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
              <Donut data={scoreDist} centerLabel="endpoints" />
              <div style={{ flex: 1, minWidth: 160 }}><Legend data={scoreDist} /></div>
            </div>
          )}
        </Panel>
        <Panel title="Fleet health" icon={ShieldCheck}>
          <div style={{ display: 'flex', justifyContent: 'center' }}>
            <HealthRing score={avgScore(worst, best)} label="avg sample" />
          </div>
          <p className="muted" style={{ fontSize: 12, textAlign: 'center', marginTop: 8 }}>
            Average of the best & worst sampled devices. Full per-device scores in the tables below.
          </p>
        </Panel>
      </div>

      <Panel title="Worst devices / upgrade candidates" icon={MonitorSmartphone} className="mt12">
        <DataTable rows={worst} cols={worstCols} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          emptyTitle="No devices" emptyMessage="No endpoint inventory yet." pageSizeDefault={10} />
      </Panel>
      <Panel title="Healthiest devices" icon={ShieldCheck} className="mt12">
        <DataTable rows={best} cols={worstCols} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          emptyTitle="No devices" emptyMessage="No endpoint inventory yet." pageSizeDefault={10} />
      </Panel>
    </>
  )
}

function scoreColor(s: number) { return s >= 80 ? 'var(--ok)' : s >= 60 ? 'var(--brand)' : s >= 40 ? 'var(--warn)' : 'var(--crit)' }
function avgScore(a: Row[], b: Row[]) {
  const all = [...a, ...b].map((r) => N(r.health_score)).filter((n) => !Number.isNaN(n))
  return all.length ? Math.round(all.reduce((x, y) => x + y, 0) / all.length) : 0
}

/* ---- 2. Hardware --------------------------------------------------------- */

function HardwareTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/hardware', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  const ramCols: DataCol<Row>[] = [dev.name, dev.ip, dev.site,
    { key: 'ram', label: 'RAM', sortVal: (r) => N(r.ram_total_bytes), render: (r) => gb(r.ram_total_bytes) },
    { key: 'cpu', label: 'CPU', render: (r) => S(r.cpu_model) || '—' },
    { key: 'model', label: 'Model', render: (r) => S(r.eff_model) || '—' }]
  return (
    <>
      <div className="grid-2">
        <ChartPanel title="RAM distribution" rows={d.ram_distribution as Row[]} kind="bar" icon={Cpu} />
        <ChartPanel title="CPU vendor" rows={d.cpu_vendors as Row[]} kind="donut" />
        <ChartPanel title="OS architecture" rows={d.os_arch as Row[]} kind="donut" />
        <ChartPanel title="Physical vs virtual" rows={d.virtual as Row[]} kind="donut" />
        <ChartPanel title="CPU cores" rows={d.cpu_cores as Row[]} kind="bar" />
        <ChartPanel title="Top CPU models" rows={d.cpu_models as Row[]} kind="bar" />
        <ChartPanel title="Device models" rows={d.models as Row[]} kind="bar" />
        <ChartPanel title="Vendors / manufacturers" rows={d.vendors as Row[]} kind="bar" />
      </div>
      <Panel title="Lowest RAM devices" icon={Cpu} className="mt12">
        <DataTable rows={(d.lowest_ram ?? []) as Row[]} cols={ramCols} getKey={(r) => S(r.device_id)} searchText={deviceSearch} pageSizeDefault={10} />
      </Panel>
      <Panel title="Highest RAM devices" icon={Cpu} className="mt12">
        <DataTable rows={(d.highest_ram ?? []) as Row[]} cols={ramCols} getKey={(r) => S(r.device_id)} searchText={deviceSearch} pageSizeDefault={10} />
      </Panel>
      <Panel title="Missing serial" icon={Boxes} className="mt12">
        <DataTable rows={(d.missing_serial ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'model', label: 'Model', render: (r) => S(r.eff_model) || '—' }, { key: 'vendor', label: 'Vendor', render: (r) => S(r.eff_vendor) || '—' }]} pageSizeDefault={10} />
      </Panel>
      <Panel title="Duplicate serials" icon={Boxes} className="mt12">
        <DataTable rows={(d.duplicate_serials ?? []) as Row[]} getKey={(r) => S(r.serial)} searchText={(r) => S(r.serial)}
          cols={[{ key: 'serial', label: 'Serial', mono: true, render: (r) => S(r.serial) }, { key: 'count', label: 'Devices', sortVal: (r) => N(r.count), render: (r) => num(r.count) }, { key: 'list', label: 'Hostnames', render: (r) => (Array.isArray(r.devices) ? (r.devices as string[]).join(', ') : '—') }]}
          emptyTitle="No duplicates" emptyMessage="No two devices share a serial number." pageSizeDefault={10} />
      </Panel>
    </>
  )
}

/* ---- 3. Disk ------------------------------------------------------------- */

function DiskTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/disks', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  const diskCols: DataCol<Row>[] = [dev.name, dev.ip, dev.site,
    { key: 'c_free', label: 'C: free', sortVal: (r) => N(r.c_free), render: (r) => gb(r.c_free) },
    { key: 'c_pct', label: 'C: free %', render: (r) => (r.c_free_pct == null ? '—' : `${N(r.c_free_pct)}%`) },
    { key: 'min', label: 'Min free (any drive)', sortVal: (r) => N(r.min_free), render: (r) => gb(r.min_free) }]
  return (
    <>
      <div className="grid-2">
        <ChartPanel title="Disk risk distribution" rows={d.risk_distribution as Row[]} kind="donut" icon={HardDrive} />
        <ChartPanel title="Disks per device" rows={d.disk_count_distribution as Row[]} kind="bar" />
        <ChartPanel title="Filesystem types" rows={d.filesystem_distribution as Row[]} kind="bar" />
      </div>
      <Panel title="Lowest free space" icon={HardDrive} className="mt12">
        <DataTable rows={(d.lowest_free ?? []) as Row[]} cols={diskCols} getKey={(r) => S(r.device_id)} searchText={deviceSearch} pageSizeDefault={15} />
      </Panel>
      <Panel title="Near-full system (C:) drives" icon={HardDrive} className="mt12">
        <DataTable rows={(d.near_full_c ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'c_free', label: 'C: free', sortVal: (r) => N(r.c_free), render: (r) => gb(r.c_free) }, { key: 'c_total', label: 'C: total', render: (r) => gb(r.c_total) }, { key: 'pct', label: 'Free %', render: (r) => (r.c_free_pct == null ? '—' : `${N(r.c_free_pct)}%`) }]}
          emptyTitle="No near-full drives" emptyMessage="No device's C: drive is below the threshold." pageSizeDefault={15} />
      </Panel>
      <Panel title="Average free space by site" icon={HardDrive} className="mt12">
        <DataTable rows={(d.avg_free_by_site ?? []) as Row[]} getKey={(r) => S(r.site)} searchText={(r) => S(r.site)}
          cols={[{ key: 'site', label: 'Site', render: (r) => S(r.site) }, { key: 'devices', label: 'Devices', sortVal: (r) => N(r.devices), render: (r) => num(r.devices) }, { key: 'avg', label: 'Avg min free', sortVal: (r) => N(r.avg_min_free_gb), render: (r) => (r.avg_min_free_gb == null ? '—' : `${N(r.avg_min_free_gb)} GB`) }]} pageSizeDefault={15} />
      </Panel>
    </>
  )
}

/* ---- 4. Software --------------------------------------------------------- */

function SoftwareTab({ query }: { query: string }) {
  const [q, setQ] = useState('')
  const [applied, setApplied] = useState('')
  const fullQ = `${query}&q=${encodeURIComponent(applied)}`
  const { data, loading, err } = useEI('/endpoint-intelligence/software', fullQ)
  if (err) return <Err msg={err} />
  const d = data ?? {}
  const cat = (d.categories ?? {}) as Row
  return (
    <>
      <div className="kpi-grid">
        <Kpi label="Devices with software" value={num(cat.devices_with_software)} tone="ok" />
        <Kpi label="Microsoft software rows" value={num(cat.microsoft_rows)} />
        <Kpi label="Office installed" value={num(cat.office_devices)} sub="devices" />
        <Kpi label="Browsers" value={num(cat.browser_devices)} sub="devices" />
        <Kpi label="Remote tools" value={num(cat.remote_tool_devices)} sub="devices" tone="warn" />
        <Kpi label="Antivirus / EDR" value={num(cat.antivirus_devices)} sub="devices" tone="ok" />
      </div>
      <Panel title="Installed software" icon={AppWindow} className="mt12"
        actions={
          <form onSubmit={(e) => { e.preventDefault(); setApplied(q) }} style={{ display: 'flex', gap: 6 }}>
            <input placeholder="Filter by name / publisher…" value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 220 }} />
            <button className="btn" type="submit">Search</button>
            {applied && <button className="btn ghost" type="button" onClick={() => { setQ(''); setApplied('') }}>Clear</button>}
          </form>
        }>
        {loading && !data ? <Loading /> : (
          <DataTable rows={(d.top_installed ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
            cols={[{ key: 'name', label: 'Software', render: (r) => S(r.name) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }, { key: 'inst', label: 'Instances', sortVal: (r) => N(r.instances), render: (r) => num(r.instances) }, { key: 'vc', label: 'Versions', sortVal: (r) => N(r.version_count), render: (r) => num(r.version_count) }]}
            emptyTitle="No software" emptyMessage="No installed software matches." pageSizeDefault={25} />
        )}
      </Panel>
      <div className="grid-2 mt12">
        <ChartPanel title="Top publishers" rows={d.publishers as Row[]} kind="bar" icon={AppWindow} />
        <Panel title="Antivirus / EDR detected" icon={ShieldCheck}>
          <DataTable rows={(d.antivirus ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
            cols={[{ key: 'name', label: 'Product', render: (r) => S(r.name) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }]}
            emptyTitle="None detected" emptyMessage="No AV/EDR product identified in installed software." pageSizeDefault={10} />
        </Panel>
      </div>
      <Panel title="Same software, multiple versions" icon={AppWindow} className="mt12">
        <DataTable rows={(d.multi_version ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
          cols={[{ key: 'name', label: 'Software', render: (r) => S(r.name) }, { key: 'vc', label: 'Versions', sortVal: (r) => N(r.version_count), render: (r) => num(r.version_count) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }, { key: 'list', label: 'Version list', render: (r) => (Array.isArray(r.versions) ? (r.versions as string[]).slice(0, 12).join(', ') : '—') }]} pageSizeDefault={15} />
      </Panel>
      <Panel title="Managed devices with no software inventory" icon={MonitorSmartphone} className="mt12">
        <DataTable rows={(d.no_software_devices ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'method', label: 'Method', render: (r) => S(r.collection_method) }, { key: 'note', label: 'Reason', render: (r) => S(r.software_note) || '—' }]}
          emptyTitle="All managed devices have software" emptyMessage="Every collected device returned an installed-software list." pageSizeDefault={15} />
      </Panel>
    </>
  )
}

/* ---- 5. Processes -------------------------------------------------------- */

function ProcessesTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/processes', query)
  const [pname, setPname] = useState('')
  const [lookup, setLookup] = useState<{ name: string; present: boolean } | null>(null)
  const lookupQ = lookup ? `${query}&name=${encodeURIComponent(lookup.name)}&present=${lookup.present}` : ''
  const lk = useEI<Row>(lookup ? '/endpoint-intelligence/processes/devices' : null, lookupQ)
  if (err) return <Err msg={err} />
  const d = data ?? {}
  return (
    <>
      {loading && !data ? <Loading /> : (
        <>
          <Panel title="Most common processes (by device count)" icon={Activity}>
            <DataTable rows={(d.top_common ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
              cols={[{ key: 'name', label: 'Process', mono: true, render: (r) => S(r.name) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }, { key: 'inst', label: 'Instances', sortVal: (r) => N(r.instances), render: (r) => num(r.instances) }, { key: 'avg', label: 'Avg mem', sortVal: (r) => N(r.avg_mem_bytes), render: (r) => gb(r.avg_mem_bytes) }, { key: 'max', label: 'Max mem', sortVal: (r) => N(r.max_mem_bytes), render: (r) => gb(r.max_mem_bytes) }]} pageSizeDefault={15} />
          </Panel>
          <div className="grid-2 mt12">
            <Panel title="Highest memory processes" icon={Activity}>
              <DataTable rows={(d.highest_memory ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
                cols={[{ key: 'name', label: 'Process', mono: true, render: (r) => S(r.name) }, { key: 'max', label: 'Max mem', sortVal: (r) => N(r.max_mem_bytes), render: (r) => gb(r.max_mem_bytes) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }]} pageSizeDefault={10} />
            </Panel>
            <Panel title="Process count per device" icon={MonitorSmartphone}>
              <DataTable rows={(d.process_count_per_device ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
                cols={[dev.name, dev.ip, { key: 'pc', label: 'Processes', sortVal: (r) => N(r.proc_count), render: (r) => num(r.proc_count) }]} pageSizeDefault={10} />
            </Panel>
          </div>
        </>
      )}
      <Panel title="Find devices running a process" icon={Activity} className="mt12">
        <form onSubmit={(e) => { e.preventDefault(); if (pname.trim()) setLookup({ name: pname.trim(), present: true }) }} style={{ display: 'flex', gap: 6, marginBottom: 10 }}>
          <input placeholder="process name, e.g. teamviewer.exe" value={pname} onChange={(e) => setPname(e.target.value)} style={{ width: 260 }} />
          <button className="btn" type="submit" disabled={!pname.trim()}>Running it</button>
          <button className="btn ghost" type="button" disabled={!pname.trim()} onClick={() => pname.trim() && setLookup({ name: pname.trim(), present: false })}>NOT running it</button>
        </form>
        {lookup && (lk.loading ? <Loading /> : lk.err ? <Err msg={lk.err} /> : (
          <DataTable rows={((lk.data?.devices ?? []) as Row[])} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
            cols={[dev.name, dev.ip, dev.site, { key: 'os', label: 'OS', render: (r) => S(r.os_caption) || '—' }]}
            emptyTitle={lookup.present ? 'No devices run this process' : 'All devices run this process'}
            emptyMessage={`Query: "${lookup.name}" ${lookup.present ? 'present' : 'absent'}.`} pageSizeDefault={15} />
        ))}
      </Panel>
    </>
  )
}

/* ---- 6. Services --------------------------------------------------------- */

function ServicesTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/services', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  return (
    <>
      <Panel title="Key service status across managed devices" icon={ShieldCheck}>
        <DataTable rows={(d.key_services ?? []) as Row[]} getKey={(r) => S(r.service)} searchText={(r) => S(r.service)}
          cols={[{ key: 'svc', label: 'Service', render: (r) => <strong>{S(r.display_name) || S(r.service)}</strong> }, { key: 'name', label: 'Name', mono: true, render: (r) => S(r.service) }, { key: 'managed', label: 'Managed', sortVal: (r) => N(r.managed_devices), render: (r) => num(r.managed_devices) }, { key: 'run', label: 'Running', sortVal: (r) => N(r.running), render: (r) => <span className="badge badge-up">{num(r.running)}</span> }, { key: 'stop', label: 'Stopped', sortVal: (r) => N(r.stopped), render: (r) => <span className="badge badge-down">{num(r.stopped)}</span> }]} pageSizeDefault={10} />
      </Panel>
      <div className="grid-2 mt12">
        <ChartPanel title="Service state distribution" rows={d.state_distribution as Row[]} kind="donut" icon={Wrench} />
        <Panel title="Automatic services that are stopped" icon={Wrench}>
          <DataTable rows={(d.auto_stopped ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
            cols={[{ key: 'svc', label: 'Service', render: (r) => S(r.display_name) || S(r.name) }, { key: 'name', label: 'Name', mono: true, render: (r) => S(r.name) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }]}
            emptyTitle="None" emptyMessage="No auto-start services found stopped." pageSizeDefault={10} />
        </Panel>
      </div>
      <Panel title="Common services" icon={Wrench} className="mt12">
        <DataTable rows={(d.common_services ?? []) as Row[]} getKey={(r) => S(r.name)} searchText={(r) => S(r.name)}
          cols={[{ key: 'svc', label: 'Service', render: (r) => S(r.display_name) || S(r.name) }, { key: 'name', label: 'Name', mono: true, render: (r) => S(r.name) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }, { key: 'run', label: 'Running on', sortVal: (r) => N(r.running_count), render: (r) => num(r.running_count) }]} pageSizeDefault={15} />
      </Panel>
    </>
  )
}

/* ---- 7. OS --------------------------------------------------------------- */

function OSTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/os', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  const missing = (d.missing_os ?? {}) as Row
  return (
    <>
      <div className="grid-2">
        <ChartPanel title="OS family distribution" rows={d.family_distribution as Row[]} kind="donut" icon={Server} />
        <ChartPanel title="Collection method" rows={d.collection_method_distribution as Row[]} kind="donut" />
        <ChartPanel title="OS caption (detailed)" rows={d.caption_distribution as Row[]} kind="bar" />
        <ChartPanel title="OS build" rows={d.build_distribution as Row[]} kind="bar" />
        <ChartPanel title="Domain / workgroup" rows={d.domain_workgroup as Row[]} kind="bar" />
      </div>
      <div className="kpi-grid mt12">
        <Kpi label="Missing OS inventory" value={num(missing.missing_os_inventory)} tone="warn" />
        <Kpi label="Collected without OS caption" value={num(missing.collected_without_caption)} tone="warn" />
      </div>
      <Panel title="Old / unsupported OS" icon={Server} className="mt12">
        <DataTable rows={(d.old_os_devices ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'os', label: 'OS', render: (r) => S(r.os_caption) }, { key: 'build', label: 'Build', render: (r) => S(r.os_build) || '—' }, { key: 'method', label: 'Method', render: (r) => S(r.collection_method) }]}
          emptyTitle="No old OS" emptyMessage="No endpoint runs an unsupported Windows version." pageSizeDefault={15} />
      </Panel>
      <Panel title="Longest uptime (not rebooted)" icon={Activity} className="mt12">
        <DataTable rows={(d.not_rebooted ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'boot', label: 'Last boot', sortVal: (r) => S(r.last_boot), render: (r) => (r.last_boot ? timeAgo(S(r.last_boot)) : '—') }, { key: 'os', label: 'OS', render: (r) => S(r.os_caption) || '—' }]}
          emptyTitle="No boot data" emptyMessage="Last-boot time was not collected." pageSizeDefault={15} />
      </Panel>
    </>
  )
}

/* ---- 8. Network ---------------------------------------------------------- */

function NetworkTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/network', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  return (
    <>
      <div className="grid-2">
        <ChartPanel title="Link speed distribution" rows={d.link_speed_distribution as Row[]} kind="bar" icon={Network} />
        <Panel title="Devices per site" icon={Network}>
          <DataTable rows={(d.site_summary ?? []) as Row[]} getKey={(r) => S(r.site)} searchText={(r) => S(r.site)}
            cols={[{ key: 'site', label: 'Site', render: (r) => S(r.site) }, { key: 'devices', label: 'Devices', sortVal: (r) => N(r.devices), render: (r) => num(r.devices) }, { key: 'm', label: 'Managed', sortVal: (r) => N(r.managed), render: (r) => num(r.managed) }, { key: 'u', label: 'Unmanaged', sortVal: (r) => N(r.unmanaged), render: (r) => num(r.unmanaged) }]} pageSizeDefault={15} />
        </Panel>
      </div>
      <Panel title="Multi-NIC devices" icon={Network} className="mt12">
        <DataTable rows={(d.multi_nic ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'nic', label: 'NICs', sortVal: (r) => N(r.nic_count), render: (r) => num(r.nic_count) }]} pageSizeDefault={10} />
      </Panel>
      <div className="grid-2 mt12">
        <Panel title="APIPA (169.254.x) addresses" icon={Network}>
          <DataTable rows={(d.apipa ?? []) as Row[]} getKey={(r) => S(r.device_id) + S(r.nic)} searchText={(r) => S(r.ip_addresses)}
            cols={[{ key: 'nic', label: 'NIC', render: (r) => S(r.nic) }, { key: 'ips', label: 'IPs', mono: true, render: (r) => S(r.ip_addresses) }]}
            emptyTitle="None" emptyMessage="No NIC reported an APIPA address." pageSizeDefault={10} />
        </Panel>
        <Panel title="Duplicate MAC addresses" icon={Network}>
          <DataTable rows={(d.duplicate_mac ?? []) as Row[]} getKey={(r) => S(r.mac)} searchText={(r) => S(r.mac)}
            cols={[{ key: 'mac', label: 'MAC', mono: true, render: (r) => S(r.mac) }, { key: 'dc', label: 'Devices', sortVal: (r) => N(r.device_count), render: (r) => num(r.device_count) }]}
            emptyTitle="None" emptyMessage="No MAC is shared across devices." pageSizeDefault={10} />
        </Panel>
      </div>
      <Panel title="Managed devices with no NIC data" icon={MonitorSmartphone} className="mt12">
        <DataTable rows={(d.no_nic_devices ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'method', label: 'Method', render: (r) => S(r.collection_method) }]}
          emptyTitle="All have NICs" emptyMessage="Every collected device reported network interfaces." pageSizeDefault={10} />
      </Panel>
    </>
  )
}

/* ---- 9. Collection Health ------------------------------------------------ */

function CollectionHealthTab({ query }: { query: string }) {
  const { data, loading, err } = useEI('/endpoint-intelligence/collection-health', query)
  if (loading && !data) return <Loading />
  if (err) return <Err msg={err} />
  const d = data ?? {}
  const st = (d.staleness ?? {}) as Row
  const pi = (d.partial_inventory ?? {}) as Row
  return (
    <>
      <div className="kpi-grid">
        <Kpi label="Collected < 24h" value={num(st.within_24h)} tone="ok" />
        <Kpi label="1–7 days" value={num(st.within_7d)} />
        <Kpi label="7–30 days" value={num(st.within_30d)} tone="warn" />
        <Kpi label="> 30 days" value={num(st.older_30d)} tone="crit" />
        <Kpi label="Never collected" value={num(st.never)} tone="crit" />
      </div>
      <div className="grid-2 mt12">
        <ChartPanel title="Collection method" rows={d.method_distribution as Row[]} kind="donut" icon={ShieldCheck} />
        <ChartPanel title="Failure reason breakdown" rows={d.reason_breakdown as Row[]} kind="bar" />
      </div>
      <Panel title="Partial inventory (managed but missing a section)" icon={MonitorSmartphone} className="mt12">
        <div className="kpi-grid">
          <Kpi label="Missing software" value={num(pi.missing_software)} tone="warn" />
          <Kpi label="Missing processes" value={num(pi.missing_processes)} tone="warn" />
          <Kpi label="Missing disks" value={num(pi.missing_disks)} tone="warn" />
          <Kpi label="Missing services" value={num(pi.missing_services)} tone="warn" />
          <Kpi label="Missing NICs" value={num(pi.missing_nics)} tone="warn" />
          <Kpi label="Any partial" value={num(pi.any_partial)} tone="crit" />
        </div>
      </Panel>
      <Panel title="Stale devices (not collected in 7 days)" icon={ShieldCheck} className="mt12">
        <DataTable rows={(d.stale_devices ?? []) as Row[]} getKey={(r) => S(r.device_id)} searchText={deviceSearch}
          cols={[dev.name, dev.ip, dev.site, { key: 'method', label: 'Method', render: (r) => S(r.collection_method) }, { key: 'when', label: 'Collected', sortVal: (r) => S(r.collected_at), render: (r) => (r.collected_at ? timeAgo(S(r.collected_at)) : '—') }, { key: 'note', label: 'Note', render: (r) => S(r.software_note) || '—' }]}
          emptyTitle="All fresh" emptyMessage="Every managed device was collected within 7 days." pageSizeDefault={15} />
      </Panel>
    </>
  )
}
