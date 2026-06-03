import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Credential } from '../api'

const KINDS = ['snmp_v2c', 'snmp_v3', 'ssh', 'winrm', 'http_basic', 'onvif', 'vendor_api', 'ldap']

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}

export function Credentials() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const list = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  return (
    <div>
      <div className="card">
        <h2>Credentials</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Secrets are encrypted at rest (AES-256-GCM) the moment they're saved. The plaintext is
          never stored, logged, or returned — only this metadata (name, kind, weak flag) is ever
          shown. Credentials are resolved to devices by scope (site → subnet → group) and bound on
          first successful auth.
        </p>
        <button style={btn} onClick={() => setShow((v) => !v)}>{show ? 'Cancel' : '+ New credential'}</button>
      </div>

      {show && <CreateForm onDone={() => { setShow(false); qc.invalidateQueries({ queryKey: ['credentials'] }) }} />}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.error && <div className="error-msg">{(list.error as Error).message}</div>}
        {list.data && list.data.length === 0 && <div className="muted">No credentials yet.</div>}
        {list.data && list.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Kind</th><th>Weak</th><th>Created</th></tr></thead>
            <tbody>
              {list.data.map((c) => (
                <tr key={c.id}>
                  <td><strong>{c.name}</strong></td>
                  <td>{c.kind}</td>
                  <td>{c.weak ? <span className="badge badge-warning">weak</span> : '—'}</td>
                  <td>{c.created_at?.slice(0, 10)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

function CreateForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [kind, setKind] = useState('snmp_v2c')
  const [secret, setSecret] = useState('')
  // SNMP v3 (USM) fields — assembled into the secret JSON the server seals.
  const [secName, setSecName] = useState('')
  const [authProto, setAuthProto] = useState('SHA')
  const [authKey, setAuthKey] = useState('')
  const [privProto, setPrivProto] = useState('AES')
  const [privKey, setPrivKey] = useState('')

  const isV3 = kind === 'snmp_v3'
  // http/winrm/onvif/vendor creds use "username:password"; surface a hint.
  const userPass = kind === 'ssh' || kind === 'winrm' || kind === 'http_basic' || kind === 'onvif' || kind === 'vendor_api'

  const buildSecret = (): string => {
    if (isV3) {
      return JSON.stringify({
        security_name: secName, auth_protocol: authKey ? authProto : '', auth_key: authKey,
        priv_protocol: privKey ? privProto : '', priv_key: privKey,
      })
    }
    return secret
  }
  const valid = name && (isV3 ? secName : secret)
  const m = useMutation({
    mutationFn: () => api.post<Credential>('/credentials', { name, kind, secret: buildSecret() }),
    onSuccess: onDone,
  })
  return (
    <div className="card">
      <h2>New credential</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 10 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>Kind
          <select style={input} value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
        </label>
        {!isV3 && (
          <label>{userPass ? 'username:password' : kind.startsWith('snmp') ? 'Community' : 'Secret'}
            <input style={input} type={userPass ? 'text' : 'password'} value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" />
          </label>
        )}
      </div>

      {isV3 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(170px,1fr))', gap: 10, marginTop: 10 }}>
          <label>Security name<input style={input} value={secName} onChange={(e) => setSecName(e.target.value)} /></label>
          <label>Auth protocol
            <select style={input} value={authProto} onChange={(e) => setAuthProto(e.target.value)}>
              {['SHA', 'SHA256', 'SHA512', 'MD5'].map((p) => <option key={p}>{p}</option>)}
            </select>
          </label>
          <label>Auth key<input style={input} type="password" value={authKey} onChange={(e) => setAuthKey(e.target.value)} autoComplete="new-password" /></label>
          <label>Priv protocol
            <select style={input} value={privProto} onChange={(e) => setPrivProto(e.target.value)}>
              {['AES', 'AES256', 'DES'].map((p) => <option key={p}>{p}</option>)}
            </select>
          </label>
          <label>Priv key<input style={input} type="password" value={privKey} onChange={(e) => setPrivKey(e.target.value)} autoComplete="new-password" /></label>
        </div>
      )}

      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!valid || m.isPending} onClick={() => m.mutate()}>
          {m.isPending ? 'Encrypting…' : 'Create'}
        </button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </div>
  )
}
