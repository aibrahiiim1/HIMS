import { useState } from 'react'
import { api, type SearchResult } from '../api'

// Search accepts an IP, MAC, or hostname and resolves where a device is
// physically connected: switch + port + VLAN, plus (later) the uplink path.
export function SearchPage() {
  const [q, setQ] = useState('')
  const [results, setResults] = useState<SearchResult | SearchResult[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function run() {
    if (!q.trim()) return
    setLoading(true); setErr(null); setResults(null)
    try {
      const r = await api.get<SearchResult | SearchResult[]>(`/search?q=${encodeURIComponent(q.trim())}`)
      setResults(r)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const list: SearchResult[] = results == null ? [] : Array.isArray(results) ? results : [results]

  return (
    <div>
      <div className="card">
        <h2>Search — IP / MAC / device name</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Find where any device is connected: enter an IP (172.21.15.44), a MAC
          (aa:bb:cc:dd:ee:ff), or a hostname. Resolves IP → MAC → switch + port + VLAN.
        </p>
        <div className="search-box">
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && run()}
            placeholder="172.21.15.44  ·  aa:bb:cc:dd:ee:ff  ·  PC-ACCOUNTS-05"
            autoFocus
          />
          <button onClick={run}>Search</button>
        </div>
      </div>

      {loading && <div className="card loading">Searching…</div>}
      {err && <div className="card error-msg">{err}</div>}

      {list.map((res, idx) => (
        <div className="card" key={idx}>
          <h2>
            {res.query_type.toUpperCase()}: {res.query}
            {res.mac && <span className="muted" style={{ marginLeft: 12, fontFamily: 'monospace' }}>{res.mac}</span>}
          </h2>
          {res.device_name && <p className="muted">Matched device: <strong>{res.device_name}</strong></p>}

          {res.switch_port.length === 0 ? (
            <div className="muted" style={{ marginTop: 8 }}>
              No switch port found. The MAC isn't in any collected FDB yet (run discovery on the access switches).
            </div>
          ) : (
            <table style={{ marginTop: 12 }}>
              <thead>
                <tr><th>Switch</th><th>Switch IP</th><th>Port</th><th>VLAN</th><th>Role</th></tr>
              </thead>
              <tbody>
                {res.switch_port.map((sp, i) => (
                  <tr key={i}>
                    <td><strong>{sp.switch_name}</strong></td>
                    <td>{sp.switch_ip ?? '—'}</td>
                    <td>{sp.if_name ?? (sp.if_index != null ? `ifIndex ${sp.if_index}` : '—')}</td>
                    <td>{sp.vlan_id}</td>
                    <td>{sp.port_role ? <span className={`badge badge-${sp.port_role}`}>{sp.port_role}</span> : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {res.path && res.path.length > 0 && (
            <div style={{ marginTop: 16 }}>
              <div className="muted" style={{ marginBottom: 6 }}>Path</div>
              <div className="path-chain">
                {res.path.map((step, i) => (
                  <span key={i} className="path-chain">
                    <span className="path-node">
                      <strong>{step.device_name ?? step.ip ?? '?'}</strong>
                      {step.if_name && <span className="muted">{step.if_name}</span>}
                    </span>
                    {i < res.path.length - 1 && <span className="path-arrow">→</span>}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
