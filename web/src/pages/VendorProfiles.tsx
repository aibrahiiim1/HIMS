import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import {
  api, type VendorProfile, type VendorProfileTestResponse, type Credential,
  type Location, type Device, type EncryptionStatus,
} from '../api'

// Vendor Connection Profiles — the operator-facing screen that closes the
// wireless / CUCM / VMware / CCTV "config gates". A profile pairs a stored
// credential with a target URL + per-vendor connection params and an optional
// site/device binding, so the scan (and manual Test / Run Collection) can
// actually authenticate and collect. Secrets never appear here.

// CreatePreset prefills the create form when arriving from a Scan Result
// "Create Vendor Profile" link (?create=1&vendor_type=…&device_id=…&target_url=…).
interface CreatePreset { vendorType: string; deviceID: string; targetURL: string }

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}
const ghost: React.CSSProperties = { padding: '3px 8px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }

// Vendor catalogue: the connection-param fields each type needs, plus an honest
// note for the gates whose protocol isn't implemented yet.
interface VendorDef {
  type: string
  label: string
  group: string
  urlLabel: string
  urlHint?: string
  // config fields beyond target_url + credential
  fields: { key: string; label: string; hint?: string }[]
  insecure?: boolean
  note?: string // honest gate for not-yet-implemented protocols
}
const VENDORS: VendorDef[] = [
  { type: 'vmware', label: 'VMware vSphere / ESXi', group: 'Virtualization', urlLabel: 'vCenter / ESXi URL', urlHint: 'https://vcenter.example.com', fields: [], insecure: true },
  { type: 'cctv', label: 'CCTV (Hikvision / Dahua / ONVIF)', group: 'Surveillance', urlLabel: 'Device / NVR address', urlHint: 'https://10.0.0.50 or 10.0.0.50', fields: [], insecure: true },
  { type: 'wireless_unifi', label: 'Ubiquiti UniFi', group: 'Wireless', urlLabel: 'Controller URL', urlHint: 'https://unifi.example.com:8443', fields: [{ key: 'site', label: 'UniFi site', hint: 'default' }], insecure: true },
  { type: 'wireless_omada', label: 'TP-Link Omada', group: 'Wireless', urlLabel: 'Controller URL', urlHint: 'https://omada.example.com:8043', fields: [{ key: 'controller_id', label: 'Controller ID', hint: 'omadac id (from controller URL)' }], insecure: true },
  { type: 'wireless_ruckus', label: 'Ruckus (SmartZone / vSZ)', group: 'Wireless', urlLabel: 'Controller URL', urlHint: 'https://ruckus.example.com:8443', fields: [{ key: 'api_base', label: 'API base path', hint: '/wsg/api/public' }], insecure: true },
  { type: 'wireless_extreme', label: 'Extreme (ExtremeCloud IQ — cloud/XIQ)', group: 'Wireless', urlLabel: 'XIQ API URL', urlHint: 'https://api.extremecloudiq.com', fields: [{ key: 'api_base', label: 'API base path', hint: '/' }], insecure: true },
  { type: 'extreme_xcc', label: 'Extreme XCC (on-prem ExtremeCloud IQ Controller)', group: 'Wireless', urlLabel: 'Controller URL', urlHint: 'https://172.21.96.100:5825', fields: [{ key: 'api_base', label: 'API base path (auto-discovered by Test)', hint: '/management/v1' }], insecure: true, note: 'On-prem ExtremeCloud IQ Controller. REST/JSON on :5825. Use admin/API credentials. Run Test Connection first — it safely discovers the API path/auth and saves it; then Run Collection pulls AP/SSID/client data. Tip: Inventory → Wireless → Add controller does this in one step.' },
  { type: 'ruckus_zd', label: 'Ruckus ZoneDirector (Web-XML / AJAX)', group: 'Wireless', urlLabel: 'ZoneDirector URL', urlHint: 'https://192.168.2.2:443', fields: [], insecure: true, note: 'Ruckus ZoneDirector (ZD3050, fw 10.x) via its internal Web-XML AJAX interface on :443 (no REST API). Use the web-admin credentials. Events are not exposed by the AJAX interface (SNMP traps only). Tip: Inventory → Wireless → Add controller does this in one step.' },
  { type: 'wireless_aruba', label: 'Aruba (Mobility / Central)', group: 'Wireless', urlLabel: 'Controller / Central URL', fields: [], note: 'Aruba REST collection is not implemented yet — saving a profile records detection + binding intent and the Test will report the honest gate. SNMP enrichment still applies to Aruba controllers in the meantime.' },
  { type: 'cucm', label: 'Cisco Unified CM (AXL)', group: 'Voice', urlLabel: 'CUCM / AXL host', urlHint: 'https://cucm.example.com', fields: [{ key: 'version', label: 'AXL schema version', hint: 'e.g. 12.5 (blank = auto)' }], insecure: true },
  { type: 'alcatel', label: 'Alcatel OmniPCX / OmniVista', group: 'Voice', urlLabel: 'OmniVista / OmniPCX host', fields: [], note: 'Alcatel management protocol is not implemented yet — saving a profile records detection + binding intent; the Test reports the honest gate and points to SNMP enrichment. This closes the dead end with a documented next action rather than silently ignoring the device.' },
]
const vendorDef = (t: string) => VENDORS.find((v) => v.type === t)

function StatusBadge({ p }: { p: VendorProfile }) {
  if (!p.enabled) return <span className="badge badge-unknown">disabled</span>
  switch (p.status) {
    case 'ok': return <span className="badge badge-up">tested ok</span>
    case 'failed': return <span className="badge badge-down">failed</span>
    default: return <span className="badge badge-warning">untested</span>
  }
}

function EncryptionGate() {
  const q = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), retry: 0 })
  if (!q.data || q.data.enabled) return null
  return (
    <div className="enc-banner crit" style={{ marginBottom: 16 }}>
      <span>🔒</span>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 700 }}>Credential storage is disabled — no encryption key is configured</div>
        <div style={{ fontSize: 12, marginTop: 2 }}>Vendor profiles bind an encrypted credential; without a key they cannot be tested or used in a scan. Set <code>HIMS_ENCRYPTION_KEY</code> and restart the API.</div>
      </div>
      <Link className="btn btn-sm" to="/security/encryption" style={{ whiteSpace: 'nowrap' }}>Configure Encryption →</Link>
    </div>
  )
}

export function VendorProfiles() {
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const [show, setShow] = useState(false)
  const [edit, setEdit] = useState<VendorProfile | null>(null)
  const [testResult, setTestResult] = useState<Record<string, VendorProfileTestResponse>>({})

  const list = useQuery({ queryKey: ['vendor-profiles'], queryFn: () => api.get<VendorProfile[]>('/vendor-profiles') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const refresh = () => qc.invalidateQueries({ queryKey: ['vendor-profiles'] })

  const test = useMutation({
    mutationFn: (id: string) => api.post<VendorProfileTestResponse>(`/vendor-profiles/${id}/test`, {}),
    onSuccess: (data, id) => { setTestResult((m) => ({ ...m, [id]: data })); refresh() },
  })
  const runColl = useMutation({
    mutationFn: (id: string) => api.post<VendorProfileTestResponse>(`/vendor-profiles/${id}/run-collection`, {}),
    onSuccess: (data, id) => { setTestResult((m) => ({ ...m, [id]: data })); refresh() },
  })
  const toggle = useMutation({
    mutationFn: (p: VendorProfile) => api.patch(`/vendor-profiles/${p.id}`, {
      name: p.name, vendor_type: p.vendor_type, target_url: p.target_url,
      credential_id: p.credential_id ?? '', location_id: p.location_id ?? '',
      device_id: p.device_id ?? '', config: p.config, enabled: !p.enabled,
    }),
    onSuccess: refresh,
  })
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/vendor-profiles/${id}`),
    onSuccess: refresh,
  })

  const locName = (id?: string) => locs.data?.find((l) => l.id === id)?.name

  // Deep links from Scan Results, derived from the URL during render (no effect):
  //   ?create=1 (+vendor_type/device_id/target_url) → prefilled create form
  //   ?open=<id>                                     → that profile, for editing
  const createParam = !!params.get('create')
  const presetFromParams: CreatePreset | null = createParam
    ? { vendorType: params.get('vendor_type') || '', deviceID: params.get('device_id') || '', targetURL: params.get('target_url') || '' }
    : null
  const openParam = params.get('open')
  const openProfile = openParam ? (list.data?.find((p) => p.id === openParam) ?? null) : null
  const editTarget = edit ?? openProfile
  const showForm = show || createParam || !!editTarget
  const clearParams = () => { if (params.toString()) setParams({}, { replace: true }) }
  const closeForm = () => { setShow(false); setEdit(null); clearParams() }

  return (
    <div>
      <EncryptionGate />
      <div className="card">
        <h2>Vendor Connection Profiles</h2>
        <p className="muted" style={{ marginBottom: 10 }}>
          Configure integration endpoints — vCenter/ESXi, Hikvision/ONVIF CCTV, wireless controllers
          (UniFi / Omada / Ruckus / Extreme / Aruba), and Cisco CUCM — so discovery can authenticate
          and collect facts instead of leaving these devices as unmanaged hints. A profile binds a
          stored credential (the secret never appears here) plus the target URL and per-vendor
          connection params, optionally scoped to a site or a specific device. During a scan, a
          matching enabled profile is resolved (device → site → global), tested, and used to collect;
          successes and failures are written to Credential Test History. Use <strong>Test</strong> to
          verify connectivity now, and <strong>Run Collection</strong> to collect against a
          device-bound profile on demand.
        </p>
        <button style={btn} onClick={() => { if (showForm) { closeForm() } else { setEdit(null); setShow(true) } }}>{showForm && !editTarget ? 'Cancel' : '+ New profile'}</button>
      </div>

      {showForm && (
        <ProfileForm
          key={editTarget?.id ?? 'new'}
          profile={editTarget}
          preset={editTarget ? null : presetFromParams}
          credentials={creds.data ?? []}
          locations={locs.data ?? []}
          onDone={() => { closeForm(); refresh() }}
          onCancel={closeForm}
        />
      )}

      <div className="card">
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.error && <div className="error-msg">{(list.error as Error).message}</div>}
        {list.data && list.data.length === 0 && (
          <div className="muted">No vendor profiles yet. Add one to onboard a vCenter, NVR, wireless controller, or CUCM.</div>
        )}
        {list.data && list.data.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Name</th><th>Vendor</th><th>Target</th><th>Credential</th><th>Site</th>
                <th>Bound device</th><th>Last test</th><th>Last collection</th><th>Status</th><th></th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((p) => {
                const def = vendorDef(p.vendor_type)
                const tr = testResult[p.id]
                return (
                  <tr key={p.id}>
                    <td><strong>{p.name}</strong></td>
                    <td>{def?.label ?? p.vendor_type}</td>
                    <td className="mono" style={{ fontSize: 12, maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis' }}>{p.target_url || '—'}</td>
                    <td>{p.credential_name || <span className="muted">none</span>}</td>
                    <td>{locName(p.location_id) || <span className="muted">—</span>}</td>
                    <td>{p.device_id ? <Link to={`/devices/${p.device_id}`}>device</Link> : <span className="muted">—</span>}</td>
                    <td style={{ fontSize: 12 }}>
                      {p.last_test_at ? (
                        <span>
                          <span className={`badge ${p.last_test_ok ? 'badge-up' : 'badge-down'}`}>{p.last_test_ok ? 'ok' : 'fail'}</span>{' '}
                          <span className="muted">{p.last_test_at.slice(0, 16).replace('T', ' ')}</span>
                        </span>
                      ) : <span className="muted">never</span>}
                    </td>
                    <td style={{ fontSize: 12 }}>
                      {p.last_collection_at ? <span className="muted">{p.last_collection_at.slice(0, 16).replace('T', ' ')}</span> : <span className="muted">never</span>}
                    </td>
                    <td><StatusBadge p={p} /></td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button style={ghost} disabled={test.isPending} onClick={() => test.mutate(p.id)}>Test</button>{' '}
                      <button style={ghost} disabled={!p.device_id || runColl.isPending} title={p.device_id ? 'Collect now against the bound device' : 'Bind a device to enable on-demand collection'} onClick={() => runColl.mutate(p.id)}>Run</button>{' '}
                      <button style={ghost} onClick={() => { setShow(false); setEdit(p) }}>Edit</button>{' '}
                      <button style={ghost} onClick={() => toggle.mutate(p)}>{p.enabled ? 'Disable' : 'Enable'}</button>{' '}
                      <button style={danger} onClick={() => { if (confirm(`Delete vendor profile "${p.name}"?`)) del.mutate(p.id) }}>Delete</button>
                      {tr && (
                        <div style={{ marginTop: 4, fontSize: 12, maxWidth: 340, whiteSpace: 'normal' }} className={tr.ok ? '' : 'error-msg'}>
                          {tr.ok ? '✓ ' : '✗ '}{tr.detail}
                        </div>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
        {(test.error || runColl.error || del.error || toggle.error) && (
          <div className="error-msg" style={{ marginTop: 8 }}>{((test.error || runColl.error || del.error || toggle.error) as Error).message}</div>
        )}
      </div>
    </div>
  )
}

function ProfileForm({ profile, preset, credentials, locations, onDone, onCancel }: {
  profile: VendorProfile | null
  preset?: CreatePreset | null
  credentials: Credential[]
  locations: Location[]
  onDone: () => void
  onCancel: () => void
}) {
  const editing = !!profile
  const presetVT = preset?.vendorType && vendorDef(preset.vendorType) ? preset.vendorType : ''
  const [name, setName] = useState(profile?.name ?? '')
  const [vendorType, setVendorType] = useState(profile?.vendor_type ?? (presetVT || 'vmware'))
  const [targetURL, setTargetURL] = useState(profile?.target_url ?? preset?.targetURL ?? '')
  const [credentialID, setCredentialID] = useState(profile?.credential_id ?? '')
  const [locationID, setLocationID] = useState(profile?.location_id ?? '')
  const [deviceID, setDeviceID] = useState(profile?.device_id ?? preset?.deviceID ?? '')
  const [deviceFilter, setDeviceFilter] = useState('')
  const [cfg, setCfg] = useState<Record<string, string>>(() => {
    const c = (profile?.config ?? {}) as Record<string, unknown>
    const out: Record<string, string> = {}
    for (const k of Object.keys(c)) if (typeof c[k] === 'string') out[k] = c[k] as string
    return out
  })
  const [insecure, setInsecure] = useState<boolean>(() => Boolean((profile?.config as Record<string, unknown> | undefined)?.insecure ?? true))

  const def = vendorDef(vendorType)!

  // Device picker (optional binding) — only needed for on-demand Run Collection.
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const filteredDevices = useMemo(() => {
    const f = deviceFilter.trim().toLowerCase()
    const all = (devices.data ?? []).filter((d) => d.primary_ip)
    if (!f) return all.slice(0, 50)
    return all.filter((d) => d.name.toLowerCase().includes(f) || (d.primary_ip ?? '').includes(f)).slice(0, 50)
  }, [devices.data, deviceFilter])

  const buildConfig = (): Record<string, unknown> => {
    const out: Record<string, unknown> = {}
    for (const fld of def.fields) if (cfg[fld.key]?.trim()) out[fld.key] = cfg[fld.key].trim()
    if (def.insecure) out.insecure = insecure
    return out
  }

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name, vendor_type: vendorType, target_url: targetURL.trim(),
        credential_id: credentialID, location_id: locationID, device_id: deviceID,
        config: buildConfig(), enabled: profile?.enabled ?? true,
      }
      return editing
        ? api.patch<VendorProfile>(`/vendor-profiles/${profile!.id}`, body)
        : api.post<VendorProfile>('/vendor-profiles', body)
    },
    onSuccess: onDone,
  })

  const valid = name.trim() && vendorType && (def.note ? true : targetURL.trim())

  return (
    <div className="card">
      <h2>{editing ? 'Edit profile' : 'New vendor profile'}</h2>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 12 }}>
        <label>Name<input style={input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Hotel A vCenter" /></label>
        <label>Vendor type
          <select style={input} value={vendorType} onChange={(e) => setVendorType(e.target.value)} disabled={editing}>
            {['Virtualization', 'Surveillance', 'Wireless', 'Voice'].map((g) => (
              <optgroup key={g} label={g}>
                {VENDORS.filter((v) => v.group === g).map((v) => <option key={v.type} value={v.type}>{v.label}</option>)}
              </optgroup>
            ))}
          </select>
        </label>
        <label>{def.urlLabel}<input style={input} value={targetURL} onChange={(e) => setTargetURL(e.target.value)} placeholder={def.urlHint} /></label>
        <label>Credential
          <select style={input} value={credentialID} onChange={(e) => setCredentialID(e.target.value)}>
            <option value="">— select —</option>
            {credentials.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
          </select>
        </label>
        {def.fields.map((fld) => (
          <label key={fld.key}>{fld.label}
            <input style={input} value={cfg[fld.key] ?? ''} onChange={(e) => setCfg((m) => ({ ...m, [fld.key]: e.target.value }))} placeholder={fld.hint} />
          </label>
        ))}
        <label>Assign to site (optional)
          <select style={input} value={locationID} onChange={(e) => setLocationID(e.target.value)}>
            <option value="">— global —</option>
            {locations.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
          </select>
        </label>
      </div>

      {def.insecure && (
        <label style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 12, fontSize: 13 }}>
          <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
          Allow self-signed / untrusted TLS certificate (common for on-prem appliances)
        </label>
      )}

      {def.note && (
        <div className="enc-banner" style={{ marginTop: 12, background: '#332b1a', borderColor: '#7a5a1a' }}>
          <span>⚠️</span>
          <div style={{ fontSize: 12 }}>{def.note}</div>
        </div>
      )}

      {/* Optional device binding — required only for on-demand Run Collection. */}
      <div style={{ marginTop: 14 }}>
        <div style={{ fontWeight: 600, marginBottom: 6, fontSize: 13 }}>
          Bind to a specific device (optional)
          {deviceID && <button style={{ ...ghost, marginLeft: 10 }} onClick={() => setDeviceID('')}>clear</button>}
        </div>
        <p className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
          Bind a device to enable on-demand <strong>Run Collection</strong> and to write collected facts back to that device. Leave unbound to use the profile only during scans (resolved by site).
        </p>
        <input style={{ ...input, marginBottom: 6, maxWidth: 360 }} placeholder="filter by name / IP…" value={deviceFilter} onChange={(e) => setDeviceFilter(e.target.value)} />
        <div style={{ maxHeight: 160, overflow: 'auto', border: '1px solid #2a3a47', borderRadius: 6, padding: 8 }}>
          {filteredDevices.map((d) => (
            <label key={d.id} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', fontSize: 13 }}>
              <input type="radio" name="vp-device" checked={deviceID === d.id} onChange={() => setDeviceID(d.id)} />
              <span className="mono" style={{ fontSize: 12 }}>{d.primary_ip}</span><span>{d.name}</span>
              <span className="muted" style={{ fontSize: 11 }}>{d.category}</span>
            </label>
          ))}
          {filteredDevices.length === 0 && <div className="muted">No matching devices.</div>}
        </div>
      </div>

      <div style={{ marginTop: 14 }}>
        <button style={btn} disabled={!valid || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? 'Saving…' : editing ? 'Save changes' : 'Create profile'}
        </button>{' '}
        <button style={ghost} onClick={onCancel}>Cancel</button>
        {save.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(save.error as Error).message}</span>}
      </div>
    </div>
  )
}
