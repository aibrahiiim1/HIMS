/* eslint-disable react-refresh/only-export-components */
// This is a shared UI kit: it intentionally co-locates presentational
// components with their small formatting helpers (timeAgo, colorFor,
// STATUS_COLOR). The react-refresh rule is an HMR-only nicety, not correctness.
import { useMemo, useState } from 'react'
import type { ComponentType, ReactNode, MouseEvent as ReactMouseEvent } from 'react'

/* ============================================================================
   Enterprise UI kit — presentational primitives shared across all pages.
   Pure SVG charts (no chart-lib dependency). Styling lives in App.css.
   ========================================================================== */

type IconType = ComponentType<{ size?: number | string; className?: string }>

/* ---- Page header ---------------------------------------------------------- */
export function PageHeader({ title, subtitle, icon: Icon, actions }: {
  title: string; subtitle?: string; icon?: IconType; actions?: ReactNode
}) {
  return (
    <div className="pg-header">
      <div className="pg-header-main">
        {Icon && <span className="pg-header-icon"><Icon size={22} /></span>}
        <div>
          <h1>{title}</h1>
          {subtitle && <div className="sub">{subtitle}</div>}
        </div>
      </div>
      {actions && <div className="pg-header-actions">{actions}</div>}
    </div>
  )
}

/* ---- Panel (card with header) -------------------------------------------- */
export function Panel({ title, subtitle, icon: Icon, actions, children, className = '', pad = true }: {
  title?: ReactNode; subtitle?: ReactNode; icon?: IconType; actions?: ReactNode
  children: ReactNode; className?: string; pad?: boolean
}) {
  return (
    <section className={`panel ${className}`}>
      {(title || actions) && (
        <header className="panel-head">
          <div className="panel-title">
            {Icon && <Icon size={16} />}
            <span>{title}</span>
            {subtitle && <span className="panel-sub">{subtitle}</span>}
          </div>
          {actions && <div className="panel-actions">{actions}</div>}
        </header>
      )}
      <div className={pad ? 'panel-body' : 'panel-body no-pad'}>{children}</div>
    </section>
  )
}

/* ---- KPI / stat card ------------------------------------------------------ */
export type Tone = 'default' | 'ok' | 'warn' | 'crit' | 'info'
export function Kpi({ label, value, sub, tone = 'default', icon: Icon, onClick }: {
  label: string; value: ReactNode; sub?: ReactNode; tone?: Tone; icon?: IconType; onClick?: () => void
}) {
  return (
    <div className={`kpi tone-${tone}${onClick ? ' is-clickable' : ''}`} onClick={onClick}>
      <div className="kpi-top">
        <span className="kpi-label">{label}</span>
        {Icon && <span className="kpi-icon"><Icon size={18} /></span>}
      </div>
      <div className="kpi-value">{value}</div>
      {sub != null && <div className="kpi-sub">{sub}</div>}
    </div>
  )
}

/* ---- Status pill ---------------------------------------------------------- */
const STATUS_MAP: Record<string, { cls: string; label: string }> = {
  up: { cls: 'badge-up', label: 'Online' },
  online: { cls: 'badge-up', label: 'Online' },
  down: { cls: 'badge-down', label: 'Offline' },
  offline: { cls: 'badge-down', label: 'Offline' },
  warning: { cls: 'badge-warning', label: 'Warning' },
  needs_attention: { cls: 'badge-warning', label: 'Needs attention' },
  unknown: { cls: 'badge-unknown', label: 'Unknown' },
}
export function StatusPill({ status, label }: { status: string; label?: string }) {
  const m = STATUS_MAP[status?.toLowerCase()] ?? { cls: 'badge-unknown', label: status || 'Unknown' }
  return <span className={`badge ${m.cls}`}>{label ?? m.label}</span>
}

/* ---- Health ring (SVG gauge with score) ----------------------------------- */
export function HealthRing({ score, size = 120, label = 'Health' }: { score: number; size?: number; label?: string }) {
  const s = Math.max(0, Math.min(100, Math.round(score)))
  const stroke = size < 90 ? 8 : 11
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const off = c * (1 - s / 100)
  const color = s >= 90 ? 'var(--ok)' : s >= 70 ? 'var(--warn)' : 'var(--crit)'
  return (
    <div className="health-ring" style={{ width: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--surface-3)" strokeWidth={stroke} />
        <circle
          cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth={stroke}
          strokeDasharray={c} strokeDashoffset={off} strokeLinecap="round"
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
        <text x="50%" y="48%" textAnchor="middle" className="ring-score" fill="var(--text)">{s}</text>
        <text x="50%" y="64%" textAnchor="middle" className="ring-unit" fill="var(--text-muted)">/ 100</text>
      </svg>
      {label && <div className="ring-label">{label}</div>}
    </div>
  )
}

/* ---- Donut chart ---------------------------------------------------------- */
export function Donut({ data, size = 160, thickness = 26, centerLabel, centerValue }: {
  data: { label: string; value: number; color: string }[]
  size?: number; thickness?: number; centerLabel?: string; centerValue?: ReactNode
}) {
  const total = data.reduce((a, d) => a + d.value, 0)
  const r = (size - thickness) / 2
  const c = 2 * Math.PI * r
  let acc = 0
  return (
    <div className="donut-wrap">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="donut">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--surface-3)" strokeWidth={thickness} />
        {total > 0 && data.map((d, i) => {
          const frac = d.value / total
          const dash = c * frac
          const seg = (
            <circle
              key={i} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={d.color} strokeWidth={thickness}
              strokeDasharray={`${dash} ${c - dash}`} strokeDashoffset={-acc}
              transform={`rotate(-90 ${size / 2} ${size / 2})`}
            />
          )
          acc += dash
          return seg
        })}
        {(centerValue != null || centerLabel) && (
          <>
            <text x="50%" y="47%" textAnchor="middle" className="donut-value" fill="var(--text)">{centerValue ?? total}</text>
            {centerLabel && <text x="50%" y="62%" textAnchor="middle" className="donut-label" fill="var(--text-muted)">{centerLabel}</text>}
          </>
        )}
      </svg>
    </div>
  )
}

export function Legend({ data, total }: { data: { label: string; value: number; color: string }[]; total?: number }) {
  const t = total ?? data.reduce((a, d) => a + d.value, 0)
  return (
    <ul className="legend">
      {data.map((d, i) => (
        <li key={i}>
          <span className="legend-dot" style={{ background: d.color }} />
          <span className="legend-label">{d.label}</span>
          <span className="legend-value">{d.value}{t > 0 && <em>{Math.round((d.value / t) * 100)}%</em>}</span>
        </li>
      ))}
    </ul>
  )
}

/* ---- Progress bar (0–100%) ------------------------------------------------ */
// A labelled determinate progress bar — used for live scan progress (hosts
// scanned of total). `pulse` adds a subtle pulse while the work is in flight.
export function ProgressBar({ value, label, sublabel, tone = 'var(--brand)', pulse = false }: {
  value: number; label?: string; sublabel?: string; tone?: string; pulse?: boolean
}) {
  const pct = Math.max(0, Math.min(100, Math.round(value)))
  return (
    <div style={{ width: '100%' }}>
      {(label || sublabel) && (
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 5, fontSize: 12 }}>
          <span style={{ fontWeight: 600 }}>{label}{sublabel ? <span className="muted" style={{ fontWeight: 400 }}> · {sublabel}</span> : null}</span>
          <span className="mono" style={{ color: tone }}>{pct}%</span>
        </div>
      )}
      <div style={{ height: 9, borderRadius: 6, background: 'var(--border, #2a3947)', overflow: 'hidden' }}>
        <div style={{
          height: '100%', borderRadius: 6, background: tone, width: `${pct}%`,
          transition: 'width .5s ease',
          animation: pulse && pct < 100 ? 'kdrpulse 1.4s ease-in-out infinite' : 'none',
        }} />
      </div>
    </div>
  )
}

/* ---- Horizontal bar list -------------------------------------------------- */
export function BarList({ rows, color = 'var(--brand)', max }: {
  rows: { label: string; value: number; color?: string; to?: string }[]
  color?: string; max?: number
}) {
  const top = max ?? Math.max(1, ...rows.map((r) => r.value))
  return (
    <div className="bar-list">
      {rows.length === 0 && <div className="muted">No data.</div>}
      {rows.map((r, i) => (
        <div className="bar-row" key={i}>
          <span className="bar-label" title={r.label}>{r.label}</span>
          <span className="bar-track">
            <span className="bar-fill" style={{ width: `${(r.value / top) * 100}%`, background: r.color ?? color }} />
          </span>
          <span className="bar-value">{r.value}</span>
        </div>
      ))}
    </div>
  )
}

/* ---- Sparkline ------------------------------------------------------------ */
export function Sparkline({ points, width = 120, height = 32, color = 'var(--brand)', fill = true }: {
  points: number[]; width?: number; height?: number; color?: string; fill?: boolean
}) {
  if (points.length < 2) return <svg width={width} height={height} className="sparkline" />
  const min = Math.min(...points), max = Math.max(...points)
  const span = max - min || 1
  const step = width / (points.length - 1)
  const ys = points.map((p) => height - 3 - ((p - min) / span) * (height - 6))
  const line = points.map((_, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${ys[i].toFixed(1)}`).join(' ')
  const area = `${line} L${width},${height} L0,${height} Z`
  return (
    <svg width={width} height={height} className="sparkline" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
      {fill && <path d={area} fill={color} opacity={0.12} />}
      <path d={line} fill="none" stroke={color} strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

/* ---- Utilization meter ---------------------------------------------------- */
export function Meter({ value, max = 100, label, unit = '%' }: { value: number; max?: number; label?: string; unit?: string }) {
  const pct = Math.max(0, Math.min(100, (value / max) * 100))
  const tone = pct >= 90 ? 'crit' : pct >= 75 ? 'warn' : 'ok'
  return (
    <div className="meter">
      {label && <div className="meter-head"><span>{label}</span><span>{Math.round(value)}{unit}</span></div>}
      <div className="meter-track"><div className={`meter-fill tone-${tone}`} style={{ width: `${pct}%` }} /></div>
    </div>
  )
}

/* ---- Activity feed -------------------------------------------------------- */
export function ActivityFeed({ items }: { items: { icon?: IconType; tone?: Tone; title: ReactNode; meta?: ReactNode; time?: string }[] }) {
  if (items.length === 0) return <div className="muted">No recent activity.</div>
  return (
    <ul className="activity">
      {items.map((it, i) => {
        const Icon = it.icon
        return (
          <li key={i} className="activity-item">
            <span className={`activity-dot tone-${it.tone ?? 'default'}`}>{Icon && <Icon size={13} />}</span>
            <div className="activity-body">
              <div className="activity-title">{it.title}</div>
              {it.meta && <div className="activity-meta">{it.meta}</div>}
            </div>
            {it.time && <span className="activity-time">{it.time}</span>}
          </li>
        )
      })}
    </ul>
  )
}

/* ---- Operational health panel (Dashboard) --------------------------------- */
const OPH_STATUS: Record<string, { cls: string; label: string }> = {
  healthy: { cls: 'badge-up', label: 'Healthy' },
  warning: { cls: 'badge-warning', label: 'Warning' },
  critical: { cls: 'badge-down', label: 'Critical' },
  unknown: { cls: 'badge-unknown', label: 'Not collected yet' },
}
export function OperationalHealthPanel({ title, icon, status, rows, impact, action, notCollectedReason }: {
  title: string; icon?: IconType; status: string
  rows: { label: string; value: ReactNode }[]
  impact?: ReactNode; action?: ReactNode; notCollectedReason?: string
}) {
  const m = OPH_STATUS[status] ?? OPH_STATUS.unknown
  const empty = status === 'unknown'
  return (
    <Panel title={title} icon={icon} actions={<span className={`badge ${m.cls}`}>{m.label}</span>}>
      {empty ? (
        <div className="muted" style={{ fontSize: 13 }}>{notCollectedReason ?? 'Not collected yet.'}</div>
      ) : (
        <ul className="sec-health">
          {rows.map((r) => (
            <li key={r.label}><span className="muted">{r.label}</span><span className="sec-val">{r.value}</span></li>
          ))}
        </ul>
      )}
      {impact && <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>{impact}</p>}
      {action && <div style={{ marginTop: 10 }}>{action}</div>}
    </Panel>
  )
}

/* ---- Empty state ---------------------------------------------------------- */
export function EmptyState({ icon: Icon, title, message, action }: {
  icon?: IconType; title: string; message?: string; action?: ReactNode
}) {
  return (
    <div className="empty">
      {Icon && <span className="empty-icon"><Icon size={30} /></span>}
      <h3>{title}</h3>
      {message && <p>{message}</p>}
      {action && <div className="empty-action">{action}</div>}
    </div>
  )
}

/* ---- Tab bar -------------------------------------------------------------- */
export function TabBar({ tabs, active, onChange }: {
  tabs: { key: string; label: string; icon?: IconType; count?: number }[]
  active: string; onChange: (k: string) => void
}) {
  return (
    <div className="tabbar" role="tablist">
      {tabs.map((t) => {
        const Icon = t.icon
        return (
          <button
            key={t.key} role="tab" aria-selected={active === t.key}
            className={'tabbar-tab' + (active === t.key ? ' active' : '')}
            onClick={() => onChange(t.key)}
          >
            {Icon && <Icon size={15} />}
            <span>{t.label}</span>
            {t.count != null && <span className="tabbar-count">{t.count}</span>}
          </button>
        )
      })}
    </div>
  )
}

/* ---- Definition list (key/value grid) ------------------------------------ */
export function DefList({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="deflist">
      {items.map((it, i) => (
        <div key={i}>
          <dt>{it.label}</dt>
          <dd>{it.value ?? '—'}</dd>
        </div>
      ))}
    </dl>
  )
}

/* ---- pagination ----------------------------------------------------------- */
// usePaged is client-side pagination over an already-loaded array, with an
// optional case-insensitive text filter. Heavy collections (services, software,
// device lists) are fully fetched, so this keeps the DOM small + scrollable
// without a backend change. Returns the current page slice + Pager props.
export function usePaged<T>(items: T[], opts?: { pageSize?: number; filter?: string; match?: (it: T, q: string) => boolean }) {
  const pageSize = opts?.pageSize ?? 10
  const [page, setPage] = useState(0)
  const q = (opts?.filter ?? '').trim().toLowerCase()
  const filtered = useMemo(() => {
    if (!q || !opts?.match) return items
    return items.filter((it) => opts.match!(it, q))
  }, [items, q, opts])
  const total = filtered.length
  const pages = Math.max(1, Math.ceil(total / pageSize))
  const cur = Math.min(page, pages - 1)
  const slice = filtered.slice(cur * pageSize, cur * pageSize + pageSize)
  return { slice, total, page: cur, pages, pageSize, setPage }
}

export function Pager({ page, pages, total, pageSize, onPage }: {
  page: number; pages: number; total: number; pageSize: number; onPage: (p: number) => void
}) {
  if (total <= pageSize) return null
  const from = page * pageSize + 1
  const to = Math.min(total, (page + 1) * pageSize)
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, justifyContent: 'flex-end', padding: '8px 2px', fontSize: 12 }}>
      <span className="muted">Showing {from}–{to} of {total}</span>
      <button className="btn btn-xs btn-ghost" disabled={page <= 0} onClick={() => onPage(page - 1)}>‹ Prev</button>
      <span className="muted">Page {page + 1} / {pages}</span>
      <button className="btn btn-xs btn-ghost" disabled={page >= pages - 1} onClick={() => onPage(page + 1)}>Next ›</button>
    </div>
  )
}

/* ---- helpers -------------------------------------------------------------- */
export function timeAgo(iso?: string | null): string {
  if (!iso) return 'never'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const s = Math.floor((Date.now() - t) / 1000)
  if (s < 0) return 'just now'
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60); if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60); if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24); if (d < 30) return `${d}d ago`
  const mo = Math.floor(d / 30); if (mo < 12) return `${mo}mo ago`
  return `${Math.floor(mo / 12)}y ago`
}

// Stable color for a label (category/vendor/status) — deterministic hash → palette.
const PALETTE = ['#2563eb', '#16a34a', '#d97706', '#dc2626', '#7c3aed', '#0891b2', '#db2777', '#65a30d', '#ea580c', '#0d9488', '#4f46e5', '#9333ea']
export function colorFor(label: string): string {
  let h = 0
  for (let i = 0; i < label.length; i++) h = (h * 31 + label.charCodeAt(i)) >>> 0
  return PALETTE[h % PALETTE.length]
}

export const STATUS_COLOR: Record<string, string> = {
  up: 'var(--ok)', online: 'var(--ok)',
  down: 'var(--crit)', offline: 'var(--crit)',
  warning: 'var(--warn)', needs_attention: 'var(--warn)',
  unknown: 'var(--neutral)',
}

/* ---- AreaChart: responsive time-series line+area with hover -------------- */
// A dependency-free trend chart for historical analytics (availability %,
// latency over time, alert rate). Stretches to its container width; hover
// reveals the value + label at the nearest point. Pass `baseline` to draw a
// reference line (e.g. an SLA target).
export function AreaChart({
  points, labels, height = 140, color = 'var(--brand)', area = true,
  min, max, unit = '', baseline, valueFmt, ariaLabel,
}: {
  points: number[]
  labels?: string[]
  height?: number
  color?: string
  area?: boolean
  min?: number
  max?: number
  unit?: string
  baseline?: number
  valueFmt?: (v: number) => string
  ariaLabel?: string
}) {
  const [hi, setHi] = useState<number | null>(null)
  if (!points.length) {
    return <div className="chart-empty" style={{ height }}>No data in this window</div>
  }
  const lo = min ?? Math.min(...points)
  const hiV = max ?? Math.max(...points, lo + 1)
  const span = hiV - lo || 1
  const W = 1000, H = 300, padT = 10
  const x = (i: number) => (points.length === 1 ? W / 2 : (i / (points.length - 1)) * W)
  const y = (v: number) => padT + (H - padT) * (1 - (v - lo) / span)
  const line = points.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
  const fmt = valueFmt ?? ((v: number) => `${Math.round(v * 100) / 100}${unit}`)
  const onMove = (e: ReactMouseEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    const frac = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
    setHi(Math.round(frac * (points.length - 1)))
  }
  return (
    <div className="areachart" style={{ position: 'relative' }} onMouseMove={onMove} onMouseLeave={() => setHi(null)} role="img" aria-label={ariaLabel}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ width: '100%', height, display: 'block' }}>
        {area && <polygon points={`0,${H} ${line} ${W},${H}`} fill={color} opacity={0.12} />}
        {baseline != null && baseline >= lo && baseline <= hiV && (
          <line x1={0} x2={W} y1={y(baseline)} y2={y(baseline)} stroke="var(--warn)" strokeWidth={1} strokeDasharray="6 6" vectorEffect="non-scaling-stroke" opacity={0.7} />
        )}
        <polyline points={line} fill="none" stroke={color} strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
        {hi != null && (
          <>
            <line x1={x(hi)} x2={x(hi)} y1={0} y2={H} stroke="var(--border)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
            <circle cx={x(hi)} cy={y(points[hi])} r={5} fill={color} stroke="var(--panel)" strokeWidth={2} vectorEffect="non-scaling-stroke" />
          </>
        )}
      </svg>
      <div className="areachart-ax"><span>{fmt(hiV)}</span><span>{fmt(lo)}</span></div>
      {labels && labels.length > 0 && (
        <div className="areachart-x"><span>{labels[0]}</span>{labels.length > 2 && <span>{labels[Math.floor(labels.length / 2)]}</span>}<span>{labels[labels.length - 1]}</span></div>
      )}
      {hi != null && (
        <div className="areachart-tip" style={{ left: `${(x(hi) / W) * 100}%` }}>
          <b>{fmt(points[hi])}</b>
          {labels && labels[hi] && <small>{labels[hi]}</small>}
        </div>
      )}
    </div>
  )
}
