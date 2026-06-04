import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Lock, KeyRound, ShieldCheck, RefreshCw, RotateCw, Copy, Download, TriangleAlert, CircleCheck, BookOpen, Rocket, ListChecks } from 'lucide-react'
import { api, unlockEncryption, type EncryptionStatus, type EncryptionUnlockResult, type KeyReveal, type ReentryCred, type GuideSection } from '../api'
import { PageHeader, Panel, Kpi, TabBar, EmptyState, timeAgo } from '../components/ui'
import { StartupChecklist, DEPLOY_MODES, deploymentSteps, buildRunbook, CmdBlock, DownloadBtn, type DeployMode } from '../components/EncryptionSetup'

type Tab = 'status' | 'wizard' | 'keys' | 'recovery' | 'guide'

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
          { key: 'wizard', label: 'Setup Wizard', icon: Rocket },
          { key: 'keys', label: 'Key Management', icon: KeyRound },
          { key: 'recovery', label: 'Credential Recovery', icon: RefreshCw, count: s?.needs_reset_count || undefined },
          { key: 'guide', label: 'Recovery Guide', icon: BookOpen },
        ]}
        active={tab} onChange={(k) => setTab(k as Tab)}
      />
      {tab === 'status' && <StatusTab s={s} loading={status.isLoading} onGo={() => setTab('wizard')} />}
      {tab === 'wizard' && <SetupWizard s={s} />}
      {tab === 'keys' && <KeysTab s={s} />}
      {tab === 'recovery' && <RecoveryTab s={s} />}
      {tab === 'guide' && <GuideTab />}
    </div>
  )
}

const ENC_BADGE: Record<string, string> = { enabled: 'badge-up', pending_restart: 'badge-warning', missing_key: 'badge-down', no_metadata: 'badge-unknown', fingerprint_mismatch: 'badge-down', invalid_key: 'badge-down' }
const ENC_LABEL: Record<string, string> = { enabled: 'Enabled', pending_restart: 'Pending restart', missing_key: 'Key missing', no_metadata: 'Not configured', fingerprint_mismatch: 'Fingerprint mismatch', invalid_key: 'Invalid key' }
const yn = (b?: boolean) => (b ? <span className="badge badge-up">Yes</span> : <span className="badge badge-down">No</span>)
function StatusBadge({ s }: { s?: EncryptionStatus }) {
  if (!s) return null
  return <span className={`badge ${ENC_BADGE[s.status] ?? 'badge-unknown'}`}>{ENC_LABEL[s.status] ?? s.status}</span>
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

  if (s.status === 'no_metadata') {
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
          <div><dt>Runtime key loaded</dt><dd>{yn(s.runtime_key_present)}</dd></div>
          <div><dt>Key length valid</dt><dd>{yn(s.runtime_key_length_valid)}</dd></div>
          <div><dt>Stored fingerprint exists</dt><dd>{yn(s.stored_fingerprint_present)}</dd></div>
          <div><dt>Fingerprint match</dt><dd>{s.fingerprint_match ? <span className="badge badge-up">match</span> : <span className="badge badge-down">no match</span>}</dd></div>
          <div style={{ gridColumn: '1 / -1' }}><dt>Status reason</dt><dd style={{ fontWeight: 400 }}>{s.reason}</dd></div>
          <div style={{ gridColumn: '1 / -1' }}><dt>SHA-256 Fingerprint</dt><dd className="mono" style={{ wordBreak: 'break-all' }}>{s.fingerprint || '—'}</dd></div>
          <div><dt>Key ID</dt><dd className="mono">{s.key_id || '—'}</dd></div>
          <div><dt>Created</dt><dd>{s.created_at ? timeAgo(s.created_at) : '—'}</dd></div>
          <div><dt>Last rotation</dt><dd>{s.last_rotation_at ? timeAgo(s.last_rotation_at) : 'never'}</dd></div>
          <div><dt>Last validation</dt><dd>{s.last_validation_at ? timeAgo(s.last_validation_at) : 'never'}</dd></div>
        </dl>
        <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>The encryption key is never stored or displayed — only this one-way fingerprint identifies it.</p>
      </Panel>
      <Panel title="System Startup Checklist" icon={ListChecks}>
        <StartupChecklist />
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

// UnlockPanel loads an EXISTING key into the running API immediately (no
// restart). This is the "I already have a key" path. The key lives only in
// process memory — never persisted/logged/returned. If the key doesn't match
// the stored fingerprint, the operator can explicitly adopt it as the baseline.
function UnlockPanel({ onUnlocked }: { onUnlocked?: () => void }) {
  const qc = useQueryClient()
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [res, setRes] = useState<EncryptionUnlockResult | null>(null)
  const inv = () => {
    qc.invalidateQueries({ queryKey: ['enc-status'] })
    qc.invalidateQueries({ queryKey: ['startup-checklist'] })
    qc.invalidateQueries({ queryKey: ['enc-reentry'] })
  }
  const submit = async (adopt: boolean) => {
    if (!key.trim()) return
    setBusy(true)
    try {
      const r = await unlockEncryption(key.trim(), adopt)
      setRes(r)
      if (r.ok && r.status === 'enabled') { setKey(''); inv(); onUnlocked?.() }
    } catch (e) {
      setRes({ ok: false, status: 'invalid_key', detail: (e as Error).message })
    } finally {
      setBusy(false)
    }
  }
  const mismatch = res && !res.ok && res.status === 'fingerprint_mismatch' && res.can_adopt
  return (
    <Panel title="Load Existing Key (Unlock)" icon={KeyRound}>
      <p className="muted" style={{ marginBottom: 12 }}>
        Paste the encryption key you already have to activate encryption in the running API <b>immediately — no restart and no environment editing needed</b>. The key is loaded into memory only: it is never stored, logged, or shown again. So it survives a future process restart, also set <code>HIMS_ENCRYPTION_KEY</code> to the same value in your deployment environment (see the Deployment step).
      </p>
      <div className="row" style={{ alignItems: 'center' }}>
        <input className="field" type="password" autoComplete="off" spellCheck={false} style={{ width: 380, fontFamily: 'var(--font-mono, monospace)' }} value={key} onChange={(e) => setKey(e.target.value)} placeholder="base64-encoded 32-byte key" />
        <button className="btn btn-primary" disabled={!key.trim() || busy} onClick={() => submit(false)}><KeyRound size={15} /> {busy ? 'Unlocking…' : 'Unlock now'}</button>
      </div>
      {res && res.ok && res.status === 'enabled' && (
        <div className="enc-banner info" style={{ marginTop: 12 }}><CircleCheck size={16} /> <span>{res.adopted ? 'Key adopted as the new baseline. ' : ''}{res.detail}</span></div>
      )}
      {res && !res.ok && res.status === 'invalid_key' && (
        <div className="enc-banner crit" style={{ marginTop: 12 }}><TriangleAlert size={16} /> <span>{res.detail}</span></div>
      )}
      {mismatch && (
        <div className="enc-banner warn" style={{ marginTop: 12 }}>
          <TriangleAlert size={16} />
          <div className="stack" style={{ gap: 8 }}>
            <span>{res!.detail}</span>
            <div className="muted" style={{ fontSize: 12, fontFamily: 'var(--font-mono, monospace)' }}>
              <div>This key&apos;s fingerprint: {res!.runtime_fingerprint?.slice(0, 23)}…</div>
              <div>Stored fingerprint:&nbsp;&nbsp;{res!.stored_fingerprint?.slice(0, 23)}…</div>
            </div>
            <div className="row">
              <button className="btn btn-danger btn-xs" disabled={busy} onClick={() => { if (confirm('Adopt this key as the new baseline? Any credentials sealed with the previous key will then need their secret re-entered.')) submit(true) }}>Adopt this key as the baseline</button>
            </div>
          </div>
        </div>
      )}
    </Panel>
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

      {!s?.enabled && <UnlockPanel />}

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

const STEPS = ['Status', 'Encryption Key', 'Deployment', 'Restart & Validate']
const restartHint: Record<DeployMode, string> = {
  local: 'Re-run the API start command from the Deployment step.',
  windows: 'Run: Restart-Service HIMS-API  (then Get-Service HIMS-API to confirm).',
  docker: 'Run: docker compose restart hims-api',
  cloud: 'Restart / redeploy the API from your hosting platform after setting the variable.',
}

function SetupWizard({ s }: { s?: EncryptionStatus }) {
  const qc = useQueryClient()
  const [step, setStep] = useState(0)
  const [mode, setMode] = useState<DeployMode | null>(null)
  const [reveal, setReveal] = useState<KeyReveal | null>(null)
  const [keyChoice, setKeyChoice] = useState<'generate' | 'have' | null>(s?.enabled ? 'have' : null)
  const [msg, setMsg] = useState('')
  const inv = () => { qc.invalidateQueries({ queryKey: ['enc-status'] }); qc.invalidateQueries({ queryKey: ['startup-checklist'] }) }

  const generate = useMutation({ mutationFn: () => api.post<KeyReveal>('/security/encryption/generate', {}), onSuccess: (d) => { setReveal(d as KeyReveal); setKeyChoice('generate'); inv() }, onError: (e) => setMsg((e as Error).message) })
  const validate = useMutation({ mutationFn: () => api.post<{ status: string; detail: string }>('/security/encryption/validate', {}), onSuccess: (d) => { setMsg(`Validation: ${(d as { status: string }).status} — ${(d as { detail: string }).detail}`); inv() }, onError: (e) => setMsg((e as Error).message) })

  const allEnabled = s?.status === 'enabled' && (s?.undecryptable_count ?? 0) === 0
  const next = () => setStep((n) => Math.min(STEPS.length - 1, n + 1))
  const back = () => setStep((n) => Math.max(0, n - 1))

  return (
    <div className="stack">
      {reveal && <KeyReveal data={reveal} onClose={() => { setReveal(null); inv() }} />}

      <div className="stepper">
        {STEPS.map((label, i) => (
          <div key={label} className={'stepper-step' + (i === step ? ' active' : i < step ? ' done' : '')}>
            <span className="step-num">{i < step ? '✓' : i + 1}</span> {label}
          </div>
        ))}
      </div>

      {step === 0 && (
        <Panel title="Where things stand" icon={ShieldCheck}>
          {s && !s.enabled && (
            <div className={'enc-banner ' + (s.status === 'pending_restart' || s.status === 'no_metadata' ? 'warn' : 'crit')} style={{ marginBottom: 14 }}>
              <TriangleAlert size={16} /> <span><b>Action required:</b> {s.reason}</span>
            </div>
          )}
          {allEnabled && <div className="enc-banner info" style={{ marginBottom: 14 }}><CircleCheck size={16} /> <span>Encryption is configured and healthy. You can still use this wizard to review deployment steps or rotate later.</span></div>}
          <StartupChecklist />
          <div className="row" style={{ marginTop: 16 }}><button className="btn btn-primary" onClick={next}>Begin setup →</button></div>
        </Panel>
      )}

      {step === 1 && (
        <Panel title="Encryption Key" icon={KeyRound}>
          {s?.enabled ? (
            <div className="enc-banner info"><CircleCheck size={16} /> <span>A key is already active (fingerprint {s.fingerprint.slice(0, 23)}…). To replace it, use Key Management → Rotate. Continue to review deployment.</span></div>
          ) : (
            <>
              <p className="muted" style={{ marginBottom: 12 }}>Choose how to provide the encryption key. It is shown only once at generation — save it immediately.</p>
              <div className="grid-2">
                <div className="card" style={{ margin: 0 }}>
                  <h3 style={{ fontSize: 15 }}>Generate a new key</h3>
                  <p className="muted" style={{ fontSize: 13, margin: '6px 0 12px' }}>Best for a fresh install with no existing encrypted secrets.</p>
                  <button className="btn btn-primary" disabled={generate.isPending} onClick={() => generate.mutate()}><KeyRound size={15} /> Generate key</button>
                </div>
                <div className="card" style={{ margin: 0 }}>
                  <h3 style={{ fontSize: 15 }}>I already have a key</h3>
                  <p className="muted" style={{ fontSize: 13, margin: '6px 0 12px' }}>Restoring a server or migrating? Paste your existing key to activate encryption right now — no restart needed.</p>
                  <button className={'btn' + (keyChoice === 'have' ? ' btn-primary' : '')} onClick={() => setKeyChoice('have')}>Use existing key</button>
                </div>
              </div>
              {keyChoice === 'have' && <div style={{ marginTop: 14 }}><UnlockPanel onUnlocked={() => setMsg('Encryption is now active. Continue to the Deployment step to make the key persist across restarts.')} /></div>}
              {keyChoice === 'generate' && <div className="enc-banner warn" style={{ marginTop: 14 }}><TriangleAlert size={16} /> <span>Key generated. Make sure you saved the recovery file — it cannot be shown again. It's also re-downloadable from the reveal dialog only while open.</span></div>}
            </>
          )}
          {msg && <div className="muted" style={{ fontSize: 12, marginTop: 10 }}>{msg}</div>}
          <div className="row" style={{ marginTop: 16 }}>
            <button className="btn btn-ghost" onClick={back}>← Back</button>
            <button className="btn btn-primary" disabled={!keyChoice && !s?.enabled} onClick={next}>Next →</button>
          </div>
        </Panel>
      )}

      {step === 2 && (
        <Panel title="Configure your deployment" icon={Rocket}>
          <p className="muted" style={{ marginBottom: 12 }}>Pick how HIMS is hosted — we'll show the exact steps to set the key and restart.</p>
          <div className="deploy-modes">
            {DEPLOY_MODES.map((m) => (
              <button key={m.key} className={'deploy-mode' + (mode === m.key ? ' active' : '')} onClick={() => setMode(m.key)}>
                <span className="dm-ico"><m.icon size={20} /></span>
                <span><b>{m.label}</b><small>{m.blurb}</small></span>
              </button>
            ))}
          </div>
          {mode && (
            <div style={{ marginTop: 16 }}>
              {deploymentSteps(mode).map((sec, i) => (
                <div key={i} style={{ marginBottom: 10 }}>
                  <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 2 }}>{sec.title}</div>
                  <CmdBlock lines={sec.lines} />
                </div>
              ))}
              <div className="row" style={{ marginTop: 8 }}>
                <DownloadBtn filename={`hims-${mode}-runbook.txt`} text={buildRunbook(mode)} label="Download deployment runbook" />
                <DownloadBtn filename="hims-disaster-recovery-runbook.txt" text={buildRunbook('disaster')} label="Disaster recovery runbook" />
              </div>
              <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>Replace <code>&lt;PASTE-YOUR-RECOVERY-KEY&gt;</code> with the key from your recovery file. The key is never sent to the browser or stored here.</p>
            </div>
          )}
          <div className="row" style={{ marginTop: 16 }}>
            <button className="btn btn-ghost" onClick={back}>← Back</button>
            <button className="btn btn-primary" disabled={!mode} onClick={next}>Next →</button>
          </div>
        </Panel>
      )}

      {step === 3 && (
        <Panel title="Restart & Validate" icon={CircleCheck}>
          <p className="muted" style={{ marginBottom: 10 }}>The API only loads the key at startup, so it must be restarted after you set <code>HIMS_ENCRYPTION_KEY</code>.</p>
          {mode && <div className="enc-banner info" style={{ marginBottom: 14 }}><RefreshCw size={16} /> <span>{restartHint[mode]}</span></div>}
          {allEnabled ? (
            <div className="enc-banner info"><CircleCheck size={16} /> <span><b>Encryption is configured and validated.</b> Credential storage, writes and discovery credential access are enabled.</span></div>
          ) : (
            <div className="enc-banner warn"><TriangleAlert size={16} /> <span>Not yet active. After restarting, click “Re-check” below. This wizard reports the real status — it won't show success until the API confirms the key is loaded.</span></div>
          )}
          <div className="row" style={{ margin: '14px 0' }}>
            <button className="btn" onClick={inv}><RefreshCw size={15} /> Re-check status</button>
            <button className="btn" disabled={!s?.enabled || validate.isPending} onClick={() => validate.mutate()}><ShieldCheck size={15} /> Validate encryption</button>
          </div>
          <StartupChecklist />
          {msg && <div className="muted" style={{ fontSize: 12, marginTop: 10 }}>{msg}</div>}
          <div className="row" style={{ marginTop: 16 }}><button className="btn btn-ghost" onClick={back}>← Back</button></div>
        </Panel>
      )}
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
