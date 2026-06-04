import { Fragment, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { ShieldCheck, Users as UsersIcon, KeyRound, Plus } from 'lucide-react'
import { api, type AppUser, type Role, type Permission } from '../api'
import { PageHeader, Panel, TabBar, EmptyState, timeAgo } from '../components/ui'

type Tab = 'users' | 'roles' | 'permissions'
const TABS: Tab[] = ['users', 'roles', 'permissions']

export function AccessControl() {
  const { tab: param } = useParams<{ tab: string }>()
  const [tab, setTab] = useState<Tab>(TABS.includes(param as Tab) ? (param as Tab) : 'users')
  return (
    <div>
      <PageHeader title="Roles & Permissions" icon={ShieldCheck} subtitle="User access management — accounts, roles and the permissions they grant" />
      <TabBar
        tabs={[{ key: 'users', label: 'Users', icon: UsersIcon }, { key: 'roles', label: 'Roles', icon: ShieldCheck }, { key: 'permissions', label: 'Permissions', icon: KeyRound }]}
        active={tab} onChange={(k) => setTab(k as Tab)}
      />
      {tab === 'users' && <UsersTab />}
      {tab === 'roles' && <RolesTab />}
      {tab === 'permissions' && <PermissionsTab />}
    </div>
  )
}

function UsersTab() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['rbac-users'], queryFn: () => api.get<AppUser[]>('/rbac/users') })
  const roles = useQuery({ queryKey: ['rbac-roles'], queryFn: () => api.get<Role[]>('/rbac/roles') })
  const inv = () => qc.invalidateQueries({ queryKey: ['rbac-users'] })
  const [form, setForm] = useState({ username: '', full_name: '', email: '' })
  const [editRoles, setEditRoles] = useState<string | null>(null)

  const create = useMutation({ mutationFn: () => api.post('/rbac/users', form), onSuccess: () => { setForm({ username: '', full_name: '', email: '' }); inv() } })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/rbac/users/${id}`), onSuccess: inv })
  const toggle = useMutation({ mutationFn: (u: AppUser) => api.patch(`/rbac/users/${u.id}`, { full_name: u.full_name, email: u.email, is_active: !u.is_active }), onSuccess: inv })

  const rows = q.data ?? []
  return (
    <>
      <Panel title="New User" icon={Plus}>
        <div className="row">
          <input className="field" placeholder="username" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
          <input className="field" placeholder="full name" value={form.full_name} onChange={(e) => setForm({ ...form, full_name: e.target.value })} />
          <input className="field" placeholder="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
          <button className="btn btn-primary" disabled={!form.username || create.isPending} onClick={() => create.mutate()}>Add user</button>
        </div>
      </Panel>
      <Panel title="Users" icon={UsersIcon} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={UsersIcon} title="No users yet" message="Add operator accounts and assign them roles." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Username</th><th>Name</th><th>Email</th><th>Status</th><th>Created</th><th></th></tr></thead>
            <tbody>
              {rows.map((u) => (
                <Fragment key={u.id}>
                  <tr>
                    <td className="cell-name">{u.username}</td>
                    <td>{u.full_name || '—'}</td>
                    <td>{u.email || '—'}</td>
                    <td>{u.is_active ? <span className="badge badge-up">active</span> : <span className="badge badge-disabled">disabled</span>}</td>
                    <td className="muted">{timeAgo(u.created_at)}</td>
                    <td className="cell-actions">
                      <button className="btn btn-ghost btn-xs" onClick={() => setEditRoles(editRoles === u.id ? null : u.id)}>Roles</button>
                      <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(u)}>{u.is_active ? 'Disable' : 'Enable'}</button>
                      <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(u.id)}>Delete</button>
                    </td>
                  </tr>
                  {editRoles === u.id && (
                    <tr><td colSpan={6} style={{ background: 'var(--surface-2)' }}><AssignEditor userId={u.id} allRoles={roles.data ?? []} onDone={() => setEditRoles(null)} /></td></tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  )
}

function AssignEditor({ userId, allRoles, onDone }: { userId: string; allRoles: Role[]; onDone: () => void }) {
  const current = useQuery({ queryKey: ['user-roles', userId], queryFn: () => api.get<Role[]>(`/rbac/users/${userId}/roles`) })
  const [sel, setSel] = useState<Set<string> | null>(null)
  const chosen = sel ?? new Set((current.data ?? []).map((r) => r.id))
  const save = useMutation({ mutationFn: () => api.post(`/rbac/users/${userId}/roles`, { role_ids: [...chosen] }), onSuccess: onDone })
  if (current.isLoading) return <div className="loading">Loading roles…</div>
  return (
    <div style={{ padding: 12 }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>Assigned roles</div>
      <div className="row">
        {allRoles.length === 0 && <span className="muted">No roles defined — create roles first.</span>}
        {allRoles.map((r) => (
          <label key={r.id} className="seg-chip" style={{ cursor: 'pointer' }}>
            <input type="checkbox" checked={chosen.has(r.id)} onChange={(e) => { const n = new Set(chosen); if (e.target.checked) n.add(r.id); else n.delete(r.id); setSel(n) }} /> {r.name}
          </label>
        ))}
      </div>
      <button className="btn btn-primary btn-sm" style={{ marginTop: 10 }} disabled={save.isPending} onClick={() => save.mutate()}>Save assignments</button>
    </div>
  )
}

function RolesTab() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['rbac-roles'], queryFn: () => api.get<Role[]>('/rbac/roles') })
  const perms = useQuery({ queryKey: ['rbac-permissions'], queryFn: () => api.get<Permission[]>('/rbac/permissions') })
  const inv = () => qc.invalidateQueries({ queryKey: ['rbac-roles'] })
  const [form, setForm] = useState({ name: '', description: '' })
  const [editPerms, setEditPerms] = useState<string | null>(null)
  const create = useMutation({ mutationFn: () => api.post('/rbac/roles', form), onSuccess: () => { setForm({ name: '', description: '' }); inv() } })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/rbac/roles/${id}`), onSuccess: inv })
  const rows = q.data ?? []
  return (
    <>
      <Panel title="New Role" icon={Plus}>
        <div className="row">
          <input className="field" placeholder="role name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <input className="field" style={{ flex: 1 }} placeholder="description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <button className="btn btn-primary" disabled={!form.name || create.isPending} onClick={() => create.mutate()}>Add role</button>
        </div>
      </Panel>
      <Panel title="Roles" icon={ShieldCheck} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={ShieldCheck} title="No roles yet" message="Define roles, then attach permissions and assign to users." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Role</th><th>Description</th><th></th></tr></thead>
            <tbody>
              {rows.map((r) => (
                <Fragment key={r.id}>
                  <tr>
                    <td className="cell-name">{r.name}</td>
                    <td>{r.description || '—'}</td>
                    <td className="cell-actions">
                      <button className="btn btn-ghost btn-xs" onClick={() => setEditPerms(editPerms === r.id ? null : r.id)}>Permissions</button>
                      <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(r.id)}>Delete</button>
                    </td>
                  </tr>
                  {editPerms === r.id && (
                    <tr><td colSpan={3} style={{ background: 'var(--surface-2)' }}><PermEditor roleId={r.id} allPerms={perms.data ?? []} onDone={() => setEditPerms(null)} /></td></tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  )
}

function PermEditor({ roleId, allPerms, onDone }: { roleId: string; allPerms: Permission[]; onDone: () => void }) {
  const current = useQuery({ queryKey: ['role-perms', roleId], queryFn: () => api.get<Permission[]>(`/rbac/roles/${roleId}/permissions`) })
  const [sel, setSel] = useState<Set<string> | null>(null)
  const chosen = sel ?? new Set((current.data ?? []).map((p) => p.id))
  const save = useMutation({ mutationFn: () => api.post(`/rbac/roles/${roleId}/permissions`, { permission_ids: [...chosen] }), onSuccess: onDone })
  if (current.isLoading) return <div className="loading">Loading permissions…</div>
  return (
    <div style={{ padding: 12 }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>Granted permissions</div>
      <div className="row">
        {allPerms.length === 0 && <span className="muted">No permissions defined — add them under the Permissions tab.</span>}
        {allPerms.map((p) => (
          <label key={p.id} className="seg-chip" style={{ cursor: 'pointer' }}>
            <input type="checkbox" checked={chosen.has(p.id)} onChange={(e) => { const n = new Set(chosen); if (e.target.checked) n.add(p.id); else n.delete(p.id); setSel(n) }} /> {p.code}
          </label>
        ))}
      </div>
      <button className="btn btn-primary btn-sm" style={{ marginTop: 10 }} disabled={save.isPending} onClick={() => save.mutate()}>Save permissions</button>
    </div>
  )
}

function PermissionsTab() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['rbac-permissions'], queryFn: () => api.get<Permission[]>('/rbac/permissions') })
  const inv = () => qc.invalidateQueries({ queryKey: ['rbac-permissions'] })
  const [form, setForm] = useState({ code: '', description: '' })
  const create = useMutation({ mutationFn: () => api.post('/rbac/permissions', form), onSuccess: () => { setForm({ code: '', description: '' }); inv() } })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/rbac/permissions/${id}`), onSuccess: inv })
  const rows = q.data ?? []
  return (
    <>
      <Panel title="New Permission" icon={Plus}>
        <div className="row">
          <input className="field" placeholder="code (e.g. devices.write)" value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value })} />
          <input className="field" style={{ flex: 1 }} placeholder="description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <button className="btn btn-primary" disabled={!form.code || create.isPending} onClick={() => create.mutate()}>Add permission</button>
        </div>
      </Panel>
      <Panel title="Permissions" icon={KeyRound} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={KeyRound} title="No permissions yet" message="Define the capability codes your roles will grant (e.g. devices.read, discovery.run)." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Code</th><th>Description</th><th></th></tr></thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id}>
                  <td className="mono">{p.code}</td>
                  <td>{p.description || '—'}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(p.id)}>Delete</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  )
}
