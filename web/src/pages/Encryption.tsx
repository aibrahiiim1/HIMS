import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Lock, KeyRound, ShieldCheck, RefreshCw, RotateCw, Copy, Download, TriangleAlert, CircleCheck, BookOpen } from 'lucide-react'
import { api, type EncryptionStatus, type KeyReveal, type ReentryCred, type GuideSection } from '../api'
import { PageHeader, Panel, Kpi, TabBar, EmptyState, timeAgo } from '../components/ui'

type Tab = 'status' | 'keys' | 'recovery' | 'guide'

export function Encryption() {
  const [tab, setTab] = useState<Tab>('status')
  const status = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), refetchInterval: 30_000 })
  const s = status.data

  return (
    <div>
      <PageHeader title="Encryption" icon={Lock} subtitle="Encryption key lifecycle — health, validation, rotation and recovery" />
      <TabBar
        tabs={[
          { key: 'status', label: 'Status', icon: ShieldCheck },
          { key: 'keys', label: 'Key Management', icon: KeyRound },
          { key: 'recovery', label: 'Credential Recovery', icon: RefreshCw, count: s?.needs_reset_count || undefined },
          { key: 'guide', label: 'Recovery Guide', icon: BookOpen },
        ]}
        active={tab} onChange={(k) => setTab(k as Tab)}
      />
      {tab === 'status' && <StatusTab s={s} loading={status.isLoading} onGo={() => setTab('keys')} />}
      {tab === 'keys' && <KeysTab s={s} />}
      {tab === 'recovery' && <RecoveryTab s={s} />}
      {tab === 'guide' && <GuideTab />}
    </div>
  )
}

function StatusBadge({ s }: { s?: EncryptionStatus }) {
  if (!s) return null
  const map = { enabled: 'badge-up', pending_restart: 'badge-warning', missing: 'badge-down' } as Record<string, string>
  const label = { enabled: 'Enabled', pending_restart: 'Pending restart', missing: 'Not configured' } as Record<string, string>
  return <span className={`badge ${map[s.status]}`}>{label[s.status]}</span>
}

function Warnings({ items }: { items: string[] }) {
  if (!items.length) return null
  return (
    <div className="stack" style={{ gap: 8, marginBottom: 16 }}>
      {items.map((w, i) => (
        <div key={i} className="enc-banner warn"><TriangleAlert size={16} /> <span>{w}</span></div>
      ))}
    </div>
  )
}

function StatusTab({ s, loading, onGo }: { s?: EncryptionStatus; loading: boolean; onGo: () => void }) {
  if (loading || !s) return <Panel title="Encryption Status"><div className="loading">Loading…</div></Panel>

  if (s.status === 'missing') {
    return (
      <Panel title="Encryption Status" icon={ShieldCheck}>
        <EmptyState icon={KeyRound} title="No encryption key has been configured"
          message="Credential secrets cannot be encrypted until a key is configured. Generate one to get started — you'll be shown the key exactly once."
          action={<button className="btn btn-primary" onClick={onGo}>Generate Encryption Key</button>} />
      </Panel>
    )
  }

  return (
    <div>
      <Warnings items={s.warnings} />
      <div className="kpi-grid">
        <Kpi label="Encryption" value={<StatusBadge s={s} />} icon={Lock} tone={s.status === 'enabled' ? 'ok' : s.status === 'pending_restart' ? 'warn' : 'crit'} sub={s.algorithm} />
        <Kpi label="Encrypted Credentials" value={s.encrypted_count} icon={KeyRound} tone="info" />
        <Kpi label="Needs Re-entry" value={s.needs_reset_count} icon={RefreshCw} tone={s.needs_reset_count > 0 ? 'warn' : 'default'} />
        <Kpi label="Undecryptable" value={s.undecryptable_count} icon={TriangleAlert} tone={s.undecryptable_count > 0 ? 'crit' : 'default'} />
      </div>
      <Panel title="Key Details" icon={ShieldCheck}>
        <dl className="deflist">
          <div><dt>Status</dt><dd><StatusBadge s={s} /></dd></div>
          <div><dt>Algorithm</dt><dd>{s.algorithm}</dd></div>
          <div><dt>Encryption Version</dt><dd>v{s.version}</dd></div>
          <div><dt>Fingerprint match</dt><dd>{s.fingerprint_match ? <span className="badge badge-up">match</span> : <span className="badge badge-down">mismatch</span>}</dd></div>
          <div style={{ gridColumn: '1 / -1' }}><dt>SHA-256 Fingerprint</dt><dd className="mono" style={{ wordBreak: 'break-all' }}>{s.fingerprint || '—'}</dd></div>
          <div><dt>Key ID</dt><dd className="mono">{s.key_id || '—'}</dd></div>
          <div><dt>Created</dt><dd>{s.created_at ? timeAgo(s.created_at) : '—'}</dd></div>
          <div><dt>Last rotation</dt><dd>{s.last_rotation_at ? timeAgo(s.last_rotation_at) : 'never'}</dd></div>
          <div><dt>Last validation</dt><dd>{s.last_validation_at ? timeAgo(s.last_validation_at) : 'never'}</dd></div>
        </dl>
        <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>The encryption key is never stored or displayed — only this one-way fingerprint identifies it.</p>
      </Panel>
    </div>
  )
}

// One-time key reveal — copy + download + required confirmation.
function KeyReveal({ data, onClose }: { data: KeyReveal; onClose: () => void }) {
  const key = data.key ?? data.new_key ?? ''
  const [saved, setSaved] = useState(false)
  const [copied, setCopied] = useState(false)
  const download = () => {
    const body = `HIMS encryption recovery key\nGenerated: ${new Date().toISOString()}\nFingerprint: ${data.fingerprint}\nKey ID: ${data.key_id}\n\nHIMS_ENCRYPTION_KEY=${key}\n\nStore this securely. It cannot be retrieved again.\n`
    const blob = new Blob([body], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a'); a.href = url; a.download = 'hims-recovery-key.txt'; a.click(); URL.revokeObjectURL(url)
  }
  return (
    <div className="modal-scrim">
      <div className="modal">
        <h2><KeyRound size={18} /> Save your recovery key now</h2>
        <p className="enc-banner warn"><TriangleAlert size={16} /> <span>This key is shown <b>once only</b>. You will never be able to view it again. If it is lost, encrypted credential secrets cannot be recovered.</span></p>
        {data.rotated != null && <p className="muted" style={{ fontSize: 13 }}>Re-encrypted {data.rotated} credential(s){data.failed && data.failed.length > 0 ? `, ${data.failed.length} failed` : ''}.</p>}
        <div className="key-box mono">{key}</div>
        <div className="row" style={{ margin: '12px 0' }}>
          <button className="btn" onClick={() => { navigator.clipboard?.writeText(key); setCopied(true) }}><Copy size={15} /> {copied ? 'Copied' : 'Copy key'}</button>
          <button className="btn" onClick={download}><Download size={15} /> Download recovery file</button>
        </div>
        <p className="muted" style={{ fontSize: 12 }}>{data.instructions}</p>
        <label className="row" style={{ gap: 8, margin: '12px 0' }}><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} /> I have saved this key securely</label>
        <div className="row"><button className="btn btn-primary" disabled={!saved} onClick={onClose}>Done</button></div>
      </div>
    </div>
  )
}

function KeysTab({ s }: { s?: EncryptionStatus }) {
  const qc = useQueryClient()
  const [reveal, setReveal] = useState<KeyReveal | null>(null)
  const [msg, setMsg] = useState('')
  const inv = () => qc.invalidateQueries({ queryKey: ['enc-status'] })

  const generate = useMutation({ mutationFn: () => api.post<KeyReveal>('/security/encryption/generate', {}), onSuccess: (d) => { setReveal(d as KeyReveal); inv() }, onError: (e) => setMsg((e as Error).message) })
  const validate = useMutation({ mutationFn: () => api.post<{ status: string; detail: string; fingerprint_match: boolean }>('/security/encryption/validate', {}), onSuccess: (d) => { setMsg(`Validation: ${(d as { status: string }).status} — ${(d as { detail: string }).detail}`); inv() }, onError: (e) => setMsg((e as Error).message) })
  const rotate = useMutation({ mutationFn: () => api.post<KeyReveal>('/security/encryption/rotate', {}), onSuccess: (d) => { setReveal(d as KeyReveal); inv() }, onError: (e) => setMsg((e as Error).message) })

  return (
    <div className="stack">
      {reveal && <KeyReveal data={reveal} onClose={() => { setReveal(null); inv() }} />}

      <Panel title="Generate Encryption Key" icon={KeyRound}>
        <p className="muted" style={{ marginBottom: 12 }}>Create a cryptographically secure 32-byte AES-256 key, shown once. Available only when no key is active.</p>
        <button className="btn btn-primary" disabled={s?.enabled || generate.isPending} onClick={() => generate.mutate()}>
          <KeyRound size={15} /> Generate key
        </button>
        {s?.enabled && <span className="muted" style={{ marginLeft: 12, fontSize: 12 }}>A key is already active — use Rotate to change it.</span>}
      </Panel>

      <Panel title="Validate Encryption Key" icon={ShieldCheck}>
        <p className="muted" style={{ marginBottom: 12 }}>Verify the loaded key matches the recorded fingerprint and that a seal/open round-trip succeeds.</p>
        <button className="btn" disabled={validate.isPending} onClick={() => validate.mutate()}><ShieldCheck size={15} /> Validate now</button>
      </Panel>

      <Panel title="Rotate Encryption Key" icon={RotateCw}>
        <p className="enc-banner warn"><TriangleAlert size={16} /> <span>Rotation re-encrypts every credential under a new key. The new key is shown once; you must set <code>HIMS_ENCRYPTION_KEY</code> to it and restart the API. Requires the current key to be loaded.</span></p>
        <button className="btn btn-primary" disabled={!s?.enabled || rotate.isPending} onClick={() => { if (confirm('Rotate the encryption key? All credentials will be re-encrypted and you must update HIMS_ENCRYPTION_KEY + restart.')) rotate.mutate() }}>
          <RotateCw size={15} /> {rotate.isPending ? 'Rotating…' : 'Rotate key'}
        </button>
      </Panel>

      {msg && <div className="enc-banner info"><CircleCheck size={16} /> <span>{msg}</span></div>}
    </div>
  )
}

function RecoveryTab({ s }: { s?: EncryptionStatus }) {
  const qc = useQueryClient()
  const needs = useQuery({ queryKey: ['enc-reentry'], queryFn: () => api.get<ReentryCred[]>('/security/encryption/needs-reentry') })
  const [confirm, setConfirm] = useState('')
  const [msg, setMsg] = useState('')
  const inv = () => { qc.invalidateQueries({ queryKey: ['enc-reentry'] }); qc.invalidateQueries({ queryKey: ['enc-status'] }); qc.invalidateQueries({ queryKey: ['credentials'] }) }

  const reset = useMutation({ mutationFn: () => api.post<{ reset: number; message: string }>('/security/encryption/reset-credentials', { confirm }), onSuccess: (d) => { setMsg((d as { message: string }).message); setConfirm(''); inv() }, onError: (e) => setMsg((e as Error).message) })
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const reenter = useMutation({ mutationFn: (id: string) => api.patch(`/credentials/${id}`, { secret: secrets[id] }), onSuccess: () => { inv() }, onError: (e) => setMsg((e as Error).message) })

  const rows = needs.data ?? []
  return (
    <div className="stack">
      <Panel title="Reset Credential Secrets" icon={RefreshCw}>
        <p className="enc-banner crit"><TriangleAlert size={16} /> <span>Existing encrypted credential secrets <b>cannot be recovered</b> without the original encryption key. This clears only the secret fields — credential records, metadata, site assignments and group memberships are preserved — and flags each for re-entry.</span></p>
        <p className="muted" style={{ fontSize: 13, margin: '10px 0' }}>Type <b>RESET CREDENTIALS</b> to confirm.</p>
        <div className="row">
          <input className="field" style={{ width: 240 }} value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder="RESET CREDENTIALS" />
          <button className="btn btn-danger" disabled={confirm !== 'RESET CREDENTIALS' || reset.isPending} onClick={() => reset.mutate()}>Reset credential secrets</button>
        </div>
        {msg && <div className="muted" style={{ fontSize: 12, marginTop: 10 }}>{msg}</div>}
      </Panel>

      <Panel title="Credentials Needing Re-entry" icon={KeyRound} subtitle={`${rows.length}`} pad={false}>
        {needs.isLoading && <div className="loading">Loading…</div>}
        {needs.data && rows.length === 0 && <EmptyState icon={CircleCheck} title="No credentials need re-entry" message="All credential secrets are intact." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Credential</th><th>Type</th><th>New secret</th><th></th></tr></thead>
            <tbody>
              {rows.map((c) => (
                <tr key={c.id}>
                  <td className="cell-name">{c.name}</td>
                  <td>{c.kind}</td>
                  <td><input className="field" type="password" style={{ width: 220 }} value={secrets[c.id] ?? ''} onChange={(e) => setSecrets({ ...secrets, [c.id]: e.target.value })} placeholder="enter secret" disabled={!s?.enabled} /></td>
                  <td className="cell-actions"><button className="btn btn-primary btn-xs" disabled={!secrets[c.id] || !s?.enabled || reenter.isPending} onClick={() => reenter.mutate(c.id)}>Save</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {!s?.enabled && rows.length > 0 && <div className="muted" style={{ padding: 'var(--space-4) var(--space-5)', fontSize: 12 }}>Re-entry requires an active encryption key. Configure/restore the key first.</div>}
      </Panel>
    </div>
  )
}

function GuideTab() {
  const q = useQuery({ queryKey: ['enc-guide'], queryFn: () => api.get<GuideSection[]>('/security/encryption/recovery-guide') })
  const sections = q.data ?? []
  const copyAll = () => navigator.clipboard?.writeText(sections.map((s) => `## ${s.title}\n${s.body}`).join('\n\n'))
  return (
    <Panel title="Encryption Recovery Guide" icon={BookOpen} actions={<button className="btn btn-ghost btn-sm" onClick={copyAll}><Copy size={14} /> Copy all</button>}>
      {q.isLoading && <div className="loading">Loading…</div>}
      <div className="stack" style={{ gap: 16 }}>
        {sections.map((sec, i) => (
          <div key={i}>
            <h3 style={{ fontSize: 15, marginBottom: 4 }}>{sec.title}</h3>
            <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>{sec.body}</p>
          </div>
        ))}
      </div>
    </Panel>
  )
}
