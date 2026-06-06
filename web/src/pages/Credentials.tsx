import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Credential, type EncryptionStatus, type Device, type CredTestResponse, type CredTestResult } from '../api'
import { CredentialRunsPanel, CredentialHistoryPanel } from '../components/CredentialTestHistory'

function EncryptionGate() {
  const q = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), retry: 0 })
  if (!q.data || q.data.enabled) return null
  return (
    <div className="enc-banner crit" style={{ marginBottom: 16 }}>
      <span>🔒</span>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 700 }}>Credential storage is disabled — no encryption key is configured</div>
        <div style={{ fontSize: 12, marginTop: 2 }}>Credential creation, updates and credential-based discovery will not work until encryption is configured. Action required: set <code>HIMS_ENCRYPTION_KEY</code> in your deployment environment and restart the API.</div>
      </div>
      <Link className="btn btn-sm" to="/security/encryption" style={{ whiteSpace: 'nowrap' }}>Configure Encryption →</Link>
    </div>
  )
}

const KINDS = ['snmp_v2c', 'snmp_v3', 'ssh', 'winrm', 'wmi', 'http_basic', 'onvif', 'vendor_api', 'ldap']

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}
const cell: React.CSSProperties = { padding: '6px 8px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13 }
const ghost: React.CSSProperties = { padding: '3px 8px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }

export function Credentials() {
  const qc = useQueryClient()
  const [show, setShow] = useState(false)
  const [edit, setEdit] = useState<string | null>(null)
  const [editName, setEditName] = useState('')
  const [editSecret, setEditSecret] = useState('')
  const [hist, setHist] = useState<{ id: string; name: string } | null>(null)
  const list = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const refresh = () => qc.invalidateQueries({ queryKey: ['credentials'] })

  const save = useMutation({
    mutationFn: (id: string) => api.patch(`/credentials/${id}`, { name: editName, secret: editSecret }),
    onSuccess: () => { setEdit(null); setEditSecret(''); refresh() },
  })
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/credentials/${id}`),
    onSuccess: refresh,
  })
  const startEdit = (c: Credential) => { setEdit(c.id); setEditName(c.name); setEditSecret('') }

  return (
    <div>
      <EncryptionGate />
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

      {list.data && list.data.length > 0 && <CredentialTester credentials={list.data} />}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.error && <div className="error-msg">{(list.error as Error).message}</div>}
        {list.data && list.data.length === 0 && <div className="muted">No credentials yet.</div>}
        {list.data && list.data.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Kind</th><th>Weak</th><th>Created</th><th></th></tr></thead>
            <tbody>
              {list.data.map((c) => (
                edit === c.id ? (
                  <tr key={c.id} style={{ background: '#1a2733' }}>
                    <td><input style={cell} value={editName} onChange={(e) => setEditName(e.target.value)} /></td>
                    <td>{c.kind}</td>
                    <td colSpan={2}>
                      <input style={{ ...cell, width: 220 }} type="password" placeholder="new secret (leave blank to keep)" value={editSecret} onChange={(e) => setEditSecret(e.target.value)} autoComplete="new-password" />
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button style={btn} disabled={!editName || save.isPending} onClick={() => save.mutate(c.id)}>Save</button>{' '}
                      <button style={ghost} onClick={() => setEdit(null)}>Cancel</button>
                    </td>
                  </tr>
                ) : (
                  <tr key={c.id}>
                    <td><strong>{c.name}</strong></td>
                    <td>{c.kind}</td>
                    <td>{c.weak ? <span className="badge badge-warning">weak</span> : '—'}</td>
                    <td>{c.created_at?.slice(0, 10)}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button style={ghost} onClick={() => setHist(hist?.id === c.id ? null : { id: c.id, name: c.name })}>History</button>{' '}
                      <button style={ghost} onClick={() => startEdit(c)}>Edit</button>{' '}
                      <button style={{ ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }} onClick={() => { if (confirm(`Delete credential "${c.name}"? It will be unbound from any devices.`)) del.mutate(c.id) }}>Delete</button>
                    </td>
                  </tr>
                )
              ))}
            </tbody>
          </table>
        )}
        {(save.error || del.error) && (
          <div className="error-msg" style={{ marginTop: 8 }}>{((save.error || del.error) as Error).message}</div>
        )}
      </div>

      {hist && <CredentialHistoryPanel credentialId={hist.id} credentialName={hist.name} />}

      <CredentialRunsPanel />
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
  const userPass = kind === 'ssh' || kind === 'winrm' || kind === 'wmi' || kind === 'http_basic' || kind === 'onvif' || kind === 'vendor_api'

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

const CAT_BADGE: Record<string, string> = {
  success: 'badge-up', auth_failed: 'badge-warning', unreachable: 'badge-down',
  unsupported: 'badge-unknown', error: 'badge-down',
}

// CredentialTester runs the universal credential-test matrix: pick credentials +
// devices, probe every pair, show a secrets-free result grid. Results come from
// POST /credentials/test (the server decrypts to probe; nothing secret returns).
function CredentialTester({ credentials }: { credentials: Credential[] }) {
  const devicesQ = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const [credSel, setCredSel] = useState<Set<string>>(new Set())
  const [devSel, setDevSel] = useState<Set<string>>(new Set())
  const [filter, setFilter] = useState('')
  const [legacyKex, setLegacyKex] = useState(false)

  const filtered = useMemo(() => {
    const f = filter.trim().toLowerCase()
    const withIP = (devicesQ.data ?? []).filter((d) => d.primary_ip)
    if (!f) return withIP.slice(0, 200)
    return withIP.filter((d) => d.name.toLowerCase().includes(f) || (d.primary_ip ?? '').includes(f) || d.category.includes(f)).slice(0, 200)
  }, [devicesQ.data, filter])

  const toggle = (set: Set<string>, id: string, fn: (s: Set<string>) => void) => {
    const next = new Set(set)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    fn(next)
  }
  const pairs = credSel.size * devSel.size

  const run = useMutation({
    mutationFn: () => api.post<CredTestResponse>('/credentials/test', {
      credential_ids: [...credSel], device_ids: [...devSel], legacy_kex: legacyKex,
    }),
  })

  return (
    <div className="card">
      <h2>Test credentials against devices</h2>
      <p className="muted" style={{ marginBottom: 10 }}>
        Verify which credentials authenticate to which devices — any combination (one-to-many or
        many-to-one). The server decrypts each secret only to run the probe; the secret is never
        returned or logged. SNMP / SSH / HTTP / ONVIF / WinRM are supported.
      </p>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <div>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>Credentials ({credSel.size})</div>
          <div style={{ maxHeight: 220, overflow: 'auto', border: '1px solid #2a3a47', borderRadius: 6, padding: 8 }}>
            {credentials.map((c) => (
              <label key={c.id} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', fontSize: 13 }}>
                <input type="checkbox" checked={credSel.has(c.id)} onChange={() => toggle(credSel, c.id, setCredSel)} />
                <span>{c.name}</span><span className="muted" style={{ fontSize: 11 }}>{c.kind}</span>
              </label>
            ))}
          </div>
        </div>
        <div>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>Devices ({devSel.size})</div>
          <input style={{ ...input, marginBottom: 6 }} placeholder="filter by name / IP / category…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <div style={{ maxHeight: 184, overflow: 'auto', border: '1px solid #2a3a47', borderRadius: 6, padding: 8 }}>
            {devicesQ.isLoading && <div className="muted">Loading devices…</div>}
            {filtered.map((d) => (
              <label key={d.id} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', fontSize: 13 }}>
                <input type="checkbox" checked={devSel.has(d.id)} onChange={() => toggle(devSel, d.id, setDevSel)} />
                <span className="mono" style={{ fontSize: 12 }}>{d.primary_ip}</span><span>{d.name}</span>
              </label>
            ))}
            {!devicesQ.isLoading && filtered.length === 0 && <div className="muted">No matching devices.</div>}
          </div>
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginTop: 12 }}>
        <button style={btn} disabled={pairs === 0 || pairs > 500 || run.isPending} onClick={() => run.mutate()}>
          {run.isPending ? 'Testing…' : `Test ${pairs} pair${pairs === 1 ? '' : 's'}`}
        </button>
        <label style={{ fontSize: 13, display: 'flex', gap: 6, alignItems: 'center' }}>
          <input type="checkbox" checked={legacyKex} onChange={(e) => setLegacyKex(e.target.checked)} />
          Legacy SSH KEX (old switches)
        </label>
        {pairs > 500 && <span className="error-msg">Too many pairs ({pairs}); max 500.</span>}
        {run.error && <span className="error-msg">{(run.error as Error).message}</span>}
      </div>

      {run.data && (
        <div style={{ marginTop: 14 }}>
          <div style={{ marginBottom: 8, fontSize: 13 }}>
            <span className="badge badge-up">{run.data.successes} ok</span>{' '}
            <span className="badge badge-down">{run.data.failures} failed</span>{' '}
            <span className="muted">of {run.data.pairs} pairs</span>
          </div>
          <table>
            <thead><tr><th>Device</th><th>IP</th><th>Credential</th><th>Protocol</th><th>Result</th><th>Detail</th><th>Latency</th></tr></thead>
            <tbody>
              {run.data.results.map((r: CredTestResult, i) => (
                <tr key={i}>
                  <td>{r.device_name}</td>
                  <td className="mono" style={{ fontSize: 12 }}>{r.ip}</td>
                  <td>{r.credential_name} <span className="muted" style={{ fontSize: 11 }}>{r.kind}</span></td>
                  <td>{r.protocol || '—'}</td>
                  <td><span className={`badge ${CAT_BADGE[r.category] ?? 'badge-unknown'}`}>{r.category.replace(/_/g, ' ')}</span></td>
                  <td className="muted" style={{ fontSize: 12 }}>{r.detail}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{r.latency_ms} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
