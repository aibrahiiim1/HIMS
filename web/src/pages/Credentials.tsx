import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { KeyRound, ShieldCheck, ListChecks, Boxes, Plus } from 'lucide-react'
import { api, type Credential, type EncryptionStatus, type Device, type CredTestResponse, type CredTestResult } from '../api'
import { CredentialRunsPanel, CredentialHistoryPanel } from '../components/CredentialTestHistory'
import { PageHeader, Panel, Kpi, TabBar, EmptyState, type Tone } from '../components/ui'
import { DataTable, type DataCol } from '../components/DataTable'

// Credential kinds: stable id → friendly label + a short hint shown in the form
// so operators know what secret each kind expects (e.g. ESXi/vCenter = root:pass
// as a vendor_api credential).
const KIND_META: Record<string, { label: string; hint?: string; tone?: Tone }> = {
  snmp_v2c:   { label: 'SNMP v2c', hint: 'Community string (e.g. public)' },
  snmp_v3:    { label: 'SNMP v3', hint: 'USM security name + auth/priv keys' },
  ssh:        { label: 'SSH', hint: 'username:password' },
  winrm:      { label: 'WinRM', hint: 'username:password (DOMAIN\\user or user@domain)' },
  wmi:        { label: 'WMI / DCOM', hint: 'username:password (domain admin for servers)' },
  http_basic: { label: 'HTTP Basic', hint: 'username:password' },
  onvif:      { label: 'CCTV (ONVIF)', hint: 'username:password' },
  vendor_api: { label: 'Vendor API (VMware / REST)', hint: 'username:password — e.g. ESXi/vCenter root:password' },
  ldap:       { label: 'LDAP / AD', hint: 'bind DN + password, or user@domain:password' },
}
const KINDS = Object.keys(KIND_META)
const kindLabel = (k: string) => KIND_META[k]?.label ?? k

const btn: React.CSSProperties = {
  padding: '8px 16px', background: 'var(--brand)', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, width: '100%',
  background: 'var(--surface-2)', color: 'var(--text)',
}
const ghost: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: 'var(--brand)', border: '1px solid var(--brand)', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { ...ghost, color: 'var(--crit)', borderColor: 'var(--crit)' }

function EncryptionGate() {
  const q = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), retry: 0 })
  if (!q.data || q.data.enabled) return null
  return (
    <div className="enc-banner crit" style={{ marginBottom: 16 }}>
      <span>🔒</span>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 700 }}>Credential storage is disabled — no encryption key is configured</div>
        <div style={{ fontSize: 12, marginTop: 2 }}>Credential creation, updates and credential-based discovery will not work until encryption is configured. Set <code>HIMS_ENCRYPTION_KEY</code> and restart the API.</div>
      </div>
      <Link className="btn btn-sm" to="/security/encryption" style={{ whiteSpace: 'nowrap' }}>Configure Encryption →</Link>
    </div>
  )
}

const TABS = [
  { key: 'creds', label: 'Credentials', icon: KeyRound },
  { key: 'test', label: 'Test against devices', icon: ListChecks },
  { key: 'history', label: 'Test history', icon: ShieldCheck },
]

export function Credentials() {
  const qc = useQueryClient()
  const [tab, setTab] = useState('creds')
  const [show, setShow] = useState(false)
  const [editCred, setEditCred] = useState<Credential | null>(null)
  const [dupCred, setDupCred] = useState<Credential | null>(null)
  const [hist, setHist] = useState<{ id: string; name: string } | null>(null)
  const [usage, setUsage] = useState<{ id: string; name: string } | null>(null)
  const list = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const refresh = () => qc.invalidateQueries({ queryKey: ['credentials'] })

  const del = useMutation({
    mutationFn: (id: string) => api.del(`/credentials/${id}`),
    onSuccess: refresh,
  })

  const creds = list.data ?? []
  const kinds = useMemo(() => {
    const m = new Map<string, number>()
    for (const c of creds) m.set(c.kind, (m.get(c.kind) ?? 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [creds])
  const weakCount = creds.filter((c) => c.weak).length
  const inUse = creds.filter((c) => (c.usage_count ?? 0) > 0).length

  const cols: DataCol<Credential>[] = [
    { key: 'name', label: 'Name', sortVal: (c) => c.name, render: (c) => <span><strong>{c.name}</strong>{c.needs_secret_reentry && <span className="badge badge-warning" style={{ marginLeft: 6 }} title="Restored as metadata — edit and re-enter the password">re-enter password</span>}</span> },
    { key: 'kind', label: 'Kind', sortVal: (c) => c.kind, render: (c) => <span className="badge badge-info">{kindLabel(c.kind)}</span> },
    { key: 'weak', label: 'Strength', sortVal: (c) => (c.weak ? 0 : 1), render: (c) => (c.weak ? <span className="badge badge-warning">weak</span> : <span className="badge badge-up">ok</span>) },
    {
      key: 'usage', label: 'Bound devices', sortVal: (c) => c.usage_count ?? 0,
      render: (c) => ((c.usage_count ?? 0) > 0
        ? <button style={ghost} title="Show bound devices" onClick={() => setUsage(usage?.id === c.id ? null : { id: c.id, name: c.name })}>{c.usage_count} device{c.usage_count === 1 ? '' : 's'}</button>
        : <span className="muted">0</span>),
    },
    { key: 'created', label: 'Created', sortVal: (c) => c.created_at ?? '', render: (c) => <span className="muted">{c.created_at?.slice(0, 10) ?? '—'}</span> },
    {
      key: 'actions', label: '', render: (c) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <button style={ghost} onClick={() => setHist(hist?.id === c.id ? null : { id: c.id, name: c.name })}>History</button>{' '}
          <button style={ghost} onClick={() => { setShow(false); setEditCred(null); setDupCred(c) }} title="Reuse this secret under a different kind (e.g. an SNMP community as an SSH/vendor login) — no re-typing">Duplicate as…</button>{' '}
          <button style={ghost} onClick={() => { setShow(false); setEditCred(c) }}>Edit</button>{' '}
          <button style={danger} onClick={() => { if (confirm(`Delete credential "${c.name}"? It will be unbound from any devices.`)) del.mutate(c.id) }}>Delete</button>
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <PageHeader
        title="Credentials"
        subtitle="Stored secrets used to authenticate and collect from devices. Encrypted at rest (AES-256-GCM) on save; plaintext is never stored, logged, or shown — only metadata appears here."
        icon={KeyRound}
        actions={<button style={btn} onClick={() => { setEditCred(null); setShow((v) => !v) }}><Plus size={14} style={{ verticalAlign: -2 }} /> {show ? 'Cancel' : 'New credential'}</button>}
      />
      <EncryptionGate />

      <div className="kpi-grid">
        <Kpi label="Total credentials" value={creds.length} icon={KeyRound} />
        <Kpi label="In use" value={inUse} sub="bound to ≥1 device" tone="ok" icon={Boxes} />
        <Kpi label="Weak" value={weakCount} tone={weakCount ? 'warn' : 'default'} icon={ShieldCheck} />
        <Kpi label="Distinct kinds" value={kinds.length} sub={kinds.slice(0, 4).map(([k, n]) => `${kindLabel(k)}·${n}`).join('  ')} />
      </div>

      <div style={{ margin: '12px 0' }}>
        <TabBar tabs={TABS} active={tab} onChange={setTab} />
      </div>

      {tab === 'creds' && (
        <>
          {show && <CreateForm onDone={() => { setShow(false); refresh() }} onCancel={() => setShow(false)} />}
          {editCred && <EditForm cred={editCred} onDone={() => { setEditCred(null); refresh() }} onCancel={() => setEditCred(null)} />}
          {dupCred && <DuplicateForm cred={dupCred} onDone={() => { setDupCred(null); refresh() }} onCancel={() => setDupCred(null)} />}

          <Panel title={`Stored credentials (${creds.length})`} icon={KeyRound} className="mt12">
            {list.isLoading && <div className="muted">Loading…</div>}
            {list.error && <div className="badge badge-down">{(list.error as Error).message}</div>}
            {!list.isLoading && creds.length === 0 && (
              <EmptyState icon={KeyRound} title="No credentials yet"
                message="Add SNMP / SSH / WinRM / WMI / HTTP / ONVIF / Vendor-API (VMware) credentials so discovery can authenticate and collect."
                action={<button style={btn} onClick={() => setShow(true)}>+ New credential</button>} />
            )}
            {creds.length > 0 && (
              <DataTable rows={creds} cols={cols} getKey={(c) => c.id}
                searchText={(c) => `${c.name} ${c.kind} ${kindLabel(c.kind)}`} searchPlaceholder="Search by name or kind…"
                filters={[{ key: 'kind', label: 'Kind', options: kinds.map(([k]) => ({ value: k, label: kindLabel(k) })), match: (c, v) => c.kind === v }]}
                pageSizeDefault={25} emptyTitle="No credentials" emptyMessage="No credentials match the filters." />
            )}
            {del.error && <div className="badge badge-down" style={{ marginTop: 8 }}>{(del.error as Error).message}</div>}
          </Panel>

          {usage && <CredentialUsagePanel credentialId={usage.id} credentialName={usage.name} onClose={() => setUsage(null)} />}
          {hist && <CredentialHistoryPanel credentialId={hist.id} credentialName={hist.name} />}
        </>
      )}

      {tab === 'test' && (creds.length > 0
        ? <CredentialTester credentials={creds} />
        : <Panel title="Test against devices" icon={ListChecks}><EmptyState icon={ListChecks} title="No credentials to test" message="Add a credential first, then test it against devices here." /></Panel>)}

      {tab === 'history' && <CredentialRunsPanel />}
    </div>
  )
}

// CredentialUsagePanel lists every device that uses a credential.
function CredentialUsagePanel({ credentialId, credentialName, onClose }: { credentialId: string; credentialName: string; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['credential-devices', credentialId],
    queryFn: () => api.get<import('../api').CredentialDevice[]>(`/credentials/${credentialId}/devices`),
  })
  return (
    <Panel title={`Devices using “${credentialName}”`} icon={Boxes} className="mt12"
      actions={<button style={ghost} onClick={onClose}>Close</button>}>
      {q.isLoading && <div className="muted">Loading…</div>}
      {q.error && <div className="badge badge-down">{(q.error as Error).message}</div>}
      {q.data && q.data.length === 0 && <div className="muted">No devices are bound to this credential.</div>}
      {q.data && q.data.length > 0 && (
        <table>
          <thead><tr><th>Device</th><th>IP</th><th>Category</th><th>Status</th><th>Bound as</th></tr></thead>
          <tbody>
            {q.data.map((d) => (
              <tr key={d.id}>
                <td><Link to={`/devices/${d.id}`}><strong>{d.name}</strong></Link></td>
                <td className="mono" style={{ fontSize: 12 }}>{d.primary_ip || '—'}</td>
                <td>{d.category}</td>
                <td>{d.status}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  {d.bound_primary && <span className="badge badge-up">primary</span>}
                  {d.bound_primary && d.bound_cctv ? ' ' : null}
                  {d.bound_cctv && <span className="badge badge-info">CCTV web</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

function EditForm({ cred, onDone, onCancel }: { cred: Credential; onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState(cred.name)
  const [secret, setSecret] = useState('')
  const save = useMutation({
    mutationFn: () => api.patch(`/credentials/${cred.id}`, { name, secret }),
    onSuccess: onDone,
  })
  return (
    <Panel title={`Edit “${cred.name}”`} icon={KeyRound} className="mt12">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 12 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>{KIND_META[cred.kind]?.label ?? cred.kind} secret
          <input style={input} type="password" placeholder="leave blank to keep current" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" />
        </label>
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!name.trim() || save.isPending} onClick={() => save.mutate()}>{save.isPending ? 'Saving…' : 'Save changes'}</button>{' '}
        <button style={ghost} onClick={onCancel}>Cancel</button>
        {save.error && <span className="badge badge-down" style={{ marginLeft: 12 }}>{(save.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

// DuplicateForm reuses an existing credential's secret under a different kind
// without re-typing it (server-side decrypt + re-seal). The classic fix: a
// password saved only as an SNMP community needs to become an SSH / vendor_api
// login. When the target is a user:password kind and the source is secret-only,
// the operator supplies just the username (not a secret).
function DuplicateForm({ cred, onDone, onCancel }: { cred: Credential; onDone: () => void; onCancel: () => void }) {
  const DUP_KINDS = ['vendor_api', 'ssh', 'winrm', 'wmi', 'http_basic', 'onvif', 'snmp_v2c']
  const USERPASS = ['ssh', 'winrm', 'wmi', 'http_basic', 'onvif', 'vendor_api', 'ldap']
  const [newKind, setNewKind] = useState('vendor_api')
  const [username, setUsername] = useState('root')
  const [name, setName] = useState(`${cred.name} (as vendor_api)`)
  // Username needed only when reusing a secret-only source as a user:password login.
  const needsUser = USERPASS.includes(newKind) && !USERPASS.includes(cred.kind)
  const m = useMutation({
    mutationFn: () => api.post<Credential>(`/credentials/${cred.id}/duplicate`, { new_kind: newKind, username: needsUser ? username : '', name }),
    onSuccess: onDone,
  })
  return (
    <Panel title={`Duplicate “${cred.name}” as another kind`} icon={KeyRound} className="mt12">
      <div className="muted" style={{ fontSize: 12, marginBottom: 10 }}>
        Reuses this credential’s stored secret under a new kind — the secret is never re-typed or shown.
        Use this when the right password exists but under the wrong kind (e.g. an SNMP community that is also a device’s login password).
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 12 }}>
        <label>New kind
          <select style={input} value={newKind} onChange={(e) => { setNewKind(e.target.value); setName(`${cred.name} (as ${e.target.value})`) }}>
            {DUP_KINDS.filter((k) => k !== cred.kind).map((k) => <option key={k} value={k}>{kindLabel(k)}</option>)}
          </select>
        </label>
        {needsUser && <label>Username<input style={input} placeholder="root / admin / administrator" value={username} onChange={(e) => setUsername(e.target.value)} /></label>}
        <label>New credential name<input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
      </div>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={(needsUser && !username.trim()) || !name.trim() || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Creating…' : 'Create duplicate'}</button>{' '}
        <button style={ghost} onClick={onCancel}>Cancel</button>
        {m.error && <span className="badge badge-down" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

function CreateForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [kind, setKind] = useState('winrm')
  const [secret, setSecret] = useState('')
  const [secName, setSecName] = useState('')
  const [authProto, setAuthProto] = useState('SHA')
  const [authKey, setAuthKey] = useState('')
  const [privProto, setPrivProto] = useState('AES')
  const [privKey, setPrivKey] = useState('')

  const isV3 = kind === 'snmp_v3'
  const userPass = ['ssh', 'winrm', 'wmi', 'http_basic', 'onvif', 'vendor_api', 'ldap'].includes(kind)
  const meta = KIND_META[kind]

  const buildSecret = (): string => isV3
    ? JSON.stringify({ security_name: secName, auth_protocol: authKey ? authProto : '', auth_key: authKey, priv_protocol: privKey ? privProto : '', priv_key: privKey })
    : secret
  const valid = name && (isV3 ? secName : secret)
  const m = useMutation({
    mutationFn: () => api.post<Credential>('/credentials', { name, kind, secret: buildSecret() }),
    onSuccess: onDone,
  })

  return (
    <Panel title="New credential" icon={Plus} className="mt12">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 12 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Domain Admin (dpm)" /></label>
        <label>Kind
          <select style={input} value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => <option key={k} value={k}>{kindLabel(k)}</option>)}
          </select>
        </label>
        {!isV3 && (
          <label>{userPass ? 'username:password' : kind.startsWith('snmp') ? 'Community' : 'Secret'}
            <input style={input} type={userPass ? 'text' : 'password'} value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" placeholder={meta?.hint} />
          </label>
        )}
      </div>
      {meta?.hint && <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>{meta.hint}</p>}

      {isV3 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(170px,1fr))', gap: 10, marginTop: 10 }}>
          <label>Security name<input style={input} value={secName} onChange={(e) => setSecName(e.target.value)} /></label>
          <label>Auth protocol
            <select style={input} value={authProto} onChange={(e) => setAuthProto(e.target.value)}>{['SHA', 'SHA256', 'SHA512', 'MD5'].map((p) => <option key={p}>{p}</option>)}</select>
          </label>
          <label>Auth key<input style={input} type="password" value={authKey} onChange={(e) => setAuthKey(e.target.value)} autoComplete="new-password" /></label>
          <label>Priv protocol
            <select style={input} value={privProto} onChange={(e) => setPrivProto(e.target.value)}>{['AES', 'AES256', 'DES'].map((p) => <option key={p}>{p}</option>)}</select>
          </label>
          <label>Priv key<input style={input} type="password" value={privKey} onChange={(e) => setPrivKey(e.target.value)} autoComplete="new-password" /></label>
        </div>
      )}

      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!valid || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Encrypting…' : 'Create credential'}</button>{' '}
        <button style={ghost} onClick={onCancel}>Cancel</button>
        {m.error && <span className="badge badge-down" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

const CAT_BADGE: Record<string, string> = {
  success: 'badge-up', auth_failed: 'badge-warning', unreachable: 'badge-down',
  unsupported: 'badge-unknown', error: 'badge-down',
}

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
    const next = new Set(set); if (next.has(id)) next.delete(id); else next.add(id); fn(next)
  }
  const pairs = credSel.size * devSel.size
  const run = useMutation({
    mutationFn: () => api.post<CredTestResponse>('/credentials/test', { credential_ids: [...credSel], device_ids: [...devSel], legacy_kex: legacyKex }),
  })

  return (
    <Panel title="Test credentials against devices" icon={ListChecks} className="mt12">
      <p className="muted" style={{ marginBottom: 10, fontSize: 13 }}>
        Verify which credentials authenticate to which devices (any combination). The server decrypts each secret only to run the probe — nothing secret is returned or logged.
      </p>
      <div className="grid-2">
        <div>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>Credentials ({credSel.size})</div>
          <div style={{ maxHeight: 220, overflow: 'auto', border: '1px solid var(--border)', borderRadius: 6, padding: 8 }}>
            {credentials.map((c) => (
              <label key={c.id} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', fontSize: 13 }}>
                <input type="checkbox" checked={credSel.has(c.id)} onChange={() => toggle(credSel, c.id, setCredSel)} />
                <span>{c.name}</span><span className="muted" style={{ fontSize: 11 }}>{kindLabel(c.kind)}</span>
              </label>
            ))}
          </div>
        </div>
        <div>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>Devices ({devSel.size})</div>
          <input style={{ ...input, marginBottom: 6 }} placeholder="filter by name / IP / category…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <div style={{ maxHeight: 184, overflow: 'auto', border: '1px solid var(--border)', borderRadius: 6, padding: 8 }}>
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
        <button style={btn} disabled={pairs === 0 || pairs > 500 || run.isPending} onClick={() => run.mutate()}>{run.isPending ? 'Testing…' : `Test ${pairs} pair${pairs === 1 ? '' : 's'}`}</button>
        <label style={{ fontSize: 13, display: 'flex', gap: 6, alignItems: 'center' }}>
          <input type="checkbox" checked={legacyKex} onChange={(e) => setLegacyKex(e.target.checked)} /> Legacy SSH KEX (old switches)
        </label>
        {pairs > 500 && <span className="badge badge-down">Too many pairs ({pairs}); max 500.</span>}
        {run.error && <span className="badge badge-down">{(run.error as Error).message}</span>}
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
                  <td>{r.credential_name} <span className="muted" style={{ fontSize: 11 }}>{kindLabel(r.kind)}</span></td>
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
    </Panel>
  )
}
