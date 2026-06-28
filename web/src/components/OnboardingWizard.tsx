import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { X, Check, AlertTriangle, Loader2, ChevronDown, ChevronRight, Settings2 } from 'lucide-react'
import { api, type Credential } from '../api'

// ---- registry types (mirror internal/api/onboarding_registry.go) ----
interface OnbField { key: string; label: string; type: string; required: boolean; placeholder?: string; help?: string; default?: string; options?: string[] }
interface OnbMethod { key: string; label: string; credential_kind: string; default_port: number; test_kind: string; collector_ready: boolean; status: string; note?: string }
interface OnbCapability { key: string; label: string; status: string; reason?: string }
interface OnbType { type: string; category: string; subtype: string; display_name: string; add_label: string; group: string; vendors?: string[]; methods: OnbMethod[]; base_fields: OnbField[]; capabilities?: OnbCapability[]; notes?: string }
interface TestStep { step: string; status: string; detail: string }
interface TestResp { steps: TestStep[]; final_status: string; category: string; summary: string }
interface IpLookup { exists: boolean; device_id?: string; name?: string; category?: string; subtype?: string; classification_locked?: boolean }

// A test "passes" (managed-capable save) when a real protocol authentication succeeded.
const PASS_STATES = new Set(['authenticated', 'managed', 'implemented_collected', 'implemented_tested'])

// Map the backend final_status to a SIMPLE operator-facing result + tone. Protocol step detail
// stays available behind an expander.
type Tone = 'up' | 'warning' | 'down' | 'unknown'
const RESULT_UI: Record<string, { label: string; tone: Tone }> = {
  implemented_collected: { label: 'Connected', tone: 'up' },
  implemented_tested: { label: 'Connected', tone: 'up' },
  authenticated: { label: 'Connected', tone: 'up' },
  managed: { label: 'Connected', tone: 'up' },
  web_reachable: { label: 'Reachable only', tone: 'warning' },
  auth_failed: { label: 'Auth failed', tone: 'down' },
  credential_failed: { label: 'Auth failed', tone: 'down' },
  zkteco_comm_key_required: { label: 'Auth failed — comm key required', tone: 'down' },
  credential_required: { label: 'Credential required', tone: 'warning' },
  unreachable: { label: 'Not reachable', tone: 'down' },
  protocol_not_supported: { label: 'Protocol not supported', tone: 'down' },
  manual_inventory_only: { label: 'Manual inventory only', tone: 'unknown' },
  error: { label: 'Error', tone: 'down' },
}
const resultUI = (s: string): { label: string; tone: Tone } => RESULT_UI[s] ?? { label: s.replace(/_/g, ' '), tone: 'warning' }
const statusTone = (s: string): Tone => (s === 'implemented_collected' ? 'up' : s === 'external_dependency_required' || s === 'manual_inventory_only' ? 'unknown' : 'warning')

// Quick-Add core fields; everything else from the registry falls into Advanced.
const QUICK_KEYS = new Set(['primary_ip', 'name', 'vendor'])

// OnboardingWizard is the single, registry-driven manual-add wizard used by every inventory
// page's "Add <Type>" action. It renders entirely from GET /manual-onboarding/device-types,
// runs a real protocol Test Connection (POST /manual-onboarding/test), and saves via
// POST /manual-onboarding/save (create-or-override-by-IP, classify+lock, audit). It NEVER
// claims managed: a failed/absent test can only be saved as manual inventory only.
//
// UX: a single clean modal with three levels — (1) Quick Add (the everyday fields), (2) a
// collapsed Advanced section (ports/model/location/notes/classification/collection), and
// (3) a live Review summary before Save. No multi-step wall.
export function OnboardingWizard({ defaultType, onClose }: { defaultType?: string; onClose: () => void }) {
  const qc = useQueryClient()
  const cat = useQuery({ queryKey: ['onboarding-types'], queryFn: () => api.get<OnbType[]>('/manual-onboarding/device-types') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  const [typeKey, setTypeKey] = useState(defaultType ?? '')
  const [changingType, setChangingType] = useState(!defaultType)
  const [vals, setVals] = useState<Record<string, string>>({})
  const [methodKey, setMethodKey] = useState('')
  const [credMode, setCredMode] = useState<'existing' | 'new'>('existing')
  const [credId, setCredId] = useState('')
  const [newCredName, setNewCredName] = useState('')
  const [newCredSecret, setNewCredSecret] = useState('')
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<TestResp | null>(null)
  const [showSteps, setShowSteps] = useState(false)
  const [advOpen, setAdvOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveMsg, setSaveMsg] = useState('')
  const [saveManual, setSaveManual] = useState(false)
  const [runCollection, setRunCollection] = useState(true)
  const [existing, setExisting] = useState<IpLookup | null>(null)

  const t = useMemo(() => (cat.data ?? []).find((x) => x.type === typeKey), [cat.data, typeKey])
  const method = useMemo(() => t?.methods.find((m) => m.key === methodKey), [t, methodKey])
  const credKind = method?.credential_kind ?? ''
  const needsCred = !!credKind
  const isZK = method?.test_kind === 'zkteco'
  const set = (k: string, v: string) => setVals((p) => ({ ...p, [k]: v }))

  // Auto-select the first connection method whenever the type changes — Quick Add stays fast.
  useEffect(() => {
    if (t && !t.methods.some((m) => m.key === methodKey)) setMethodKey(t.methods[0]?.key ?? '')
    setTest(null)
  }, [t]) // eslint-disable-line react-hooks/exhaustive-deps

  // Existing-IP lookup (debounced) → drives the override warning + Review action line.
  useEffect(() => {
    const ip = (vals.primary_ip ?? '').trim()
    if (!ip) { setExisting(null); return }
    const h = setTimeout(() => { api.get<IpLookup>(`/manual-onboarding/lookup?ip=${encodeURIComponent(ip)}`).then(setExisting).catch(() => setExisting(null)) }, 350)
    return () => clearTimeout(h)
  }, [vals.primary_ip])

  const buildCredential = () => {
    if (isZK) return newCredSecret ? { secret: newCredSecret } : {} // ZK comm key (optional, in-memory only unless non-default)
    if (!needsCred) return {}
    if (credMode === 'existing' && credId) return { id: credId }
    if (credMode === 'new' && newCredSecret) return { kind: credKind, secret: newCredSecret, name: newCredName }
    return {}
  }

  const runTest = async () => {
    if (!t || !method) return
    setTesting(true); setTest(null); setShowSteps(false)
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
  const canSave = !!t && !!vals.primary_ip && !!methodKey && (testPassed || saveManual)

  // Honest final-state preview for the Review card.
  const finalState = testPassed
    ? (runCollection ? 'Managed — collection will run now' : 'Managed-capable — collect later from the device page')
    : saveManual ? 'Manual inventory only (NOT managed)' : '— run a successful test, or tick manual inventory'
  const credSummary = isZK ? (newCredSecret ? 'ZKTeco comm key (supplied)' : 'ZKTeco default key (no secret)')
    : !needsCred ? 'None needed'
      : credMode === 'existing' ? (creds.data?.find((c) => c.id === credId)?.name ?? 'existing credential (none selected)')
        : (newCredSecret ? `new ${credKind} credential` : `new ${credKind} (secret empty)`)

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
      setTimeout(onClose, 1900)
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
        <textarea value={vals[f.key] ?? ''} onChange={(e) => set(f.key, e.target.value)} placeholder={f.placeholder} style={{ ...inp, minHeight: 52 }} />
      ) : (
        <input type={f.type === 'number' ? 'number' : 'text'} value={vals[f.key] ?? f.default ?? ''} onChange={(e) => set(f.key, e.target.value)} placeholder={f.placeholder} style={inp} />
      )}
      {f.help && <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{f.help}</span>}
    </label>
  )

  const quickFields = (t?.base_fields ?? []).filter((f) => QUICK_KEYS.has(f.key) && f.key !== 'primary_ip')
  const advFields = (t?.base_fields ?? []).filter((f) => !QUICK_KEYS.has(f.key))
  const res = test ? resultUI(test.final_status) : null

  return (
    <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', justifyContent: 'center', alignItems: 'flex-start', paddingTop: 36, zIndex: 1000 }} onClick={onClose}>
      <div style={{ width: 600, maxWidth: '95%', maxHeight: '90vh', overflowY: 'auto', background: 'var(--surface)', borderRadius: 10, boxShadow: '0 12px 48px rgba(0,0,0,.4)' }} onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 18px', borderBottom: '1px solid var(--border)', position: 'sticky', top: 0, background: 'var(--surface)', zIndex: 2 }}>
          <b>{t ? t.add_label : 'Add Device'}</b>
          <button className="btn btn-ghost btn-xs" onClick={onClose}><X size={16} /></button>
        </div>

        <div style={{ padding: 18 }}>
          {cat.isLoading && <div className="loading" style={{ display: 'flex', gap: 8, alignItems: 'center' }}><Loader2 size={15} className="spin" /> Loading device types…</div>}
          {cat.isError && <div className="banner" style={{ fontSize: 12, color: 'var(--crit)' }}>Could not load device types. <button className="btn btn-ghost btn-xs" onClick={() => cat.refetch()}>Retry</button></div>}

          {cat.data && (
            <>
              {/* ---- Device Type (pre-selected; compact when opened from a page button) ---- */}
              <div style={{ marginBottom: 14 }}>
                <span style={{ fontSize: 12, fontWeight: 600 }}>Device Type</span>
                {t && !changingType ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 3 }}>
                    <span className="badge badge-up" style={{ fontSize: 12 }}>{t.display_name}</span>
                    <button className="btn btn-ghost btn-xs" onClick={() => setChangingType(true)}>change</button>
                  </div>
                ) : (
                  <select value={typeKey} onChange={(e) => { setTypeKey(e.target.value); setChangingType(false) }} style={inp} autoFocus={!defaultType}>
                    <option value="">— select —</option>
                    {cat.data.map((x) => <option key={x.type} value={x.type}>{x.display_name}</option>)}
                  </select>
                )}
                {t?.notes && <div className="banner" style={{ fontSize: 11.5, marginTop: 8 }}>{t.notes}</div>}
              </div>

              {!t ? (
                <div className="muted" style={{ fontSize: 12 }}>Pick a device type to begin. Each type drives its own fields, connection methods, and credential types.</div>
              ) : (
                <>
                  {/* ================= 1) QUICK ADD ================= */}
                  <label style={{ display: 'block', marginBottom: 10 }}>
                    <span style={{ fontSize: 12, fontWeight: 600 }}>IP Address<span style={{ color: 'var(--crit)' }}> *</span></span>
                    <input value={vals.primary_ip ?? ''} onChange={(e) => set('primary_ip', e.target.value)} placeholder="10.0.0.10" style={inp} autoFocus={!!defaultType} />
                  </label>
                  {existing?.exists && (
                    <div className="banner" style={{ fontSize: 11.5, marginBottom: 10, display: 'flex', gap: 6, borderLeft: '3px solid var(--warn)' }}>
                      <AlertTriangle size={14} style={{ flexShrink: 0, marginTop: 1 }} />
                      <span>This IP already exists (<b>{existing.name}</b> · {existing.category}{existing.subtype ? `/${existing.subtype}` : ''}). Manual add will <b>update</b> the existing device, <b>lock</b> the classification, <b>preserve</b> discovery evidence, and <b>audit</b> the change — no duplicate.</span>
                    </div>
                  )}
                  {quickFields.map(field)}

                  {/* Connection Method */}
                  <label style={{ display: 'block', marginBottom: 6 }}>
                    <span style={{ fontSize: 12, fontWeight: 600 }}>Connection Method</span>
                    <select value={methodKey} onChange={(e) => { setMethodKey(e.target.value); setTest(null) }} style={inp}>
                      {t.methods.map((m) => <option key={m.key} value={m.key}>{m.label}{m.default_port ? ` · port ${m.default_port}` : ''}</option>)}
                    </select>
                  </label>
                  {method && (
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10, flexWrap: 'wrap' }}>
                      <span className={`badge badge-${statusTone(method.status)}`} style={{ fontSize: 10 }}>{method.status.replace(/_/g, ' ')}</span>
                      {method.note && <span className="muted" style={{ fontSize: 11 }}>{method.note}</span>}
                    </div>
                  )}

                  {/* Credential */}
                  <div style={{ marginBottom: 12 }}>
                    <span style={{ fontSize: 12, fontWeight: 600 }}>Credential</span>
                    {isZK ? (
                      <>
                        <input placeholder="Communication key (optional — blank uses the SDK default 0)" value={newCredSecret} onChange={(e) => setNewCredSecret(e.target.value)} style={inp} />
                        <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>Most ZKTeco devices use the default key — leave blank. A non-default key is stored encrypted (never shown back).</span>
                      </>
                    ) : !needsCred ? (
                      <div className="banner" style={{ fontSize: 11.5, marginTop: 4 }}>No credential needed (manual inventory / anonymous).</div>
                    ) : (
                      <>
                        <div style={{ display: 'flex', gap: 14, margin: '4px 0 6px', fontSize: 13 }}>
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

                  {/* Test Connection — simple result first, steps behind an expander */}
                  <div style={{ marginBottom: 12 }}>
                    <button className="btn btn-sm" onClick={runTest} disabled={testing || !vals.primary_ip || !methodKey} style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
                      {testing ? <><Loader2 size={14} className="spin" /> Testing…</> : 'Test Connection'}
                    </button>
                    {res && (
                      <div style={{ marginTop: 10 }}>
                        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                          <span className={`badge badge-${res.tone}`} style={{ fontSize: 12 }}>{res.tone === 'up' ? <Check size={12} style={{ marginRight: 3, verticalAlign: 'middle' }} /> : null}{res.label}</span>
                          {test?.summary && <span className="muted" style={{ fontSize: 11.5 }}>{test.summary}</span>}
                        </div>
                        {!testPassed && <div className="muted" style={{ fontSize: 11, marginTop: 4 }}>Not managed — you can still save as manual inventory below.</div>}
                        <button className="btn btn-ghost btn-xs" style={{ marginTop: 4, display: 'inline-flex', gap: 3, alignItems: 'center' }} onClick={() => setShowSteps((v) => !v)}>
                          {showSteps ? <ChevronDown size={13} /> : <ChevronRight size={13} />} Protocol details
                        </button>
                        {showSteps && (
                          <div style={{ marginTop: 6, padding: '8px 10px', background: 'var(--bg,#0d1117)', borderRadius: 6, border: '1px solid var(--border)' }}>
                            {(test?.steps ?? []).map((s, i) => (
                              <div key={i} style={{ display: 'flex', gap: 8, fontSize: 12.5, padding: '2px 0' }}>
                                {s.status === 'ok' ? <Check size={14} color="var(--ok)" /> : s.status === 'fail' ? <X size={14} color="var(--crit)" /> : <span style={{ width: 14 }} />}
                                <b style={{ minWidth: 84 }}>{s.step}</b><span className="muted">{s.detail}</span>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}
                  </div>

                  {/* ================= 2) ADVANCED (collapsed) ================= */}
                  <div style={{ borderTop: '1px solid var(--border)', paddingTop: 10, marginBottom: 12 }}>
                    <button className="btn btn-ghost btn-sm" onClick={() => setAdvOpen((v) => !v)} style={{ display: 'inline-flex', gap: 6, alignItems: 'center', padding: 0 }}>
                      {advOpen ? <ChevronDown size={15} /> : <ChevronRight size={15} />} <Settings2 size={14} /> Advanced options
                    </button>
                    {advOpen && (
                      <div style={{ marginTop: 10 }}>
                        {advFields.map(field)}
                        {t.capabilities && t.capabilities.length > 0 && (
                          <div style={{ fontSize: 11.5, marginTop: 4 }}>
                            <span className="muted">Capabilities: </span>
                            {t.capabilities.map((c) => <span key={c.key} className={`badge badge-${statusTone(c.status)}`} style={{ fontSize: 10, marginRight: 4 }}>{c.label}: {c.status.replace(/_/g, ' ')}</span>)}
                          </div>
                        )}
                        {testPassed && (
                          <label style={{ fontSize: 13, display: 'flex', gap: 6, alignItems: 'center', marginTop: 8 }}>
                            <input type="checkbox" checked={runCollection} onChange={(e) => setRunCollection(e.target.checked)} /> Run collection now (management state reflects the result)
                          </label>
                        )}
                      </div>
                    )}
                  </div>

                  {/* ================= 3) REVIEW & SAVE ================= */}
                  <div style={{ background: 'var(--bg,#0d1117)', border: '1px solid var(--border)', borderRadius: 8, padding: '10px 12px', fontSize: 12.5 }}>
                    <div style={{ fontWeight: 600, marginBottom: 6, fontSize: 12 }}>Review</div>
                    <div style={{ display: 'grid', gridTemplateColumns: '108px 1fr', gap: 4 }}>
                      <span className="muted">Action</span><b>{existing?.exists ? 'Override existing IP (no duplicate)' : 'Create new device'}</b>
                      <span className="muted">Type</span><b>{t.display_name}</b>
                      <span className="muted">IP</span><b>{vals.primary_ip || '—'}</b>
                      <span className="muted">Method</span><b>{method?.label || '—'}</b>
                      <span className="muted">Credential</span><b>{credSummary}</b>
                      <span className="muted">Test result</span><b>{test ? resultUI(test.final_status).label : 'not tested'}</b>
                      <span className="muted">Final state</span><b style={{ color: testPassed ? 'var(--ok)' : 'var(--warn)' }}>{finalState}</b>
                    </div>
                    {!testPassed && (
                      <label style={{ fontSize: 12.5, color: 'var(--warn)', display: 'flex', gap: 6, alignItems: 'flex-start', marginTop: 8 }}>
                        <input type="checkbox" checked={saveManual} onChange={(e) => setSaveManual(e.target.checked)} style={{ marginTop: 2 }} />
                        <span>Test did not authenticate — save as <b>manual inventory only</b> (operator-asserted type, NOT managed).</span>
                      </label>
                    )}
                  </div>

                  {saveMsg && <div className="banner" style={{ marginTop: 10, fontSize: 12 }}>{saveMsg}</div>}
                </>
              )}
            </>
          )}
        </div>

        {/* Footer */}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, padding: '12px 18px', borderTop: '1px solid var(--border)', position: 'sticky', bottom: 0, background: 'var(--surface)' }}>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary btn-sm" onClick={save} disabled={!canSave || saving}>{saving ? 'Saving…' : existing?.exists ? 'Save (override)' : 'Save device'}</button>
        </div>
      </div>
    </div>
  )
}

const inp: React.CSSProperties = { display: 'block', width: '100%', padding: '7px 10px', border: '1px solid var(--border)', borderRadius: 6, fontSize: 13, background: 'var(--bg,#0d1117)', color: 'inherit', marginTop: 3, boxSizing: 'border-box' }
