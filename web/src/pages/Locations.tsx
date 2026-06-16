import { useMemo, useState } from 'react'
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Credential, type Location, type Subnet, type SubnetCredCount, type SubnetCredential } from '../api'

const KINDS = ['group', 'hotel', 'building', 'floor', 'area', 'room', 'rack', 'office']
// Suggested child kind for a given parent kind (operator can override).
const NEXT_KIND: Record<string, string> = {
  group: 'hotel', hotel: 'building', building: 'floor', floor: 'room',
  area: 'room', room: 'rack', rack: 'office', office: 'office',
}
const KIND_COLOR: Record<string, string> = {
  group: '#7e57c2', hotel: '#1565c0', building: '#00838f', floor: '#2e7d32',
  area: '#558b2f', room: '#ef6c00', rack: '#c62828', office: '#5d4037',
}

const input: React.CSSProperties = { padding: '5px 8px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13 }
const btn: React.CSSProperties = { padding: '4px 10px', background: '#1565c0', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 12, fontWeight: 600 }
const ghost: React.CSSProperties = { padding: '3px 8px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 11 }

// SubnetCredEditor — the per-subnet credential multi-select. Optional + clearable:
// with credentials assigned, scans of IPs in this subnet try ONLY these (no global
// spray / lockout risk); cleared ⇒ the subnet reverts to default credential
// resolution. Fetches the subnet's current set on open; saves the whole set via PUT.
function SubnetCredEditor({ subnetId, cidr, allCreds, onClose, onSaved }: {
  subnetId: string; cidr: string; allCreds: Credential[]; onClose: () => void; onSaved: () => void
}) {
  const current = useQuery({ queryKey: ['subnet-creds', subnetId], queryFn: () => api.get<SubnetCredential[]>(`/subnets/${subnetId}/credentials`) })
  const [sel, setSel] = useState<Set<string> | null>(null)
  const selected = sel ?? new Set((current.data ?? []).map((c) => c.id))
  const toggle = (id: string) => { const n = new Set(selected); if (n.has(id)) n.delete(id); else n.add(id); setSel(n) }
  const save = useMutation({
    mutationFn: () => api.put(`/subnets/${subnetId}/credentials`, { credential_ids: [...selected] }),
    onSuccess: () => { onSaved(); onClose() },
  })
  return (
    <div style={{ margin: '4px 0 8px 24px', padding: 10, border: '1px solid #455a64', borderRadius: 8, maxWidth: 560 }}>
      <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>
        Credentials for <span style={{ fontFamily: 'monospace' }}>{cidr}</span>
        <span className="muted" style={{ fontWeight: 400 }}> — scans of this subnet try only the checked credentials. Leave none checked to use the global/default behavior.</span>
      </div>
      {current.isLoading && <div className="loading">Loading…</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3, maxHeight: 220, overflowY: 'auto' }}>
        {(allCreds ?? []).map((c) => (
          <label key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, cursor: 'pointer' }}>
            <input type="checkbox" checked={selected.has(c.id)} onChange={() => toggle(c.id)} />
            <span>{c.name}</span>
            <span style={{ background: '#37474f', color: '#b0bec5', fontSize: 10, padding: '0 6px', borderRadius: 8 }}>{c.kind}</span>
            {c.weak && <span style={{ color: '#ffb74d', fontSize: 10 }}>weak</span>}
          </label>
        ))}
        {(allCreds ?? []).length === 0 && <div className="muted" style={{ fontSize: 12 }}>No credentials defined yet — add some on the Credentials page first.</div>}
      </div>
      <div style={{ display: 'flex', gap: 6, marginTop: 8, alignItems: 'center' }}>
        <button style={editorBtn} disabled={save.isPending} onClick={() => save.mutate()}>Save ({selected.size})</button>
        <button style={editorGhost} onClick={() => setSel(new Set())}>Clear all</button>
        <button style={editorGhost} onClick={onClose}>Cancel</button>
        {save.error && <span className="error-msg" style={{ fontSize: 12 }}>{(save.error as Error).message}</span>}
      </div>
    </div>
  )
}
const editorBtn: React.CSSProperties = { padding: '4px 12px', background: '#1565c0', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 12, fontWeight: 600 }
const editorGhost: React.CSSProperties = { padding: '3px 10px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 11 }

export function Locations() {
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const subnets = useQuery({ queryKey: ['subnets-all'], queryFn: () => api.get<Subnet[]>('/subnets') })
  const refresh = () => { qc.invalidateQueries({ queryKey: ['locations-all'] }); qc.invalidateQueries({ queryKey: ['subnets-all'] }) }
  const subnetsOf = useMemo(() => {
    const m: Record<string, Subnet[]> = {}
    for (const s of subnets.data ?? []) {
      ;(m[s.location_id] ??= []).push(s)
    }
    return m
  }, [subnets.data])

  const [subParent, setSubParent] = useState<string | null>(null)
  const [subCidr, setSubCidr] = useState('')
  const [subMsg, setSubMsg] = useState('') // inline error for the add-subnet form
  const addSubnet = useMutation({
    mutationFn: (locId: string) => api.post(`/locations/${locId}/subnets`, { cidr: subCidr.trim() }),
    onSuccess: () => { setSubCidr(''); setSubMsg(''); setSubParent(null); refresh() },
    onError: (e) => setSubMsg((e as Error).message), // surface the server reason instead of a silent 400
  })
  // Validate the CIDR client-side (must include a prefix length) so the common
  // "172.21.96.0" (no /24) mistake gets an instant, friendly hint, not a silent 400.
  const submitSubnet = (locId: string) => {
    const v = subCidr.trim()
    setSubMsg('')
    if (!v.includes(':') && !/^\d{1,3}(\.\d{1,3}){3}\/([0-9]|[12]\d|3[0-2])$/.test(v)) {
      setSubMsg('Enter a network in CIDR form with a prefix length — e.g. 172.21.96.0/24')
      return
    }
    addSubnet.mutate(locId)
  }
  const delSubnet = useMutation({ mutationFn: (id: string) => api.del(`/subnets/${id}`), onSuccess: refresh })

  // --- Subnet-scoped credentials ---
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  // Per-subnet credential count + kinds (one query per location that has subnets),
  // so each subnet row can badge its scope without opening the editor.
  const locIdsWithSubnets = useMemo(() => Object.keys(subnetsOf), [subnetsOf])
  const countQueries = useQueries({
    queries: locIdsWithSubnets.map((locId) => ({
      queryKey: ['subnet-cred-counts', locId],
      queryFn: () => api.get<SubnetCredCount[]>(`/locations/${locId}/subnet-credential-counts`),
    })),
  })
  const countBySubnet = useMemo(() => {
    const m: Record<string, SubnetCredCount> = {}
    for (const q of countQueries) for (const c of (q.data ?? [])) m[c.subnet_id] = c
    return m
  }, [countQueries])
  const refreshCounts = (locId: string) => qc.invalidateQueries({ queryKey: ['subnet-cred-counts', locId] })
  const [credEditSubnet, setCredEditSubnet] = useState<string | null>(null)

  const [addParent, setAddParent] = useState<string | 'root' | null>(null)
  const [addKind, setAddKind] = useState('group')
  const [addName, setAddName] = useState('')
  const [editId, setEditId] = useState<string | null>(null)
  const [editName, setEditName] = useState('')

  const childrenOf = useMemo(() => {
    const m: Record<string, Location[]> = {}
    for (const l of data ?? []) {
      const k = l.parent_id ?? 'root'
      ;(m[k] ??= []).push(l)
    }
    for (const k of Object.keys(m)) m[k].sort((a, b) => a.name.localeCompare(b.name))
    return m
  }, [data])

  const create = useMutation({
    mutationFn: (b: { parent_id: string | null; kind: string; name: string }) => api.post('/locations', b),
    onSuccess: () => { setAddParent(null); setAddName(''); refresh() },
  })
  const rename = useMutation({
    mutationFn: (b: { id: string; name: string }) => api.patch(`/locations/${b.id}`, { name: b.name }),
    onSuccess: () => { setEditId(null); refresh() },
  })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/locations/${id}`), onSuccess: refresh })

  const openAdd = (parent: string | 'root', parentKind?: string) => {
    setAddParent(parent); setAddName(''); setAddKind(parent === 'root' ? 'group' : NEXT_KIND[parentKind ?? 'group'] ?? 'hotel')
  }

  const AddForm = ({ parent }: { parent: string | 'root' }) => (
    <div style={{ display: 'flex', gap: 6, alignItems: 'center', margin: '4px 0 4px 24px' }}>
      <select style={input} value={addKind} onChange={(e) => setAddKind(e.target.value)}>
        {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
      </select>
      <input style={{ ...input, width: 160 }} placeholder="name" value={addName} autoFocus onChange={(e) => setAddName(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && addName.trim()) create.mutate({ parent_id: parent === 'root' ? null : parent, kind: addKind, name: addName.trim() }) }} />
      <button style={btn} disabled={!addName.trim() || create.isPending} onClick={() => create.mutate({ parent_id: parent === 'root' ? null : parent, kind: addKind, name: addName.trim() })}>Add</button>
      <button style={ghost} onClick={() => setAddParent(null)}>Cancel</button>
    </div>
  )

  const Node = ({ loc, depth }: { loc: Location; depth: number }) => {
    const kids = childrenOf[loc.id] ?? []
    return (
      <div style={{ marginLeft: depth === 0 ? 0 : 20 }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0' }}>
          <span style={{ background: KIND_COLOR[loc.kind] ?? '#555', color: '#fff', fontSize: 10, padding: '1px 6px', borderRadius: 8, textTransform: 'uppercase' }}>{loc.kind}</span>
          {editId === loc.id ? (
            <>
              <input style={{ ...input, width: 160 }} value={editName} autoFocus onChange={(e) => setEditName(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && editName.trim()) rename.mutate({ id: loc.id, name: editName.trim() }) }} />
              <button style={btn} disabled={!editName.trim()} onClick={() => rename.mutate({ id: loc.id, name: editName.trim() })}>Save</button>
              <button style={ghost} onClick={() => setEditId(null)}>Cancel</button>
            </>
          ) : (
            <>
              <strong>{loc.name}</strong>
              {loc.code && <span className="muted" style={{ fontSize: 11 }}>[{loc.code}]</span>}
              {kids.length > 0 && <span className="muted" style={{ fontSize: 11 }}>· {kids.length}</span>}
              <button style={ghost} onClick={() => openAdd(loc.id, loc.kind)}>+ child</button>
              <button style={ghost} onClick={() => { setSubParent(subParent === loc.id ? null : loc.id); setSubCidr(''); setSubMsg('') }}>+ subnet</button>
              <button style={ghost} onClick={() => { setEditId(loc.id); setEditName(loc.name) }}>rename</button>
              <button style={{ ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }} onClick={() => { if (confirm(`Delete "${loc.name}" and everything under it?`)) del.mutate(loc.id) }}>delete</button>
            </>
          )}
        </div>
        {/* Subnets attached to this node — feed By-Site scan + credential scope */}
        {(subnetsOf[loc.id] ?? []).length > 0 && (
          <div style={{ marginLeft: 24, display: 'flex', flexDirection: 'column', gap: 4, padding: '2px 0' }}>
            {(subnetsOf[loc.id] ?? []).map((s) => {
              const cc = countBySubnet[s.id]
              const scoped = (cc?.count ?? 0) > 0
              return (
                <div key={s.id} style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 6 }}>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '1px 8px', border: '1px solid #2e7d32', borderRadius: 10, fontSize: 11, fontFamily: 'monospace' }}>
                    🌐 {s.cidr}
                    <button onClick={() => delSubnet.mutate(s.id)} title="remove subnet" style={{ background: 'none', border: 'none', color: '#ef9a9a', cursor: 'pointer', fontSize: 13, lineHeight: 1 }}>×</button>
                  </span>
                  {scoped ? (
                    <span title={`Scans of this subnet try ONLY these ${cc!.count} credential(s)`} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '1px 8px', background: '#1b3a4b', border: '1px solid #4fc3f7', borderRadius: 10, fontSize: 10, color: '#9fdcff' }}>
                      🔒 {cc!.count} scoped{cc!.kinds.length > 0 ? ` · ${cc!.kinds.join(', ')}` : ''}
                    </span>
                  ) : (
                    <span title="No subnet credentials assigned — scans use the global/default credential set" style={{ padding: '1px 8px', background: '#3a2f1b', border: '1px solid #ffb74d', borderRadius: 10, fontSize: 10, color: '#ffd699' }}>
                      ⚠ uses global creds
                    </span>
                  )}
                  <button style={ghost} onClick={() => setCredEditSubnet(credEditSubnet === s.id ? null : s.id)}>{scoped ? 'edit creds' : 'set creds'}</button>
                  {credEditSubnet === s.id && (
                    <div style={{ flexBasis: '100%' }}>
                      <SubnetCredEditor
                        subnetId={s.id} cidr={s.cidr} allCreds={creds.data ?? []}
                        onClose={() => setCredEditSubnet(null)}
                        onSaved={() => refreshCounts(loc.id)}
                      />
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
        {subParent === loc.id && (
          <div style={{ margin: '4px 0 4px 24px' }}>
            <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <input style={{ ...input, width: 180 }} placeholder="CIDR e.g. 172.21.96.0/24" value={subCidr} autoFocus onChange={(e) => { setSubCidr(e.target.value); if (subMsg) setSubMsg('') }} onKeyDown={(e) => { if (e.key === 'Enter' && subCidr.trim()) submitSubnet(loc.id) }} />
              <button style={btn} disabled={!subCidr.trim() || addSubnet.isPending} onClick={() => submitSubnet(loc.id)}>Add subnet</button>
              <button style={ghost} onClick={() => { setSubParent(null); setSubMsg('') }}>Cancel</button>
            </div>
            {subMsg && <div style={{ color: '#ef9a9a', fontSize: 12, marginTop: 4 }}>{subMsg}</div>}
          </div>
        )}
        {addParent === loc.id && <AddForm parent={loc.id} />}
        {kids.map((k) => <Node key={k.id} loc={k} depth={depth + 1} />)}
      </div>
    )
  }

  const roots = childrenOf['root'] ?? []

  return (
    <div>
      <div className="card">
        <h2>Locations <span className="muted" style={{ fontSize: 13, fontWeight: 400 }}>— Hotel Group → Hotel → Building → Floor / Area / Room / Rack / Office</span></h2>
        <p className="muted" style={{ fontSize: 12 }}>Build the site hierarchy here, then place devices into it from Inventory. Deleting a node removes its whole subtree; devices anchored to a deleted node are simply un-anchored (not deleted).</p>
        <button style={btn} onClick={() => openAdd('root')}>+ Add root (group / hotel)</button>
        {addParent === 'root' && <AddForm parent="root" />}
        {(create.error || rename.error || del.error) && <div className="error-msg" style={{ marginTop: 6 }}>{((create.error || rename.error || del.error) as Error).message}</div>}
      </div>

      <div className="card">
        {isLoading && <div className="loading">Loading…</div>}
        {error && <div className="error-msg">{(error as Error).message}</div>}
        {data && roots.length === 0 && <div className="muted">No locations yet. Add a root group or hotel to start.</div>}
        {roots.map((r) => <Node key={r.id} loc={r} depth={0} />)}
      </div>
    </div>
  )
}
