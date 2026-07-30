import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { UserCircle, KeyRound, Eye, EyeOff, Check, ShieldCheck } from 'lucide-react'
import { api, type AuthMe } from '../api'
import { PageHeader, Panel, DefList } from '../components/ui'
import { passwordProblem, strengthOf } from '../lib/password'

// Account is the signed-in user's own page: who they are, and the only place
// they can rotate their own password (the admin reset under Roles & Permissions
// is a different, privileged path that does not need the current password).
export function Account() {
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me') })
  return (
    <div>
      <PageHeader title="My Account" icon={UserCircle} subtitle="Your identity, permissions, and password" />
      <Panel title="Signed in as" icon={ShieldCheck}>
        <DefList items={[
          { label: 'Username', value: me.data?.username ?? '—' },
          { label: 'Role', value: me.data?.admin ? 'Administrator (full access)' : 'Operator' },
          { label: 'Site scope', value: me.data?.site_id ? me.data.site_id : 'All sites (global)' },
          { label: 'Permissions', value: `${me.data?.permissions?.length ?? 0} granted` },
        ]} />
      </Panel>
      <ChangePasswordPanel />
    </div>
  )
}

function ChangePasswordPanel() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [show, setShow] = useState(false)
  const [done, setDone] = useState(false)

  const problem = next ? passwordProblem(next) : ''
  const mismatch = confirm.length > 0 && next !== confirm
  const same = !!next && next === current
  const strength = strengthOf(next)
  const ready = !!current && !!next && !problem && !same && next === confirm

  const change = useMutation({
    mutationFn: () => api.post('/auth/password', { current_password: current, new_password: next }),
    onSuccess: () => { setDone(true); setCurrent(''); setNext(''); setConfirm(''); setShow(false) },
  })

  return (
    <Panel title="Change Password" icon={KeyRound} subtitle="Rotates your own password; other sessions are signed out">
      {done && (
        <div className="row" style={{ gap: 8, alignItems: 'center', marginBottom: 10 }}>
          <Check size={16} style={{ color: 'var(--ok, #16a34a)' }} />
          <span>Password changed. Your other sessions were signed out; this one stays active.</span>
        </div>
      )}
      <div className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
        <input
          className="field" type="password" placeholder="current password" autoComplete="current-password"
          value={current} onChange={(e) => { setCurrent(e.target.value); setDone(false) }} style={{ minWidth: 220 }}
        />
      </div>
      <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        <input
          className="field" type={show ? 'text' : 'password'} placeholder="new password" autoComplete="new-password"
          value={next} onChange={(e) => { setNext(e.target.value); setDone(false) }} style={{ minWidth: 220 }}
        />
        <button className="btn btn-sm" type="button" title={show ? 'Hide' : 'Show'} onClick={() => setShow((v) => !v)}>
          {show ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
        {next && <span className="muted" style={{ fontSize: 12 }}>{strength.label}</span>}
      </div>
      <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
        <input
          className="field" type={show ? 'text' : 'password'} placeholder="confirm new password" autoComplete="new-password"
          value={confirm} onChange={(e) => { setConfirm(e.target.value); setDone(false) }} style={{ minWidth: 220 }}
        />
      </div>
      {problem && <div style={{ color: 'var(--crit)', fontSize: 12, marginTop: 6 }}>{problem}</div>}
      {same && <div style={{ color: 'var(--crit)', fontSize: 12, marginTop: 6 }}>New password must be different from the current one.</div>}
      {mismatch && <div style={{ color: 'var(--crit)', fontSize: 12, marginTop: 6 }}>Passwords do not match.</div>}
      {change.isError && <div style={{ color: 'var(--crit)', fontSize: 12, marginTop: 6 }}>{(change.error as Error).message}</div>}
      <button className="btn btn-primary btn-sm" style={{ marginTop: 10 }} disabled={!ready || change.isPending} onClick={() => change.mutate()}>
        {change.isPending ? 'Changing…' : 'Change password'}
      </button>
    </Panel>
  )
}
