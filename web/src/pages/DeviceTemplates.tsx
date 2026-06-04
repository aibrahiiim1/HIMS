import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LayoutTemplate, Plus, Trash2, Rocket, Activity, Bell, Library, X } from 'lucide-react'
import {
  api, type Device, type DeviceTemplate, type TemplateCheck, type TemplateAlert,
  type TemplateMonitoring, type ApplyTemplateResult,
} from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

type Editable = {
  id?: string
  name: string
  vendor: string
  device_type: string
  enabled: boolean
  checks: TemplateCheck[]
  alerts: TemplateAlert[]
  discovery_rules: unknown
  classification_rules: unknown
}

const newCheck = (): TemplateCheck => ({ kind: 'tcp', label: '', port: 443, oid: '', interval_seconds: 60, down_threshold: 2 })
const newAlert = (): TemplateAlert => ({ name: '', trigger_status: 'down', min_failures: 2, severity: 'critical', auto_work_order: false, work_order_priority: 'high' })

const blank = (): Editable => ({ name: '', vendor: '', device_type: '', enabled: true, checks: [], alerts: [], discovery_rules: {}, classification_rules: {} })

function asMonitoring(v: unknown): TemplateMonitoring {
  const m = (v ?? {}) as Partial<TemplateMonitoring>
  return { checks: Array.isArray(m.checks) ? m.checks : [], alerts: Array.isArray(m.alerts) ? m.alerts : [] }
}

// Built-in starter library — real, standard ports/OIDs so a fresh install has
// sensible profiles in one click. sysUpTime (1.3.6.1.2.1.1.3.0) is the safe
// universal scalar SNMP probe.
const LIBRARY: Editable[] = [
  {
    ...blank(), name: 'Network Switch', device_type: 'switch',
    checks: [
      { kind: 'tcp', label: 'SSH', port: 22, oid: '', interval_seconds: 60, down_threshold: 2 },
      { kind: 'tcp', label: 'HTTPS mgmt', port: 443, oid: '', interval_seconds: 60, down_threshold: 2 },
      { kind: 'snmp', label: 'sysUpTime', port: 0, oid: '1.3.6.1.2.1.1.3.0', interval_seconds: 120, down_threshold: 3 },
    ],
    alerts: [{ name: 'Switch unreachable', trigger_status: 'down', min_failures: 2, severity: 'critical', auto_work_order: true, work_order_priority: 'high' }],
  },
  {
    ...blank(), name: 'Server', device_type: 'server',
    checks: [
      { kind: 'tcp', label: 'SSH', port: 22, oid: '', interval_seconds: 60, down_threshold: 2 },
      { kind: 'tcp', label: 'RDP', port: 3389, oid: '', interval_seconds: 60, down_threshold: 2 },
      { kind: 'snmp', label: 'sysUpTime', port: 0, oid: '1.3.6.1.2.1.1.3.0', interval_seconds: 120, down_threshold: 3 },
    ],
    alerts: [{ name: 'Server unreachable', trigger_status: 'down', min_failures: 2, severity: 'critical', auto_work_order: false, work_order_priority: 'high' }],
  },
  {
    ...blank(), name: 'Firewall', device_type: 'firewall',
    checks: [
      { kind: 'tcp', label: 'HTTPS mgmt', port: 443, oid: '', interval_seconds: 60, down_threshold: 2 },
      { kind: 'tcp', label: 'SSH', port: 22, oid: '', interval_seconds: 60, down_threshold: 2 },
    ],
    alerts: [{ name: 'Firewall unreachable', trigger_status: 'down', min_failures: 1, severity: 'critical', auto_work_order: true, work_order_priority: 'critical' }],
  },
  {
    ...blank(), name: 'Printer', device_type: 'printer',
    checks: [
      { kind: 'tcp', label: 'RAW 9100', port: 9100, oid: '', interval_seconds: 120, down_threshold: 3 },
      { kind: 'tcp', label: 'Web UI', port: 80, oid: '', interval_seconds: 300, down_threshold: 3 },
    ],
    alerts: [{ name: 'Printer offline', trigger_status: 'down', min_failures: 3, severity: 'warning', auto_work_order: false, work_order_priority: 'low' }],
  },
]

export function DeviceTemplates() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['device-templates'], queryFn: () => api.get<DeviceTemplate[]>('/device-templates') })
  const inv = () => qc.invalidateQueries({ queryKey: ['device-templates'] })
  const [form, setForm] = useState<Editable | null>(null)
  const [applyFor, setApplyFor] = useState<DeviceTemplate | null>(null)
  const [msg, setMsg] = useState('')

  const save = useMutation({
    mutationFn: (e: Editable) => {
      const body = {
        name: e.name, vendor: e.vendor, device_type: e.device_type, enabled: e.enabled,
        discovery_rules: e.discovery_rules ?? {}, classification_rules: e.classification_rules ?? {},
        monitoring_rules: { checks: e.checks, alerts: e.alerts },
      }
      return e.id ? api.patch(`/device-templates/${e.id}`, body) : api.post('/device-templates', body)
    },
    onSuccess: () => { setForm(null); setMsg('Saved.'); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/device-templates/${id}`), onSuccess: inv })

  const rows = q.data ?? []
  const totalChecks = useMemo(() => (q.data ?? []).reduce((a, t) => a + asMonitoring(t.monitoring_rules).checks.length, 0), [q.data])

  const startEdit = (t: DeviceTemplate) => {
    const m = asMonitoring(t.monitoring_rules)
    setForm({ id: t.id, name: t.name, vendor: t.vendor, device_type: t.device_type, enabled: t.enabled, checks: m.checks, alerts: m.alerts, discovery_rules: t.discovery_rules, classification_rules: t.classification_rules })
    setMsg('')
  }

  return (
    <div>
      <PageHeader title="Device Templates" icon={LayoutTemplate}
        subtitle="Reusable monitoring profiles — TCP/SNMP check sets + default alert rules — applied to many devices at once"
        actions={<button className="btn btn-primary btn-sm" onClick={() => { setForm(blank()); setMsg('') }}><Plus size={14} /> New template</button>} />

      <div className="kpi-grid">
        <Kpi label="Templates" value={rows.length} icon={LayoutTemplate} tone="info" />
        <Kpi label="Enabled" value={rows.filter((t) => t.enabled).length} tone="ok" />
        <Kpi label="Checks Defined" value={totalChecks} icon={Activity} />
        <Kpi label="Device Types" value={new Set(rows.map((t) => t.device_type).filter(Boolean)).size} />
      </div>

      {msg && <div className={'enc-banner ' + (msg === 'Saved.' ? 'info' : 'crit')} style={{ marginBottom: 12 }}>{msg}</div>}

      {!form && (
        <Panel title="Starter Library" icon={Library} subtitle="One-click profiles with standard ports + sysUpTime">
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {LIBRARY.map((lib) => (
              <button key={lib.name} className="btn btn-ghost btn-sm" onClick={() => { setForm({ ...lib, checks: lib.checks.map((c) => ({ ...c })), alerts: lib.alerts.map((a) => ({ ...a })) }); setMsg('') }}>
                <Plus size={13} /> {lib.name}
              </button>
            ))}
          </div>
        </Panel>
      )}

      {form && <TemplateEditor form={form} setForm={setForm} onSave={() => save.mutate(form)} saving={save.isPending} onCancel={() => setForm(null)} />}

      <Panel title="Templates" icon={LayoutTemplate} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={LayoutTemplate} title="No templates yet" message="Use the starter library above or create a template, then apply it to devices to seed their monitoring + alert rules." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Device type</th><th>Vendor</th><th>Checks</th><th>Alerts</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {rows.map((t) => {
                const m = asMonitoring(t.monitoring_rules)
                return (
                  <tr key={t.id}>
                    <td className="cell-name">{t.name}</td>
                    <td>{t.device_type ? <span className="badge badge-unknown">{t.device_type}</span> : '—'}</td>
                    <td>{t.vendor || '—'}</td>
                    <td>{m.checks.length}</td>
                    <td>{m.alerts.length}</td>
                    <td>{t.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                    <td className="cell-actions">
                      <button className="btn btn-primary btn-xs" onClick={() => setApplyFor(t)}><Rocket size={12} /> Apply</button>
                      <button className="btn btn-ghost btn-xs" onClick={() => startEdit(t)}>Edit</button>
                      <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(t.id)}><Trash2 size={12} /></button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>

      {applyFor && <ApplyDialog template={applyFor} onClose={() => setApplyFor(null)} />}
    </div>
  )
}

function TemplateEditor({ form, setForm, onSave, saving, onCancel }: {
  form: Editable; setForm: (e: Editable) => void; onSave: () => void; saving: boolean; onCancel: () => void
}) {
  const setCheck = (i: number, patch: Partial<TemplateCheck>) => setForm({ ...form, checks: form.checks.map((c, j) => (j === i ? { ...c, ...patch } : c)) })
  const setAlert = (i: number, patch: Partial<TemplateAlert>) => setForm({ ...form, alerts: form.alerts.map((a, j) => (j === i ? { ...a, ...patch } : a)) })

  return (
    <Panel title={form.id ? 'Edit Template' : 'New Template'} icon={Plus}
      actions={<>
        <button className="btn btn-ghost btn-sm" onClick={onCancel}>Cancel</button>
        <button className="btn btn-primary btn-sm" disabled={!form.name || saving} onClick={onSave}>{saving ? 'Saving…' : form.id ? 'Update' : 'Create'}</button>
      </>}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: 12 }}>
        <label className="form-field">Name<input className="field" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Network Switch" /></label>
        <label className="form-field">Device type (category)<input className="field" value={form.device_type} onChange={(e) => setForm({ ...form, device_type: e.target.value })} placeholder="switch" /></label>
        <label className="form-field">Vendor (optional)<input className="field" value={form.vendor} onChange={(e) => setForm({ ...form, vendor: e.target.value })} placeholder="aruba_hpe" /></label>
        <label className="form-field" style={{ alignSelf: 'end' }}><span className="row" style={{ gap: 6 }}><input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} /> enabled</span></label>
      </div>

      <h4 style={{ margin: '16px 0 6px', display: 'flex', alignItems: 'center', gap: 6 }}><Activity size={15} /> Monitors</h4>
      <table className="data-table">
        <thead><tr><th>Type</th><th>Label</th><th>Port / OID</th><th>Interval (s)</th><th>Down after</th><th></th></tr></thead>
        <tbody>
          {form.checks.map((c, i) => (
            <tr key={i}>
              <td><select className="field" value={c.kind} onChange={(e) => setCheck(i, { kind: e.target.value as 'tcp' | 'snmp' })}><option value="tcp">TCP</option><option value="snmp">SNMP</option></select></td>
              <td><input className="field" value={c.label} onChange={(e) => setCheck(i, { label: e.target.value })} placeholder="SSH" /></td>
              <td>{c.kind === 'tcp'
                ? <input className="field" type="number" value={c.port} onChange={(e) => setCheck(i, { port: Number(e.target.value) })} placeholder="443" style={{ width: 110 }} />
                : <input className="field mono" value={c.oid} onChange={(e) => setCheck(i, { oid: e.target.value })} placeholder="1.3.6.1.2.1.1.3.0" />}</td>
              <td><input className="field" type="number" value={c.interval_seconds} onChange={(e) => setCheck(i, { interval_seconds: Number(e.target.value) })} style={{ width: 90 }} /></td>
              <td><input className="field" type="number" value={c.down_threshold} onChange={(e) => setCheck(i, { down_threshold: Number(e.target.value) })} style={{ width: 70 }} /></td>
              <td><button className="btn btn-ghost btn-xs" onClick={() => setForm({ ...form, checks: form.checks.filter((_, j) => j !== i) })}><X size={12} /></button></td>
            </tr>
          ))}
        </tbody>
      </table>
      <button className="btn btn-ghost btn-sm" style={{ marginTop: 6 }} onClick={() => setForm({ ...form, checks: [...form.checks, newCheck()] })}><Plus size={13} /> Add monitor</button>

      <h4 style={{ margin: '16px 0 6px', display: 'flex', alignItems: 'center', gap: 6 }}><Bell size={15} /> Default Alert Rules <span className="muted" style={{ fontSize: 12, fontWeight: 400 }}>(category-scoped, created once)</span></h4>
      <table className="data-table">
        <thead><tr><th>Name</th><th>Trigger</th><th>Min failures</th><th>Severity</th><th>Auto WO</th><th></th></tr></thead>
        <tbody>
          {form.alerts.map((a, i) => (
            <tr key={i}>
              <td><input className="field" value={a.name} onChange={(e) => setAlert(i, { name: e.target.value })} placeholder="Switch unreachable" /></td>
              <td><select className="field" value={a.trigger_status} onChange={(e) => setAlert(i, { trigger_status: e.target.value as 'down' | 'warning' })}><option value="down">down</option><option value="warning">warning</option></select></td>
              <td><input className="field" type="number" value={a.min_failures} onChange={(e) => setAlert(i, { min_failures: Number(e.target.value) })} style={{ width: 80 }} /></td>
              <td><select className="field" value={a.severity} onChange={(e) => setAlert(i, { severity: e.target.value as 'info' | 'warning' | 'critical' })}><option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option></select></td>
              <td><input type="checkbox" checked={a.auto_work_order} onChange={(e) => setAlert(i, { auto_work_order: e.target.checked })} /></td>
              <td><button className="btn btn-ghost btn-xs" onClick={() => setForm({ ...form, alerts: form.alerts.filter((_, j) => j !== i) })}><X size={12} /></button></td>
            </tr>
          ))}
        </tbody>
      </table>
      <button className="btn btn-ghost btn-sm" style={{ marginTop: 6 }} onClick={() => setForm({ ...form, alerts: [...form.alerts, newAlert()] })}><Plus size={13} /> Add alert rule</button>
    </Panel>
  )
}

function ApplyDialog({ template, onClose }: { template: DeviceTemplate; onClose: () => void }) {
  const [mode, setMode] = useState<'category' | 'select'>(template.device_type ? 'category' : 'select')
  const [picked, setPicked] = useState<string[]>([])
  const [result, setResult] = useState<ApplyTemplateResult | null>(null)
  const [err, setErr] = useState('')

  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api.get<Device[]>('/devices') })
  const all = devices.data ?? []
  const inCategory = useMemo(() => (devices.data ?? []).filter((d) => template.device_type && d.category === template.device_type), [devices.data, template.device_type])

  const apply = useMutation({
    mutationFn: () => api.post<ApplyTemplateResult>(`/device-templates/${template.id}/apply`, mode === 'select' ? { device_ids: picked } : {}),
    onSuccess: (r) => setResult(r),
    onError: (e) => setErr((e as Error).message),
  })

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 620 }} onClick={(e) => e.stopPropagation()}>
        <div className="modal-head"><h3><Rocket size={16} /> Apply “{template.name}”</h3><button className="btn btn-ghost btn-xs" onClick={onClose}><X size={14} /></button></div>
        <div className="modal-body">
          {result ? (
            <div>
              <div className="enc-banner info" style={{ marginBottom: 12 }}>
                Applied to {result.devices} device(s): {result.checks_created} checks created, {result.checks_skipped} already present; {result.alerts_created} alert rules created, {result.alerts_skipped} existing.
              </div>
              {result.warnings.length > 0 && <ul className="muted" style={{ fontSize: 12 }}>{result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>}
              <button className="btn btn-primary" onClick={onClose}>Done</button>
            </div>
          ) : (
            <>
              <div className="row" style={{ gap: 16, marginBottom: 12 }}>
                <label className="row" style={{ gap: 6 }}><input type="radio" checked={mode === 'category'} disabled={!template.device_type} onChange={() => setMode('category')} /> All <b>{template.device_type || '—'}</b> devices ({inCategory.length})</label>
                <label className="row" style={{ gap: 6 }}><input type="radio" checked={mode === 'select'} onChange={() => setMode('select')} /> Pick devices</label>
              </div>
              {mode === 'select' && (
                <div style={{ maxHeight: 260, overflow: 'auto', border: '1px solid var(--border)', borderRadius: 6 }}>
                  <table className="data-table">
                    <tbody>
                      {all.map((d) => (
                        <tr key={d.id}>
                          <td style={{ width: 30 }}><input type="checkbox" checked={picked.includes(d.id)} onChange={() => setPicked((p) => p.includes(d.id) ? p.filter((x) => x !== d.id) : [...p, d.id])} /></td>
                          <td className="cell-name">{d.name}</td>
                          <td className="muted">{d.category}</td>
                          <td className="mono muted">{d.primary_ip ?? '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {err && <div className="enc-banner crit" style={{ marginTop: 10 }}>{err}</div>}
              <div style={{ marginTop: 12 }}>
                <button className="btn btn-primary" disabled={apply.isPending || (mode === 'select' && picked.length === 0)} onClick={() => { setErr(''); apply.mutate() }}>
                  {apply.isPending ? 'Applying…' : `Apply${mode === 'select' ? ` to ${picked.length}` : ''}`}
                </button>
                <span className="muted" style={{ marginLeft: 12, fontSize: 12 }}>Idempotent — existing checks/rules are skipped.</span>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
