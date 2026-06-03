import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Device } from '../api'

// Detail page per category (where one exists); others list without a link.
const DETAIL_BASE: Record<string, string> = {
  switch: '/devices',
  server: '/servers',
  virtual_host: '/virtual-hosts',
  firewall: '/firewalls',
  camera: '/cctv',
  nvr: '/cctv',
  wireless_controller: '/wlan',
  printer: '/printers',
  ups: '/ups',
  pbx: '/pbx',
}

const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13,
}

// Full inventory across every category — including service types (dhcp, dns,
// database, directory, application, endpoint, …) that have no dedicated nav
// page. This is where a Kea DHCP web app, a database, etc. become browsable.
export function Inventory() {
  const [cat, setCat] = useState('all')
  const [q, setQ] = useState('')
  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', 'all'],
    queryFn: () => api.get<Device[]>('/devices?category=all'),
  })

  const counts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const d of data ?? []) m[d.category] = (m[d.category] ?? 0) + 1
    return m
  }, [data])

  const cats = useMemo(() => Object.keys(counts).sort(), [counts])

  const rows = useMemo(() => {
    let r = data ?? []
    if (cat !== 'all') r = r.filter((d) => d.category === cat)
    if (q.trim()) {
      const t = q.toLowerCase()
      r = r.filter((d) =>
        d.name.toLowerCase().includes(t) ||
        (d.primary_ip ?? '').toLowerCase().includes(t) ||
        (d.vendor ?? '').toLowerCase().includes(t))
    }
    return r
  }, [data, cat, q])

  return (
    <div>
      <div className="card">
        <h2>Inventory <span className="muted" style={{ fontSize: 13, fontWeight: 400 }}>— every device, all categories</span></h2>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
          <select style={{ ...input, width: 220 }} value={cat} onChange={(e) => setCat(e.target.value)}>
            <option value="all">All categories ({(data ?? []).length})</option>
            {cats.map((c) => <option key={c} value={c}>{c} ({counts[c]})</option>)}
          </select>
          <input style={{ ...input, width: 220 }} placeholder="filter by name / IP / vendor" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {/* Category chips with counts — click to filter */}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {cats.map((c) => (
            <button
              key={c}
              onClick={() => setCat(c === cat ? 'all' : c)}
              style={{
                padding: '3px 10px', borderRadius: 12, fontSize: 12, cursor: 'pointer',
                border: '1px solid #555',
                background: cat === c ? '#1565c0' : 'transparent',
                color: cat === c ? '#fff' : '#bbb',
              }}
            >
              {c} <b>{counts[c]}</b>
            </button>
          ))}
        </div>
      </div>

      <div className="card">
        {isLoading && <div className="loading">Loading inventory…</div>}
        {error && <div className="error-msg">Failed to load: {(error as Error).message}</div>}
        {data && rows.length === 0 && <div className="muted">No devices match.</div>}
        {rows.length > 0 && (
          <table>
            <thead>
              <tr><th>Name</th><th>IP</th><th>Category</th><th>Vendor</th><th>Model</th><th>Driver</th><th>Status</th></tr>
            </thead>
            <tbody>
              {rows.map((d) => {
                const base = DETAIL_BASE[d.category]
                return (
                  <tr key={d.id}>
                    <td>{base ? <Link to={`${base}/${d.id}`}>{d.name}</Link> : d.name}</td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{d.primary_ip ?? '—'}</td>
                    <td>{d.category}</td>
                    <td>{d.vendor ?? '—'}</td>
                    <td>{d.model ?? '—'}</td>
                    <td>{d.driver ?? '—'}</td>
                    <td><span className={`badge badge-${d.status}`}>{d.status}</span></td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
