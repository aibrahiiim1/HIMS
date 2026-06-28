import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { X, Check, AlertTriangle, Loader2 } from 'lucide-react'
import { api, type Credential } from '../api'

// ---- registry types (mirror internal/api/onboarding_registry.go) ----
interface OnbField { key: string; label: string; type: string; required: boolean; placeholder?: string; help?: string; default?: string; options?: string[] }
interface OnbMethod { key: string; label: string; credential_kind: string; default_port: number; test_kind: string; collector_ready: boolean; note?: string }
interface OnbCapability { key: string; label: string; status: string; reason?: string }
interface OnbType { type: string; category: string; subtype: string; display_name: string; add_label: string; group: string; vendors?: string[]; methods: OnbMethod[]; base_fields: OnbField[]; capabilities?: OnbCapability[]; notes?: string }
interface TestStep { step: string; status: string; detail: string }
interface TestResp { steps: TestStep[]; final_status: string; category: string; summary: string }

const STEPS = ['Device Type', 'Network Address', 'Connection Method', 'Credential', 'Test Connection', 'Review & Save']

const stepTone = (s: string) => (s === 'ok' ? 'var(--ok)' : s === 'fail' ? 'var(--crit)' : 'var(--text-muted)')
const PASS_STATES = new Set(['authenticated', 'managed'])

// OnboardingWizard is the single, registry-driven manual-add wizard used by every inventory
// page's "Add <Type>" action. It renders entirely from GET /manual-onboarding/device-types,
// runs a real protocol Test Connection (POST /manual-onboarding/test), and saves via
// POST /manual-onboarding/save (create-or-override-by-IP, classify+lock, audit). It NEVER
// claims managed: a failed/absent test can only be saved as manual inventory only.
export function OnboardingWizard({ defaultType, onClose }: { defaultType?: string; onClose: () => void }) {
  const qc = useQueryClient()
  const cat = useQuery({ queryKey: ['onboarding-types'], queryFn: () => api.get<OnbType[]>('/manual-onboarding/device-types') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  const [typeKey, setTypeKey] = useState(defaultType ?? '')
  const [step, setStep] = useState(defaultType ? 1 : 0)
  const [vals, setVals] = useState<Record<string, string>>({})
  const [methodKey, setMethodKey] = useState('')
  const [credMode, setCredMode] = useState<'existing' | 'new' | 'none'>('existing')
  const [credId, setCredId] = useState('')
  const [newCredName, setNewCredName] = useState('')
  const [newCredSecret, setNewCredSecret] = useState('')
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<TestResp | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveMsg, setSaveMsg] = useState('')
  const [saveManual, setSaveManual] = useState(false)
  const [runCollection, setRunCollection] = useState(true)

  const t = useMemo(() => (cat.data ?? []).find((x) => x.type === typeKey), [cat.data, typeKey])
  const method = useMemo(() => t?.methods.find((m) => m.key === methodKey), [t, methodKey])
  const credKind = method?.credential_kind ?? ''
  const needsCred = !!credKind
  const set = (k: string, v: string) => setVals((p) => ({ ...p, [k]: v }))

  const buildCredential = () => {
    if (!needsCred) return {}
    if (credMode === 'existing' && credId) return { id: credId }
    if (credMode === 'new' && newCredSecret) return { kind: credKind, secret: newCredSecret, name: newCredName }
    return {}
  }

  const runTest = async () => {
    if (!t || !method) return
    setTesting(true); setTest(null)
    try {
      const r = await api.post<TestResp>('/manual-onboarding/test', {
        type: t.type, primary_ip: vals.primary_ip, method: method.key,
        port: vals.port ? Number(vals.port) : 0, credential: buildCredential(),
      })
      setTest(r)
    } catch (e) { setTest({ steps: [{ step: 'error', status: 'fail', detail: (e as Error).message }], final_status: 'error', category: 'error', summary: (e as Error).message }) }
    setTesting(false)
  }

  const testPassed = !!test && PASS_STATES.has(test.final_status)
  const canSave = !!t && !!vals.primary_ip && (testPassed || saveManual)

  const save = async () => {
    if (!t || !method) return
    setSaving(true); setSaveMsg('')
    try {
      const r = await api.post<{ device_id: string; existing: boolean; state: string; message: string; collection_run: boolean }>('/manual-onboarding/save', {
        type: t.type, method: method.key, port: vals.port ? Number(vals.port) : 0,
        primary_ip: vals.primary_ip, name: vals.name, vendor: vals.vendor, model: vals.model,
        location: vals.location, criticality: vals.criticality, manual_classification_reason: vals.manual_classification_reason,
        notes: vals.notes, credential: buildCredential(), test_passed: testPassed,
        save_as_manual_inventory: saveManual && !testPassed, run_collection: runCollection,
      })
      qc.invalidateQueries({ queryKey: ['group-inventory'] }); qc.invalidateQueries({ queryKey: ['inventory-bmc'] }); qc.invalidateQueries({ queryKey: ['device-category-counts'] })
      setSaveMsg(`✓ ${r.existing ? 'Updated existing device (manual override)' : 'Device created'} — ${r.state}. ${r.message}`)
      setTimeout(onClose, 1800)
    } catch (e) { setSaveMsg('✗ ' + (e as Error).message) }
    setSaving(false)
  }

  const field = (f: OnbField) => (
    <label key={f.key} style={{ display: 'block', marginBottom: 10 }}>
      <span style={{ fontSize: 12, fontWeight: 600 }}>{f.label}{f.required && <span style={{ color: 'var(--crit)' }}> *</span>}</span>
      {f.type === 'select' ? (
        <select value={vals[f.key] ?? f.default ?? ''} onChange={(e) => set(f.key, e.target.value)} style={inp}>
          <option value="">—</option>
          {(f.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      ) : f.type === 'textarea' ? (
        <textarea value={vals[f.key] ?? ''} onChange={(e) => set(f.key, e.target.value)} placeholder={f.placeholder} style={{ ...inp, minHeight: 56 }} />
      ) : (
        <input type={f.type === 'number' ? 'number' : 'text'} value={vals[f.key] ?? f.default ?? ''} onChange={(e) => set(f.key, e.target.value)} placeholder={f.placeholder} style={inp} />
      )}
      {f.help && <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{f.help}</span>}
    </label>
  )

  return (
    <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', justifyContent: 'center', alignItems: 'flex-start', paddingTop: 40, zIndex: 1000 }} onClick={onClose}>
      <div style={{ width: 620, maxWidth: '95%', maxHeight: '90vh', overflowY: 'auto', background: 'var(--surface)', borderRadius: 10, boxShadow: '0 12px 48px rgba(0,0,0,.4)' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
          <b>{t ? t.add_label : 'Add Device'}</b>
          <button className="btn btn-ghost btn-xs" onClick={onClose}><X size={16} /></button>
        </div>

        {/* stepper */}
        <div style={{ display: 'flex', gap: 6, padding: '10px 18px', flexWrap: 'wrap', borderBottom: '1px solid var(--border)' }}>
          {STEPS.map((s, i) => (
            <span key={s} style={{ fontSize: 11, padding: '3px 8px', borderRadius: 12, background: i === step ? 'var(--accent,#3b82f6)' : 'var(--bg,#1116)', color: i === step ? '#fff' : 'var(--text-muted)' }}>{i + 1}. {s}</span>
          ))}
        </div>

        <div style={{ padding: 18 }}>
          {cat.isLoading && <div className="loading">Loading device types…</div>}

          {/* Step 0: Device Type */}
          {step === 0 && cat.data && (
            <div>
              <p className="muted" style={{ fontSize: 12 }}>Choose the device type to add. Each type drives its own fields, connection methods, and credential types.</p>
              <select value={typeKey} onChange={(e) => setTypeKey(e.target.value)} style={inp}>
                <option value="">— select —</option>
                {cat.data.map((x) => <option key={x.type} value={x.type}>{x.display_name}</option>)}
              </select>
              {t?.notes && <div className="banner" style={{ fontSize: 12, marginTop: 10 }}>{t.notes}</div>}
            </div>
          )}

          {/* Step 1: Network Address + base fields */}
          {step === 1 && t && (
            <div>{t.base_fields.filter((f) => f.key !== 'port').map(field)}</div>
          )}

          {/* Step 2: Connection Method */}
          {step === 2 && t && (
            <div>
              {t.methods.map((m) => (
                <label key={m.key} style={{ display: 'flex', gap: 8, alignItems: 'flex-start', padding: '8px 10px', border: `1px solid ${methodKey === m.key ? 'var(--accent,#3b82f6)' : 'var(--border)'}`, borderRadius: 8, marginBottom: 8, cursor: 'pointer' }}>
                  <input type="radio" name="method" checked={methodKey === m.key} onChange={() => setMethodKey(m.key)} />
                  <span>
                    <b style={{ fontSize: 13 }}>{m.label}</b> <span className="muted" style={{ fontSize: 11 }}>· port {m.default_port || '—'} · {m.collector_ready ? 'collector ready' : 'identity only'}</span>
                    {m.note && <div style={{ fontSize: 11, color: 'var(--warn)' }}>{m.note}</div>}
                  </span>
                </label>
              ))}
              {t.base_fields.filter((f) => f.key === 'port').map(field)}
            </div>
          )}

          {/* Step 3: Credential */}
          {step === 3 && t && (
            <div>
              {!needsCred ? (
                <div className="banner" style={{ fontSize: 12 }}>This method needs no credential (manual inventory / anonymous). Continue.</div>
              ) : (
                <>
                  <div style={{ display: 'flex', gap: 12, marginBottom: 10, fontSize: 13 }}>
                    <label><input type="radio" checked={credMode === 'existing'} onChange={() => setCredMode('existing')} /> Existing</label>
                    <label><input type="radio" checked={credMode === 'new'} onChange={() => setCredMode('new')} /> Create new</label>
                  </div>
                  {credMode === 'existing' ? (
                    <select value={credId} onChange={(e) => setCredId(e.target.value)} style={inp}>
                      <option value="">— select a {credKind} credential —</option>
                      {(creds.data ?? []).filter((c) => c.kind === credKind).map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
                    </select>
                  ) : (
                    <>
                      <input placeholder="credential name" value={newCredName} onChange={(e) => setNewCredName(e.target.value)} style={inp} />
                      <input placeholder={credKind.startsWith('snmp') ? 'community string' : 'username:password'} value={newCredSecret} onChange={(e) => setNewCredSecret(e.target.value)} style={inp} />
                      <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>Encrypted at rest (AES-256-GCM); never shown back. {credKind.startsWith('snmp') ? 'SNMP community.' : "Must be 'username:password' with a non-empty password."}</span>
                    </>
                  )}
                </>
              )}
            </div>
          )}

          {/* Step 4: Test Connection */}
          {step === 4 && t && (
            <div>
              <button className="btn btn-primary btn-sm" onClick={runTest} disabled={testing || !vals.primary_ip || !methodKey}>
                {testing ? <Loader2 size={14} className="spin" /> : 'Run Test Connection'}
              </button>
              {test && (
                <div style={{ marginTop: 12 }}>
                  {test.steps.map((s, i) => (
                    <div key={i} style={{ display: 'flex', gap: 8, fontSize: 13, padding: '3px 0' }}>
                      {s.status === 'ok' ? <Check size={15} color="var(--ok)" /> : s.status === 'fail' ? <X size={15} color="var(--crit)" /> : <span style={{ width: 15 }} />}
                      <b style={{ color: stepTone(s.status), minWidth: 90 }}>{s.step}</b><span className="muted">{s.detail}</span>
                    </div>
                  ))}
                  <div style={{ marginTop: 10, fontSize: 13 }}>
                    Result: <span className={`badge badge-${testPassed ? 'up' : 'warning'}`}>{test.final_status}</span>
                    {!testPassed && <span className="muted" style={{ marginLeft: 8 }}>not managed — you can save as manual inventory only.</span>}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Step 5: Review & Save */}
          {step === 5 && t && (
            <div style={{ fontSize: 13 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', gap: 4 }}>
                <span className="muted">Type</span><b>{t.display_name}</b>
                <span className="muted">IP</span><b>{vals.primary_ip || '—'}</b>
                <span className="muted">Method</span><b>{method?.label || '—'}</b>
                <span className="muted">Test result</span><b>{test?.final_status || 'not tested'}</b>
              </div>
              <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 6 }}>
                <div className="banner" style={{ fontSize: 12, display: 'flex', gap: 6 }}><AlertTriangle size={14} /> Manual classification will LOCK this device's type — future scans won't overwrite it (volatile fields still update).</div>
                <div className="banner" style={{ fontSize: 12 }}>If this IP already exists from a scan, it will be UPDATED (manual override), not duplicated — discovery evidence is preserved.</div>
                {testPassed
                  ? <label style={{ fontSize: 13 }}><input type="checkbox" checked={runCollection} onChange={(e) => setRunCollection(e.target.checked)} /> Run collection now (management state will reflect the result)</label>
                  : <label style={{ fontSize: 13, color: 'var(--warn)' }}><input type="checkbox" checked={saveManual} onChange={(e) => setSaveManual(e.target.checked)} /> Test did not authenticate — save as <b>manual inventory only</b> (operator-asserted type, NOT managed)</label>}
              </div>
              {saveMsg && <div className="banner" style={{ marginTop: 10, fontSize: 12 }}>{saveMsg}</div>}
            </div>
          )}
        </div>

        {/* footer nav */}
        <div style={{ display: 'flex', justifyContent: 'space-between', padding: '12px 18px', borderTop: '1px solid var(--border)' }}>
          <button className="btn btn-ghost btn-sm" onClick={() => setStep((s) => Math.max(0, s - 1))} disabled={step === 0}>Back</button>
          {step < 5 ? (
            <button className="btn btn-primary btn-sm" onClick={() => setStep((s) => s + 1)}
              disabled={(step === 0 && !typeKey) || (step === 1 && !vals.primary_ip) || (step === 2 && !methodKey)}>Next</button>
          ) : (
            <button className="btn btn-primary btn-sm" onClick={save} disabled={!canSave || saving}>{saving ? 'Saving…' : 'Save device'}</button>
          )}
        </div>
      </div>
    </div>
  )
}

const inp: React.CSSProperties = { display: 'block', width: '100%', padding: '7px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, background: 'var(--bg,#0d1117)', color: 'inherit', marginTop: 3 }
