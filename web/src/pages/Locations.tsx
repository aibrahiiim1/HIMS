import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Location } from '../api'

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

export function Locations() {
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const refresh = () => qc.invalidateQueries({ queryKey: ['locations-all'] })

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
              <button style={ghost} onClick={() => { setEditId(loc.id); setEditName(loc.name) }}>rename</button>
              <button style={{ ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }} onClick={() => { if (confirm(`Delete "${loc.name}" and everything under it?`)) del.mutate(loc.id) }}>delete</button>
            </>
          )}
        </div>
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
