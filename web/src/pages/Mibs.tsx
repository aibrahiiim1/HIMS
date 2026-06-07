import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  api, type MibFile, type MibObject, type OIDMapping,
  type MibPack, type MibPackDetail, type MibPackTable, type MibTestResult, type MibWalkRow,
} from '../api'
import { usePaged, Pager } from '../components/ui'

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 10px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12,
}
const danger: React.CSSProperties = { ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}
const tab = (active: boolean): React.CSSProperties => ({
  padding: '8px 16px', border: 'none', borderBottom: active ? '2px solid #1565c0' : '2px solid transparent',
  background: 'transparent', color: active ? '#1565c0' : '#90a4ae', cursor: 'pointer', fontWeight: 600, fontSize: 14,
})

const PURPOSES = ['clients', 'aps', 'ssids', 'radios', 'events', 'operational', 'stats']

// Domain fields each purpose can map a column to (the mapping assistant). column
// 0 means "use the row index" (MAC fields render the index as a MAC address).
const FIELD_HINTS: Record<string, string[]> = {
  clients: ['client_mac', 'client_ip', 'client_hostname', 'client_ap', 'client_ssid', 'client_rssi', 'client_band'],
  aps: ['ap_name', 'ap_mac', 'ap_ip', 'ap_model', 'ap_serial', 'ap_firmware', 'ap_status'],
  ssids: ['ssid_name', 'ssid_ssid', 'ssid_status', 'ssid_security', 'ssid_band', 'ssid_vlan'],
  radios: ['radio_ap', 'radio_name', 'radio_band', 'radio_channel', 'radio_power'],
  events: ['event_message', 'event_severity', 'event_time'],
  operational: ['operational_status'],
  stats: [],
}

function statusBadge(status: string) {
  const map: Record<string, string> = {
    supported: 'badge-success', empty: 'badge-warning', timeout: 'badge-danger',
    no_such_object: 'badge-muted', error: 'badge-danger',
  }
  return <span className={`badge ${map[status] ?? 'badge-muted'}`}>{status}</span>
}

export function Mibs() {
  const [view, setView] = useState<'packs' | 'library'>('packs')
  return (
    <div>
      <div className="card" style={{ paddingBottom: 0 }}>
        <h2 style={{ marginBottom: 4 }}>MIB Management</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          MIB packs make uploaded or built-in MIBs <strong>actually drive SNMP collection</strong> — like Vendor
          Fingerprints. Built-in packs ship as fallback; your uploaded packs win by priority. Map a table to a root
          OID + purpose, test it against a real device, then run collection.
        </p>
        <div style={{ display: 'flex', gap: 4, borderBottom: '1px solid #2a3947' }}>
          <button style={tab(view === 'packs')} onClick={() => setView('packs')}>MIB Packs</button>
          <button style={tab(view === 'library')} onClick={() => setView('library')}>OID Library (legacy)</button>
        </div>
      </div>
      {view === 'packs' ? <PacksTab /> : <LibraryTab />}
    </div>
  )
}

// ============================ MIB Packs ====================================

function PacksTab() {
  const qc = useQueryClient()
  const [sp] = useSearchParams()
  const [selID, setSelID] = useState<string | null>(sp.get('pack'))
  const prefill = sp.get('root') || sp.get('table') || sp.get('purpose')
    ? { table: sp.get('table') ?? undefined, root: sp.get('root') ?? undefined, purpose: sp.get('purpose') ?? undefined }
    : undefined
  const packs = useQuery({ queryKey: ['mib-packs'], queryFn: () => api.get<MibPack[]>('/mib-packs') })

  const toggle = useMutation({
    mutationFn: (p: MibPack) => api.patch(`/mib-packs/${p.id}`, { enabled: !p.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['mib-packs'] }),
  })
  const setPrio = useMutation({
    mutationFn: ({ id, priority }: { id: string; priority: number }) => api.patch(`/mib-packs/${id}`, { priority }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['mib-packs'] }),
  })
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/mib-packs/${id}`),
    onSuccess: () => { setSelID(null); qc.invalidateQueries({ queryKey: ['mib-packs'] }) },
  })

  const list = packs.data ?? []
  const builtin = list.filter((p) => p.source === 'builtin')
  const user = list.filter((p) => p.source === 'user')

  const renderRows = (rows: MibPack[]) => rows.map((p) => (
    <tr key={p.id} style={selID === p.id ? { background: 'rgba(21,101,192,0.12)' } : undefined}>
      <td>
        <strong>{p.name}</strong>
        {!p.enabled && <span className="badge badge-muted" style={{ marginLeft: 6 }}>disabled</span>}
        {p.last_test_detail && <div className="muted" style={{ fontSize: 11 }}>{p.last_test_detail}</div>}
      </td>
      <td><span className={`badge ${p.source === 'user' ? 'badge-success' : 'badge-muted'}`}>{p.source}</span></td>
      <td>{p.vendor || '—'}</td>
      <td>{p.category || '—'}</td>
      <td style={{ fontSize: 12 }}>{p.version || '—'}</td>
      <td>
        <input type="number" defaultValue={p.priority} style={{ ...input, width: 64, padding: '3px 6px' }}
          onBlur={(e) => { const v = Number(e.target.value); if (v !== p.priority) setPrio.mutate({ id: p.id, priority: v }) }} />
      </td>
      <td style={{ textAlign: 'center' }}>{p.table_count}</td>
      <td style={{ textAlign: 'center' }}>{p.file_count}</td>
      <td style={{ whiteSpace: 'nowrap' }}>
        <button style={ghost} onClick={() => setSelID(p.id)}>Open</button>{' '}
        <button style={ghost} onClick={() => toggle.mutate(p)}>{p.enabled ? 'Disable' : 'Enable'}</button>{' '}
        {p.source === 'user' && (
          <button style={danger} onClick={() => { if (confirm(`Delete MIB pack "${p.name}"?`)) del.mutate(p.id) }}>Delete</button>
        )}
      </td>
    </tr>
  ))

  return (
    <>
      <UploadCard onDone={() => qc.invalidateQueries({ queryKey: ['mib-packs'] })} />

      <div className="card">
        <h3>MIB packs <span className="muted" style={{ fontWeight: 400, fontSize: 13 }}>(user packs win over built-in by priority — lower runs first)</span></h3>
        {list.length === 0 && <div className="muted">No MIB packs yet. Upload one above; the built-in Extreme/HiPath pack seeds on server start.</div>}
        {list.length > 0 && (
          <table>
            <thead><tr>
              <th>Name</th><th>Source</th><th>Vendor</th><th>Category</th><th>Version</th>
              <th>Priority</th><th>Tables</th><th>Files</th><th></th>
            </tr></thead>
            <tbody>
              {user.length > 0 && <tr><td colSpan={9} className="muted" style={{ fontSize: 11, paddingTop: 8 }}>USER PACKS</td></tr>}
              {renderRows(user)}
              {builtin.length > 0 && <tr><td colSpan={9} className="muted" style={{ fontSize: 11, paddingTop: 8 }}>BUILT-IN (FALLBACK)</td></tr>}
              {renderRows(builtin)}
            </tbody>
          </table>
        )}
      </div>

      {selID && <PackDetail id={selID} onClose={() => setSelID(null)} prefill={prefill} />}
    </>
  )
}

function UploadCard({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [vendor, setVendor] = useState('')
  const [category, setCategory] = useState('wireless_controller')
  const [file, setFile] = useState<File | null>(null)
  const [result, setResult] = useState<string | null>(null)
  const ref = useRef<HTMLInputElement>(null)

  const up = useMutation({
    mutationFn: async () => {
      const fd = new FormData()
      if (name) fd.append('name', name)
      if (vendor) fd.append('vendor', vendor)
      if (category) fd.append('category', category)
      fd.append('file', file!)
      return api.postForm<{ files: number; modules: string[]; tables: string[]; warnings: string[] }>('/mib-packs/upload', fd)
    },
    onSuccess: (r) => {
      setResult(`Parsed ${r.files} file(s): ${r.modules.length} module(s), ${r.tables.length} table(s)${r.warnings?.length ? `, ${r.warnings.length} warning(s)` : ''}.`)
      setName(''); setVendor(''); setFile(null); if (ref.current) ref.current.value = ''
      onDone()
    },
  })

  return (
    <div className="card">
      <h3>Upload MIB pack</h3>
      <p className="muted" style={{ marginBottom: 10 }}>
        Drop a single MIB (<code>.mib</code>/<code>.txt</code>/<code>.my</code>) or a vendor ZIP. Files are parsed for
        modules + tables; you then map tables to OIDs below. Uploading is not a dead screen — your pack becomes a
        real, testable collector.
      </p>
      <div style={{ display: 'grid', gap: 8, gridTemplateColumns: '1fr 1fr 1fr', maxWidth: 720 }}>
        <input style={input} placeholder="Pack name (optional)" value={name} onChange={(e) => setName(e.target.value)} />
        <input style={input} placeholder="Vendor (e.g. Extreme Networks)" value={vendor} onChange={(e) => setVendor(e.target.value)} />
        <input style={input} placeholder="Category (e.g. wireless_controller)" value={category} onChange={(e) => setCategory(e.target.value)} />
      </div>
      <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 12 }}>
        <input ref={ref} type="file" accept=".mib,.txt,.my,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        <button style={btn} disabled={!file || up.isPending} onClick={() => { setResult(null); up.mutate() }}>
          {up.isPending ? 'Uploading & parsing…' : 'Upload & parse'}
        </button>
        {up.error && <span className="error-msg">{(up.error as Error).message}</span>}
        {result && <span className="badge badge-success">{result}</span>}
      </div>
    </div>
  )
}

function PackDetail({ id, onClose, prefill }: { id: string; onClose: () => void; prefill?: { table?: string; root?: string; purpose?: string } }) {
  const qc = useQueryClient()
  const detail = useQuery({ queryKey: ['mib-pack', id], queryFn: () => api.get<MibPackDetail>(`/mib-packs/${id}`) })
  const d = detail.data
  if (!d) return <div className="card">Loading pack…</div>

  const meta = (d.pack.parse_meta ?? {}) as { modules?: string[]; tables?: string[]; warnings?: string[]; object_count?: number }

  return (
    <div className="card" style={{ borderLeft: '3px solid #1565c0' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0 }}>{d.pack.name} <span className={`badge ${d.pack.source === 'user' ? 'badge-success' : 'badge-muted'}`}>{d.pack.source}</span></h3>
        <button style={ghost} onClick={onClose}>Close</button>
      </div>
      <p className="muted" style={{ fontSize: 13 }}>{d.pack.description}</p>

      {/* Parse metadata */}
      {(meta.modules?.length || meta.tables?.length || meta.warnings?.length) && (
        <div style={{ fontSize: 13, marginBottom: 12 }}>
          {meta.modules?.length ? <div><strong>Modules:</strong> {meta.modules.join(', ')}</div> : null}
          {meta.tables?.length ? <div><strong>Parsed tables:</strong> {meta.tables.slice(0, 30).join(', ')}{meta.tables.length > 30 ? ` … (+${meta.tables.length - 30})` : ''}</div> : null}
          {meta.warnings?.length ? <div style={{ color: '#ffb74d' }}><strong>Warnings:</strong> {meta.warnings.join('; ')}</div> : null}
        </div>
      )}

      {/* Files */}
      {d.files.length > 0 && (
        <details style={{ marginBottom: 12 }}>
          <summary style={{ cursor: 'pointer', fontWeight: 600 }}>Uploaded files ({d.files.length})</summary>
          <table style={{ marginTop: 6 }}>
            <thead><tr><th>Filename</th><th>Module</th><th>Size</th><th>Parse</th></tr></thead>
            <tbody>
              {d.files.map((f) => (
                <tr key={f.id}>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{f.filename}</td>
                  <td>{f.module_name || '—'}</td>
                  <td>{(f.size_bytes / 1024).toFixed(1)} KB</td>
                  <td>{f.parse_status === 'ok' ? <span className="badge badge-success">ok</span> : <span className="badge badge-warning" title={f.parse_detail}>{f.parse_status}</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}

      <MappingEditor packId={id} tables={d.tables} prefill={prefill} onDone={() => qc.invalidateQueries({ queryKey: ['mib-pack', id] })} />
      <TestAgainstDevice packId={id} />
    </div>
  )
}

function MappingEditor({ packId, tables, onDone, prefill }: { packId: string; tables: MibPackTable[]; onDone: () => void; prefill?: { table?: string; root?: string; purpose?: string } }) {
  const [tableName, setTableName] = useState(prefill?.table ?? '')
  const [rootOid, setRootOid] = useState(prefill?.root ?? '')
  const [purpose, setPurpose] = useState(prefill?.purpose && PURPOSES.includes(prefill.purpose) ? prefill.purpose : 'clients')
  const [colMap, setColMap] = useState('{}')
  const [err, setErr] = useState<string | null>(null)

  // Insert a field hint into the column_map JSON (placeholder column 0) so the
  // operator just edits the column number after clicking.
  const addField = (field: string) => {
    let m: Record<string, number> = {}
    try { m = JSON.parse(colMap || '{}') } catch { /* keep editing */ }
    if (!(field in m)) m[field] = 0
    setColMap(JSON.stringify(m))
  }

  const save = useMutation({
    mutationFn: () => {
      let column_map: Record<string, number>
      try { column_map = JSON.parse(colMap || '{}') } catch { throw new Error('column_map must be valid JSON, e.g. {"ap_name":2,"ap_mac":13}') }
      return api.post(`/mib-packs/${packId}/tables`, { table_name: tableName, root_oid: rootOid, purpose, column_map, enabled: true })
    },
    onSuccess: () => { setTableName(''); setRootOid(''); setColMap('{}'); setErr(null); onDone() },
    onError: (e) => setErr((e as Error).message),
  })

  const edit = (t: MibPackTable) => {
    setTableName(t.table_name); setRootOid(t.root_oid); setPurpose(t.purpose)
    setColMap(JSON.stringify(t.column_map ?? {}))
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <h4 style={{ marginBottom: 6 }}>Table → OID mappings</h4>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Each row maps a MIB table to its <strong>root OID</strong> + a <strong>purpose</strong> (which wireless roster
        it feeds). <code>column_map</code> binds a domain field to a column sub-ID; column <code>0</code> means
        "use the row index" (MAC fields render the index as a MAC). Edit a row to re-target tables a device exposes.
      </p>
      {tables.length > 0 && (
        <table style={{ marginBottom: 10 }}>
          <thead><tr><th>Table</th><th>Root OID</th><th>Purpose</th><th>Columns</th><th></th></tr></thead>
          <tbody>
            {tables.map((t) => (
              <tr key={t.id} style={t.enabled ? undefined : { opacity: 0.5 }}>
                <td>{t.table_name}</td>
                <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{t.root_oid}</td>
                <td><span className="badge badge-muted">{t.purpose}</span></td>
                <td style={{ fontFamily: 'monospace', fontSize: 11 }}>{JSON.stringify(t.column_map ?? {})}</td>
                <td><button style={ghost} onClick={() => edit(t)}>Edit</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div style={{ display: 'grid', gap: 8, gridTemplateColumns: '160px 1fr 130px', alignItems: 'start' }}>
        <input style={input} placeholder="table_name" value={tableName} onChange={(e) => setTableName(e.target.value)} />
        <input style={{ ...input, fontFamily: 'monospace' }} placeholder="root OID (1.3.6.1.4.1.5624.1.2.5.1.2)" value={rootOid} onChange={(e) => setRootOid(e.target.value)} />
        <select style={input} value={purpose} onChange={(e) => setPurpose(e.target.value)}>
          {PURPOSES.map((p) => <option key={p} value={p}>{p}</option>)}
        </select>
      </div>
      {(FIELD_HINTS[purpose] ?? []).length > 0 && (
        <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
          <span className="muted" style={{ fontSize: 11 }}>Fields for <strong>{purpose}</strong> (click to add):</span>
          {FIELD_HINTS[purpose].map((f) => (
            <button key={f} style={{ ...ghost, fontSize: 11, padding: '2px 8px' }} onClick={() => addField(f)}>+ {f}</button>
          ))}
        </div>
      )}
      <textarea style={{ ...input, marginTop: 8, fontFamily: 'monospace', minHeight: 54 }}
        placeholder='column_map e.g. {"client_mac":1,"client_ip":2,"client_ssid":6,"client_ap":12}'
        value={colMap} onChange={(e) => setColMap(e.target.value)} />
      <div style={{ marginTop: 8 }}>
        <button style={btn} disabled={!tableName || !rootOid || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? 'Saving…' : 'Save mapping'}
        </button>
        {err && <span className="error-msg" style={{ marginLeft: 12 }}>{err}</span>}
      </div>
    </div>
  )
}

function TestAgainstDevice({ packId }: { packId: string }) {
  const [ip, setIp] = useState('')
  const [community, setCommunity] = useState('')
  const [rootOid, setRootOid] = useState('')
  const [res, setRes] = useState<MibTestResult | null>(null)

  const run = useMutation({
    mutationFn: () => api.post<MibTestResult>(`/mib-packs/${packId}/test-device`, {
      ip: ip.trim(), community: community.trim() || undefined, root_oid: rootOid.trim() || undefined, max_rows: 25,
    }),
    onSuccess: (r) => setRes(r),
  })

  return (
    <div style={{ background: 'rgba(255,255,255,0.03)', borderRadius: 8, padding: 12 }}>
      <h4 style={{ marginBottom: 6 }}>Test against device</h4>
      <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Walk this pack's mapped tables against a live device and see which respond. Read-only — nothing is persisted to
        the inventory (raw rows are captured for preview). Provide the device IP and (if not already bound) an SNMP
        community.
      </p>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        <input style={{ ...input, width: 160 }} placeholder="Device IP" value={ip} onChange={(e) => setIp(e.target.value)} />
        <input style={{ ...input, width: 150 }} placeholder="SNMP community" value={community} onChange={(e) => setCommunity(e.target.value)} />
        <input style={{ ...input, width: 240, fontFamily: 'monospace' }} placeholder="ad-hoc root OID (optional)" value={rootOid} onChange={(e) => setRootOid(e.target.value)} />
        <button style={btn} disabled={!ip || run.isPending} onClick={() => { setRes(null); run.mutate() }}>
          {run.isPending ? 'Walking…' : 'Run test'}
        </button>
      </div>
      {run.error && <div className="error-msg" style={{ marginTop: 8 }}>{(run.error as Error).message}</div>}
      {res && (
        <div style={{ marginTop: 10 }}>
          <div style={{ marginBottom: 6 }}>
            {res.ok ? <span className="badge badge-success">{res.detail}</span> : <span className="badge badge-warning">{res.detail}</span>}
          </div>
          {res.results && res.results.length > 0 && (
            <table>
              <thead><tr><th>Table</th><th>Root OID</th><th>Purpose</th><th>Status</th><th>Rows</th><th>Sample (col→value)</th></tr></thead>
              <tbody>
                {res.results.map((t) => (
                  <tr key={t.table}>
                    <td>{t.table}</td>
                    <td style={{ fontFamily: 'monospace', fontSize: 11 }}>{t.root_oid}</td>
                    <td><span className="badge badge-muted">{t.purpose}</span></td>
                    <td>{statusBadge(t.status)}{t.detail && <div className="muted" style={{ fontSize: 10 }}>{t.detail}</div>}</td>
                    <td style={{ textAlign: 'center' }}>{t.count}</td>
                    <td style={{ fontFamily: 'monospace', fontSize: 10, maxWidth: 320, overflow: 'hidden' }}>
                      {(t.sample ?? []).slice(0, 3).map((s, i) => <div key={i}>{JSON.stringify(s)}</div>)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  )
}

// ============================ Legacy library ===============================

function LibraryTab() {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [content, setContent] = useState('')
  const [fileID, setFileID] = useState<string | null>(null)

  const files = useQuery({ queryKey: ['mibs'], queryFn: () => api.get<MibFile[]>('/mibs') })
  const objects = useQuery({
    queryKey: ['mib-objects', fileID],
    queryFn: () => api.get<MibObject[]>(`/mibs/${fileID}/objects`),
    enabled: !!fileID,
  })
  const mappings = useQuery({ queryKey: ['oid-mappings'], queryFn: () => api.get<OIDMapping[]>('/oid-mappings') })
  const pagedObjects = usePaged(objects.data ?? [], { pageSize: 10 })

  const upload = useMutation({
    mutationFn: () => api.post('/mibs', { name, content }),
    onSuccess: () => { setName(''); setContent(''); qc.invalidateQueries({ queryKey: ['mibs'] }) },
  })
  const delMapping = useMutation({
    mutationFn: (mid: string) => api.del(`/oid-mappings/${mid}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['oid-mappings'] }),
  })

  return (
    <div>
      <div className="card">
        <h3>Paste a MIB (legacy OID library)</h3>
        <p className="muted" style={{ marginBottom: 10 }}>
          The legacy library parses a MIB into a browsable OID list and lets you bind individual OIDs to metric keys.
          For SNMP-driven wireless collection use <strong>MIB Packs</strong> above instead.
        </p>
        <div style={{ display: 'grid', gap: 8, maxWidth: 720 }}>
          <input style={input} placeholder="MIB name (e.g. FORTINET-FORTIGATE-MIB)" value={name} onChange={(e) => setName(e.target.value)} />
          <textarea style={{ ...input, minHeight: 120, fontFamily: 'monospace' }} placeholder="Paste MIB text…" value={content} onChange={(e) => setContent(e.target.value)} />
          <div>
            <button style={btn} disabled={!name || !content || upload.isPending} onClick={() => upload.mutate()}>
              {upload.isPending ? 'Parsing…' : 'Upload & parse'}
            </button>
            {upload.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(upload.error as Error).message}</span>}
          </div>
        </div>
      </div>

      <div className="card">
        <h3>Uploaded MIBs</h3>
        {files.data && files.data.length === 0 && <div className="muted">No MIBs uploaded yet.</div>}
        {files.data && files.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Objects</th><th>Unresolved</th><th>Uploaded</th><th></th></tr></thead>
            <tbody>
              {files.data.map((f) => (
                <tr key={f.id}>
                  <td><strong>{f.name}</strong></td>
                  <td>{f.object_count}</td>
                  <td>{f.unresolved > 0 ? <span className="badge badge-warning">{f.unresolved}</span> : '0'}</td>
                  <td>{f.uploaded_at?.slice(0, 10)}</td>
                  <td><button style={ghost} onClick={() => setFileID(f.id)}>View OIDs</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {fileID && (
        <div className="card">
          <h3>OID objects</h3>
          {objects.data && objects.data.length > 0 && (
            <table>
              <thead><tr><th>Name</th><th>OID</th><th>Syntax</th><th>Kind</th></tr></thead>
              <tbody>
                {pagedObjects.slice.map((o) => (
                  <tr key={o.id} style={o.unresolved ? { opacity: 0.6 } : undefined}>
                    <td>{o.name}{o.unresolved && <span className="badge badge-warning" style={{ marginLeft: 6 }}>unresolved</span>}</td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{o.oid}</td>
                    <td>{o.syntax ?? '—'}</td>
                    <td>{o.kind}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {objects.data && objects.data.length > 0 && <Pager page={pagedObjects.page} pages={pagedObjects.pages} total={pagedObjects.total} pageSize={pagedObjects.pageSize} onPage={pagedObjects.setPage} />}
        </div>
      )}

      <div className="card">
        <h3>OID mappings</h3>
        <MappingForm onDone={() => qc.invalidateQueries({ queryKey: ['oid-mappings'] })} />
        {mappings.data && mappings.data.length > 0 && (
          <table>
            <thead><tr><th>OID</th><th>Label</th><th>Metric</th><th>Vendor</th><th>Template</th><th></th></tr></thead>
            <tbody>
              {mappings.data.map((m) => (
                <tr key={m.id}>
                  <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{m.oid}</td>
                  <td>{m.label}</td>
                  <td>{m.metric_key ?? '—'}</td>
                  <td>{m.vendor ?? '—'}</td>
                  <td>{m.template ?? '—'}</td>
                  <td><button style={ghost} onClick={() => delMapping.mutate(m.id)}>Delete</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

function MappingForm({ onDone }: { onDone: () => void }) {
  const [oid, setOid] = useState('')
  const [label, setLabel] = useState('')
  const [metricKey, setMetricKey] = useState('')
  const [vendor, setVendor] = useState('')
  const m = useMutation({
    mutationFn: () => api.post<OIDMapping>('/oid-mappings', {
      oid, label, metric_key: metricKey || null, vendor: vendor || null,
    }),
    onSuccess: () => { setOid(''); setLabel(''); setMetricKey(''); setVendor(''); onDone() },
  })
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 12 }}>
      <input style={{ ...input, width: 220 }} placeholder="OID (1.3.6.1.4.1.…)" value={oid} onChange={(e) => setOid(e.target.value)} />
      <input style={{ ...input, width: 160 }} placeholder="Label" value={label} onChange={(e) => setLabel(e.target.value)} />
      <input style={{ ...input, width: 160 }} placeholder="metric_key" value={metricKey} onChange={(e) => setMetricKey(e.target.value)} />
      <input style={{ ...input, width: 120 }} placeholder="vendor" value={vendor} onChange={(e) => setVendor(e.target.value)} />
      <button style={btn} disabled={!oid || !label || m.isPending} onClick={() => m.mutate()}>Bind</button>
    </div>
  )
}

// Re-export MibWalkRow type usage placeholder to keep import meaningful in future raw-row views.
export type { MibWalkRow }
