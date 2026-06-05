import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type MibFile, type MibObject, type OIDMapping } from '../api'
import { usePaged, Pager } from '../components/ui'

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 10px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

export function Mibs() {
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
    mutationFn: (id: string) => api.del(`/oid-mappings/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['oid-mappings'] }),
  })

  return (
    <div>
      <div className="card">
        <h2>MIB Library</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Upload a MIB → it's parsed into an OID library. Bind an OID to a metric/template below so
          the vendor's MIB becomes usable monitoring vocabulary. (Upload alone isn't understanding —
          the value is the mapping.)
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
