import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'

// A client-side searchable / sortable / filterable / paginated table. Designed
// for the current data sizes (≤ a few thousand rows). The props are shaped so a
// server-side variant (passing pre-paged rows + total + onQueryChange) can be
// dropped in later without changing call sites much.

export interface DataCol<T> {
  key: string
  label: string
  render: (r: T) => ReactNode
  sortVal?: (r: T) => string | number // sortable column when set
  mono?: boolean
  width?: number
}

export interface DataFilter<T> {
  key: string
  label: string
  options: { value: string; label: string }[]
  match: (r: T, value: string) => boolean
}

export function DataTable<T>({
  rows, cols, filters = [], searchText, searchPlaceholder = 'Search…', pageSizeDefault = 25,
  getKey, emptyTitle = 'Nothing to show', emptyMessage = 'No rows match the current filters.', maxHeight = 520,
}: {
  rows: T[]
  cols: DataCol<T>[]
  filters?: DataFilter<T>[]
  searchText: (r: T) => string
  searchPlaceholder?: string
  pageSizeDefault?: number
  getKey: (r: T, i: number) => string
  emptyTitle?: string
  emptyMessage?: string
  maxHeight?: number
}) {
  const [q, setQ] = useState('')
  const [size, setSize] = useState(pageSizeDefault)
  const [page, setPage] = useState(1)
  const [sortKey, setSortKey] = useState<string | null>(null)
  const [dir, setDir] = useState<1 | -1>(1)
  const [fvals, setFvals] = useState<Record<string, string>>({})

  const filtered = useMemo(() => {
    const ql = q.trim().toLowerCase()
    let out = rows
    if (ql) out = out.filter((r) => searchText(r).toLowerCase().includes(ql))
    for (const f of filters) {
      const v = fvals[f.key]
      if (v) out = out.filter((r) => f.match(r, v))
    }
    if (sortKey) {
      const col = cols.find((c) => c.key === sortKey)
      if (col?.sortVal) {
        const sv = col.sortVal
        out = [...out].sort((a, b) => {
          const av = sv(a), bv = sv(b)
          if (av < bv) return -1 * dir
          if (av > bv) return 1 * dir
          return 0
        })
      }
    }
    return out
  }, [rows, q, fvals, sortKey, dir, filters, cols, searchText])

  const total = filtered.length
  const pages = Math.max(1, Math.ceil(total / size))
  const cur = Math.min(page, pages)
  const slice = filtered.slice((cur - 1) * size, cur * size)

  const toggleSort = (key: string) => {
    if (sortKey === key) setDir((d) => (d === 1 ? -1 : 1))
    else { setSortKey(key); setDir(1) }
    setPage(1)
  }

  return (
    <div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 8 }}>
        <input className="input" style={{ width: 240, padding: '6px 10px', fontSize: 13 }} placeholder={searchPlaceholder}
          value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} />
        {filters.map((f) => (
          <select key={f.key} className="input" style={{ padding: '6px 8px', fontSize: 13 }}
            value={fvals[f.key] ?? ''} onChange={(e) => { setFvals((p) => ({ ...p, [f.key]: e.target.value })); setPage(1) }}>
            <option value="">{f.label}: all</option>
            {f.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        ))}
        <span className="muted" style={{ fontSize: 12, marginLeft: 'auto' }}>
          {total} row{total === 1 ? '' : 's'}{q || Object.values(fvals).some(Boolean) ? ` (of ${rows.length})` : ''}
        </span>
        <select className="input" style={{ padding: '6px 8px', fontSize: 13 }} value={size}
          onChange={(e) => { setSize(Number(e.target.value)); setPage(1) }}>
          {[25, 50, 100].map((n) => <option key={n} value={n}>{n}/page</option>)}
        </select>
      </div>

      {total === 0 ? (
        <div className="muted" style={{ padding: 16, textAlign: 'center' }}><strong>{emptyTitle}</strong><div style={{ fontSize: 12 }}>{emptyMessage}</div></div>
      ) : (
        <div style={{ maxHeight, overflow: 'auto', border: '1px solid var(--border, #2a3947)', borderRadius: 6 }}>
          <table className="data-table" style={{ margin: 0 }}>
            <thead style={{ position: 'sticky', top: 0, zIndex: 1, background: 'var(--panel, #16202b)' }}>
              <tr>
                {cols.map((c) => (
                  <th key={c.key} style={{ cursor: c.sortVal ? 'pointer' : 'default', whiteSpace: 'nowrap', width: c.width }}
                    onClick={() => c.sortVal && toggleSort(c.key)}>
                    {c.label}{sortKey === c.key ? (dir === 1 ? ' ▲' : ' ▼') : c.sortVal ? ' ↕' : ''}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {slice.map((r, i) => (
                <tr key={getKey(r, i)}>
                  {cols.map((c) => <td key={c.key} className={c.mono ? 'mono' : undefined} style={{ fontSize: 12 }}>{c.render(r)}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {pages > 1 && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, justifyContent: 'center' }}>
          <button className="btn btn-ghost btn-sm" disabled={cur <= 1} onClick={() => setPage(cur - 1)}>← Prev</button>
          <span className="muted" style={{ fontSize: 12 }}>Page {cur} / {pages}</span>
          <button className="btn btn-ghost btn-sm" disabled={cur >= pages} onClick={() => setPage(cur + 1)}>Next →</button>
        </div>
      )}
    </div>
  )
}
