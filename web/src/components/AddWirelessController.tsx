import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { X, Wifi, AlertTriangle, FlaskConical, CheckCircle2, XCircle, MinusCircle } from 'lucide-react'
import { api, type Location } from '../api'

interface Props {
  onClose: () => void
  onAdded?: (deviceID: string) => void
}

// These mirror the backend Wireless Controller Driver Catalog
// (GET /wireless/controller-vendors). The form is GENERATED from this payload —
// the dropdown, the per-driver fields, the default port, the credential type and
// the honest per-capability status are all server-driven, so onboarding a new
// platform is a backend catalog row with NO change to this form.
type VendorStatus = 'working' | 'detection_only' | 'collector_pending' | 'unsupported'
type CapStatus = 'supported' | 'implemented_live_validation_pending' | 'external_dependency_required' | 'not_implemented' | 'collector_pending' | 'needs_configuration' | 'auth_failed' | 'unsupported_by_device' | 'endpoint_not_exposed' | 'collected'
interface VendorField { key: string; label: string; placeholder?: string; default?: string; help?: string; required: boolean }
interface Capability { key: string; label: string; status: CapStatus; reason?: string }
interface WLVendor {
  key: string
  display_name: string
  model_family: string
  controller_type: string
  protocol: string
  default_port: number
  credential_type: string
  login_method: string
  path_rules: string
  health_endpoints: string
  fields: VendorField[] | null
  capabilities: Capability[]
  status: VendorStatus
  collector_available: boolean
  message?: string
  next_action?: string
}

interface TestCheck { name: string; status: 'ok' | 'fail' | 'warn' | 'skip'; detail?: string }
interface TestResult {
  vendor: string; ok: boolean; reachable: boolean; authenticated: boolean
  api_version?: string; detail: string; checks: TestCheck[]
}

function capTone(s: CapStatus): string {
  switch (s) {
    case 'collected': return 'badge-success'
    case 'supported': return 'badge-info'
    case 'implemented_live_validation_pending':
    case 'external_dependency_required': return 'badge-info'
    case 'auth_failed': return 'badge-down'
    case 'endpoint_not_exposed':
    case 'unsupported_by_device':
    case 'needs_configuration': return 'badge-warning'
    default: return 'badge-muted'
  }
}
function capText(s: CapStatus): string {
  switch (s) {
    case 'supported': return 'supported'
    case 'collected': return 'collected'
    case 'implemented_live_validation_pending': return 'implemented · live-validation pending'
    case 'external_dependency_required': return 'implemented · external dependency'
    case 'collector_pending': return 'collector pending'
    case 'not_implemented': return 'not implemented'
    case 'endpoint_not_exposed': return 'endpoint not exposed'
    case 'unsupported_by_device': return 'unsupported by device'
    case 'needs_configuration': return 'needs configuration'
    case 'auth_failed': return 'auth failed'
    default: return s
  }
}
function checkIcon(s: TestCheck['status']) {
  if (s === 'ok') return <CheckCircle2 size={14} color="var(--ok, #2e7d32)" />
  if (s === 'fail') return <XCircle size={14} color="var(--crit, #c62828)" />
  if (s === 'warn') return <AlertTriangle size={14} color="var(--warn, #ed6c02)" />
  return <MinusCircle size={14} color="var(--neutral, #888)" />
}

// AddWirelessController is the model-driven "Add controller" flow: select a
// driver, fill only that driver's fields, Test Connection (no DB writes), then
// Add & collect. It posts to POST /wireless/controllers.
export function AddWirelessController({ onClose, onAdded }: Props) {
  const qc = useQueryClient()
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const vendors = useQuery({ queryKey: ['wireless-controller-vendors'], queryFn: () => api.get<WLVendor[]>('/wireless/controller-vendors') })

  const [vendorKey, setVendorKey] = useState('')
  const [f, setF] = useState({ ip: '', name: '', username: 'admin', password: '', port: '', location_id: '' })
  const [extra, setExtra] = useState<Record<string, string>>({})
  const [ignoreTLS, setIgnoreTLS] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [test, setTest] = useState<TestResult | null>(null)
  const set = (k: keyof typeof f, v: string) => setF((p) => ({ ...p, [k]: v }))

  const vendor = useMemo(() => (vendors.data ?? []).find((v) => v.key === vendorKey), [vendors.data, vendorKey])
  const working = vendor?.status === 'working'

  function chooseVendor(key: string) {
    setVendorKey(key)
    setTest(null); setErr(null)
    const v = (vendors.data ?? []).find((x) => x.key === key)
    const seed: Record<string, string> = {}
    for (const fld of v?.fields ?? []) seed[fld.key] = fld.default ?? ''
    setExtra(seed)
  }

  // The shared payload for both Test Connection and Add — keys match the backend
  // request struct; vendor-specific params (site / controller_id / api_base) are
  // spread from `extra`.
  const payload = () => ({
    vendor: vendorKey,
    ip: f.ip.trim(),
    name: f.name.trim(),
    location_id: f.location_id || null,
    username: f.username.trim(),
    password: f.password,
    port: f.port ? Number(f.port) : 0,
    ssl_verify: !ignoreTLS,
    ...Object.fromEntries(Object.entries(extra).map(([k, v]) => [k, v.trim()])),
  })

  function validate(): boolean {
    setErr(null)
    if (!vendor) { setErr('Choose the controller type.'); return false }
    if (!f.ip.trim()) { setErr('Controller IP is required.'); return false }
    // Bearer-token drivers (Aruba Central) authenticate with the token alone — it
    // is entered in the password field; the username is optional.
    if (vendor.credential_type === 'bearer_token') {
      if (!f.password) { setErr(`${vendor.display_name} requires an API/OAuth2 token (enter it as the password).`); return false }
    } else if (!f.username.trim() || !f.password) {
      setErr('Admin username and password are required.'); return false
    }
    for (const fld of vendor.fields ?? []) {
      if (fld.required && !(extra[fld.key] ?? '').trim()) { setErr(`${vendor.display_name} requires ${fld.label}.`); return false }
    }
    return true
  }

  const runTest = useMutation({
    mutationFn: () => api.post<TestResult>('/wireless/controllers/test', payload()),
    onSuccess: (r) => setTest(r),
    onError: (e) => setErr((e as Error).message),
  })
  const add = useMutation({
    mutationFn: () => api.post<{ device_id: string }>('/wireless/controllers', payload()),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ['devices'] }); onAdded?.(r.device_id); onClose() },
    onError: (e) => setErr((e as Error).message),
  })

  function onTest() { if (validate()) { setTest(null); runTest.mutate() } }
  function onSubmit() { if (validate()) add.mutate() }

  const field = (label: string, node: React.ReactNode, hint?: string) => (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12 }}>
      <span className="muted">{label}</span>
      {node}
      {hint && <span className="muted" style={{ fontSize: 11 }}>{hint}</span>}
    </label>
  )
  const inputStyle = { padding: '6px 8px', fontSize: 13 } as const

  return (
    <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', justifyContent: 'flex-end', zIndex: 1000 }} onClick={onClose}>
      <div className="modal-panel" style={{ width: 480, maxWidth: '100%', height: '100%', background: 'var(--surface)', overflowY: 'auto', boxShadow: '-4px 0 24px rgba(0,0,0,.3)' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 16px', borderBottom: '1px solid var(--border)', position: 'sticky', top: 0, background: 'var(--surface)', zIndex: 1 }}>
          <strong style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}><Wifi size={16} /> Add wireless controller</strong>
          <button className="btn btn-ghost btn-sm" onClick={onClose}><X size={16} /></button>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, padding: 16 }}>
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            The form adapts to the controller you choose. Collected via the vendor management API/XML as the
            <strong> primary</strong> method. The password is encrypted at rest (never stored in plain text).
          </p>

          {vendors.isLoading && <div className="muted" style={{ fontSize: 12 }}>Loading controller types…</div>}
          {vendors.isError && <div className="error-msg" style={{ fontSize: 12 }}>Could not load controller types.</div>}

          {field('Vendor / platform', (
            <select value={vendorKey} onChange={(e) => chooseVendor(e.target.value)} style={inputStyle}>
              <option value="">— select controller type —</option>
              {(vendors.data ?? []).filter((v) => v.status !== 'unsupported').map((v) => (
                <option key={v.key} value={v.key}>{v.display_name}</option>
              ))}
            </select>
          ), vendor ? `${vendor.model_family} · ${vendor.protocol}` : 'Pick the controller family — adding it as the wrong driver cannot collect.')}

          {/* Driver capability summary — honest per feature, straight from the catalog. */}
          {vendor && (
            <div className={'enc-banner ' + (working ? 'info' : 'warn')} style={{ fontSize: 12 }}>
              {!working && <strong style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}><AlertTriangle size={14} /> Collector pending</strong>}
              {vendor.message && <div style={{ marginTop: working ? 0 : 6 }}>{vendor.message}</div>}
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 8 }}>
                {vendor.capabilities.map((c) => (
                  <span key={c.key} className={'badge ' + capTone(c.status)} title={`${c.label}: ${capText(c.status)}${c.reason ? ' — ' + c.reason : ''}`}>
                    {c.label}: {capText(c.status)}
                  </span>
                ))}
              </div>
              <div className="muted" style={{ fontSize: 11, marginTop: 8 }}>Login: {vendor.login_method} · Credential: {vendor.credential_type.replace(/_/g, ' ')}</div>
            </div>
          )}

          {field('Controller IP', <input value={f.ip} onChange={(e) => { set('ip', e.target.value); setTest(null) }} placeholder="172.21.96.100" style={inputStyle} />)}
          {field('Name (optional)', <input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="Aqua controller" style={inputStyle} />)}
          {field('HIMS site / location (optional)', (
            <select value={f.location_id} onChange={(e) => set('location_id', e.target.value)} style={inputStyle}>
              <option value="">— none —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </select>
          ))}
          {vendor?.credential_type !== 'bearer_token' && field('Admin username', <input value={f.username} onChange={(e) => { set('username', e.target.value); setTest(null) }} autoComplete="off" style={inputStyle} />)}
          {field(vendor?.credential_type === 'bearer_token' ? 'API / OAuth2 token' : 'Admin password', <input type="password" value={f.password} onChange={(e) => { set('password', e.target.value); setTest(null) }} autoComplete="new-password" style={inputStyle} />)}
          {field('Port', <input value={f.port} onChange={(e) => { set('port', e.target.value); setTest(null) }} placeholder={vendor ? String(vendor.default_port) : 'choose a vendor first'} style={inputStyle} />, vendor ? `Default ${vendor.default_port} · ${vendor.path_rules}` : undefined)}

          {/* Driver-specific connection parameters, rendered from the catalog. */}
          {(vendor?.fields ?? []).map((fld) => field(
            fld.label + (fld.required ? ' *' : ' (optional)'),
            <input value={extra[fld.key] ?? ''} onChange={(e) => { setExtra((p) => ({ ...p, [fld.key]: e.target.value })); setTest(null) }} placeholder={fld.placeholder ?? ''} style={inputStyle} />,
            fld.help,
          ))}

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
            <input type="checkbox" checked={ignoreTLS} onChange={(e) => { setIgnoreTLS(e.target.checked); setTest(null) }} />
            Ignore TLS certificate (self-signed mgmt cert)
          </label>

          {/* Structured Test Connection result. */}
          {test && (
            <div className={'enc-banner ' + (test.ok ? 'ok' : 'crit')} style={{ fontSize: 12 }}>
              <strong>{test.ok ? '✓ Connection OK' : '✗ Connection failed'}</strong> — {test.detail}
              <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {test.checks.map((c, i) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    {checkIcon(c.status)}<span>{c.name}{c.detail ? <span className="muted"> — {c.detail}</span> : ''}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {err && <div className="error-msg" style={{ fontSize: 12 }}>{err}</div>}

          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', flexWrap: 'wrap' }}>
            <button className="btn btn-ghost btn-sm" onClick={onClose} disabled={add.isPending || runTest.isPending}>Cancel</button>
            {working && (
              <button className="btn btn-ghost btn-sm" onClick={onTest} disabled={!vendor || runTest.isPending || add.isPending}>
                <FlaskConical size={14} /> {runTest.isPending ? 'Testing…' : 'Test Connection'}
              </button>
            )}
            <button className="btn btn-primary btn-sm" onClick={onSubmit} disabled={add.isPending || !vendor}>
              <Wifi size={14} /> {add.isPending ? 'Adding…' : working ? 'Add & collect' : 'Add (detection only)'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
