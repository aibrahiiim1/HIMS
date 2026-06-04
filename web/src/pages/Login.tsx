import { useState } from 'react'
import { ShieldCheck, LogIn } from 'lucide-react'
import { api, type AuthMe } from '../api'

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      await api.post<AuthMe>('/auth/login', { username, password })
      onSuccess()
    } catch (e) {
      setErr((e as Error).message || 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-screen">
      <form className="login-card" onSubmit={submit}>
        <div className="login-brand"><ShieldCheck size={26} /> <span>HIMS</span></div>
        <div className="login-sub">Hotel Infrastructure Management System</div>
        <label className="form-field">Username
          <input className="field" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        </label>
        <label className="form-field">Password
          <input className="field" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
        </label>
        {err && <div className="enc-banner crit" style={{ marginTop: 4 }}>{err}</div>}
        <button className="btn btn-primary" type="submit" disabled={busy || !username || !password} style={{ marginTop: 6, justifyContent: 'center' }}>
          <LogIn size={15} /> {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
