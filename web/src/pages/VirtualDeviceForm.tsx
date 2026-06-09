import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Ghost, Plus, Trash2, Download, Upload, Save, ArrowLeft } from 'lucide-react'
import { api, saveBlob, locationPaths, type Location, type Device, type VirtualDeviceReq, type VirtualImportReport } from '../api'
import { PageHeader, Panel } from '../components/ui'
import { VIRTUAL_TEMPLATES, VIRTUAL_TYPE_ORDER, VIRTUAL_STATUSES, templateFor, type Col, type Section } from './virtual/templates'

// VirtualDeviceForm: category-aware create/edit for a virtual (manually-entered)
// device. Context decides the category — a ?type= (from a category page) skips the
// picker; All-Inventory shows the type chooser first; an :id edits in place.
export function VirtualDeviceForm() {
  const { id } = useParams()
  const editing = !!id
  const [params] = useSearchParams()
  const typeParam = params.get('type') || ''
  const nav = useNavigate()
  const qc = useQueryClient()
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const locPath = useMemo(() => locationPaths(locs.data ?? []), [locs.data])

  const [req, setReq] = useState<VirtualDeviceReq>(() => seed(typeParam, params))
  const [factRows, setFactRows] = useState<{ k: string; v: string }[]>([])
  const [picked, setPicked] = useState<boolean>(editing || (!!typeParam && !!VIRTUAL_TEMPLATES[typeParam]))
  const [err, setErr] = useState<string | null>(null)
  const [report, setReport] = useState<VirtualImportReport | null>(null)

  // Edit: load the last-saved payload (lossless round-trip).
  useEffect(() => {
    if (!editing) return
    api.get<VirtualDeviceReq>(`/devices/virtual/${id}/config`).then((c) => {
      if (c && (c as VirtualDeviceReq).name) {
        setReq(c)
        setFactRows(Object.entries(c.facts ?? {}).map(([k, v]) => ({ k, v: String(v) })))
      } else {
        // Pre-blob device: fall back to its identity so at least that is editable.
        api.get<Device[]>(`/devices?category=all`).then((all) => {
          const d = all.find((x) => x.id === id)
          if (d) setReq((p) => ({ ...p, name: d.name, category: d.category, vendor: d.vendor ?? '', model: d.model ?? '', serial: d.serial ?? '', os_version: d.os_version ?? '', primary_ip: d.primary_ip ?? '', vlan: d.vlan ?? '', class: d.device_class ?? '', status: d.status }))
        })
      }
    }).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const tmpl = templateFor(req.category || 'other')
  const set = (k: keyof VirtualDeviceReq, v: unknown) => setReq((p) => ({ ...p, [k]: v }))
  const setBlock = (block: string, rows: unknown[]) => setReq((p) => ({ ...p, [block]: rows }))
  const setSingleton = (block: string, obj: Record<string, unknown>) => setReq((p) => ({ ...p, [block]: obj }))

  const save = useMutation({
    mutationFn: () => {
      const body: VirtualDeviceReq = { ...req, location_id: req.location_id || undefined, facts: factsObj(factRows) }
      return editing ? api.put<Device>(`/devices/virtual/${id}`, body) : api.post<Device>('/devices/virtual', body)
    },
    onSuccess: (dev) => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      const devId = editing ? id : (dev as Device | undefined)?.id
      nav(devId ? `/devices/${devId}` : '/inventory')
    },
    onError: (e) => setErr((e as Error).message),
  })

  const importXlsx = useMutation({
    mutationFn: async (file: File) => { const fd = new FormData(); fd.append('file', file); return api.postForm<VirtualImportReport>('/devices/virtual/import', fd) },
    onSuccess: (r) => { setReport(r); qc.invalidateQueries({ queryKey: ['devices'] }) },
    onError: (e) => setErr((e as Error).message),
  })

  const downloadTemplate = async () => {
    try {
      const t = req.category && VIRTUAL_TEMPLATES[req.category] ? `?type=${req.category}` : ''
      const blob = await api.getBlob(`/devices/virtual/template.xlsx${t}`)
      saveBlob(blob, t ? `virtual-${req.category}-template.xlsx` : 'virtual-devices-template.xlsx')
    } catch (e) { setErr((e as Error).message) }
  }

  const submit = () => {
    setErr(null)
    if (!req.name.trim()) { setErr('Name is required'); return }
    save.mutate()
  }

  // ---- Type picker (All-Inventory entry) ----
  if (!picked) {
    return (
      <div>
        <PageHeader title="Add Virtual Device" icon={Ghost} subtitle="Choose the device type — each has its own template." />
        <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(auto-fill,minmax(190px,1fr))' }}>
          {VIRTUAL_TYPE_ORDER.map((cat) => {
            const t = VIRTUAL_TEMPLATES[cat]; const Icon = t.icon
            return (
              <button key={cat} className="card" style={{ textAlign: 'left', cursor: 'pointer', display: 'flex', flexDirection: 'column', gap: 6, padding: 16 }}
                onClick={() => { setReq((p) => ({ ...p, category: cat })); setPicked(true) }}>
                <span style={{ color: 'var(--brand)' }}><Icon size={22} /></span>
                <strong>{t.label}</strong>
                <small className="muted">{t.blurb}</small>
              </button>
            )
          })}
        </div>
        <div style={{ marginTop: 16 }}><button className="btn btn-ghost" onClick={() => nav('/inventory')}><ArrowLeft size={14} /> Cancel</button></div>
      </div>
    )
  }

  return (
    <div>
      <PageHeader title={editing ? `Edit Virtual ${tmpl.label}` : `Add Virtual ${tmpl.label}`} icon={tmpl.icon}
        subtitle={tmpl.blurb}
        actions={
          <>
            <button className="btn btn-ghost btn-sm" onClick={downloadTemplate}><Download size={14} /> Excel template</button>
            <label className="btn btn-ghost btn-sm" style={{ cursor: 'pointer' }}>
              <Upload size={14} /> Import Excel
              <input type="file" accept=".xlsx" style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) importXlsx.mutate(f) }} />
            </label>
          </>
        } />

      {err && <div className="enc-banner crit" style={{ marginBottom: 12 }}>{err}</div>}
      {importXlsx.isPending && <div className="loading">Importing…</div>}
      {report && <ImportReport report={report} onClose={() => setReport(null)} onGo={() => nav('/inventory')} />}

      <Panel title="Identity">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: 12 }}>
          <Field label="Name *"><input className="field" value={req.name} onChange={(e) => set('name', e.target.value)} placeholder={`Virtual-${tmpl.label}`} /></Field>
          <Field label="Type">
            <select className="field" value={req.category} onChange={(e) => set('category', e.target.value)}>
              {VIRTUAL_TYPE_ORDER.map((c) => <option key={c} value={c}>{VIRTUAL_TEMPLATES[c].label}</option>)}
            </select>
          </Field>
          <Field label="Status">
            <select className="field" value={req.status ?? 'up'} onChange={(e) => set('status', e.target.value)}>{VIRTUAL_STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}</select>
          </Field>
          <Field label="Management IP"><input className="field" value={req.primary_ip ?? ''} onChange={(e) => set('primary_ip', e.target.value)} placeholder="optional" /></Field>
          <Field label="Vendor"><input className="field" value={req.vendor ?? ''} onChange={(e) => set('vendor', e.target.value)} /></Field>
          <Field label="Model"><input className="field" value={req.model ?? ''} onChange={(e) => set('model', e.target.value)} /></Field>
          <Field label="Serial"><input className="field" value={req.serial ?? ''} onChange={(e) => set('serial', e.target.value)} /></Field>
          <Field label="OS / Firmware"><input className="field" value={req.os_version ?? ''} onChange={(e) => set('os_version', e.target.value)} /></Field>
          <Field label="VLAN (mgmt)"><input className="field" value={req.vlan ?? ''} onChange={(e) => set('vlan', e.target.value)} /></Field>
          <Field label="Class"><input className="field" value={req.class ?? ''} onChange={(e) => set('class', e.target.value)} placeholder="core_switch / access_switch / …" /></Field>
          <Field label="Criticality">
            <select className="field" value={req.criticality ?? ''} onChange={(e) => set('criticality', e.target.value)}>
              <option value="">—</option>{['low', 'normal', 'high', 'critical'].map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          </Field>
          <Field label="Location">
            <select className="field" value={req.location_id ?? ''} onChange={(e) => set('location_id', e.target.value)}>
              <option value="">— none —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{locPath[l.id] ?? l.name}</option>)}
            </select>
          </Field>
          <Field label="Site"><input className="field" value={req.site ?? ''} onChange={(e) => set('site', e.target.value)} /></Field>
          <Field label="Notes" wide><input className="field" value={req.notes ?? ''} onChange={(e) => set('notes', e.target.value)} /></Field>
        </div>
      </Panel>

      {tmpl.sections.map((sec, i) => (
        <SectionEditor key={i} section={sec} req={req} factRows={factRows}
          onRows={setBlock} onSingleton={setSingleton} onRoles={(r) => set('roles', r)} onFacts={setFactRows} />
      ))}

      <div className="row" style={{ gap: 10, marginTop: 16, justifyContent: 'flex-end' }}>
        <button className="btn btn-ghost" onClick={() => nav(editing ? `/devices/${id}` : '/inventory')}>Cancel</button>
        <button className="btn btn-primary" disabled={save.isPending} onClick={submit}><Save size={15} /> {save.isPending ? 'Saving…' : editing ? 'Save changes' : `Create virtual ${tmpl.label.toLowerCase()}`}</button>
      </div>
    </div>
  )
}

// ---- section dispatch -------------------------------------------------------

function SectionEditor({ section, req, factRows, onRows, onSingleton, onRoles, onFacts }: {
  section: Section; req: VirtualDeviceReq; factRows: { k: string; v: string }[]
  onRows: (block: string, rows: unknown[]) => void
  onSingleton: (block: string, obj: Record<string, unknown>) => void
  onRoles: (roles: string[]) => void
  onFacts: (rows: { k: string; v: string }[]) => void
}) {
  if (section.kind === 'singleton') {
    const obj = ((req as unknown as Record<string, Record<string, unknown>>)[section.block]) ?? {}
    return (
      <Panel title={section.title}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))', gap: 12 }}>
          {section.cols.map((c) => (
            <Field key={c.key} label={c.label}>{cellInput(c, obj[c.key], (v) => onSingleton(section.block, { ...obj, [c.key]: v }))}</Field>
          ))}
        </div>
      </Panel>
    )
  }
  if (section.kind === 'roles') {
    const roles = req.roles ?? []
    return (
      <Panel title={section.title} subtitle={roles.length ? `${roles.length}` : section.hint}
        actions={<button className="btn btn-ghost btn-xs" onClick={() => onRoles([...roles, ''])}><Plus size={12} /> Add</button>} pad={roles.length === 0}>
        {roles.length === 0 ? <div className="muted" style={{ fontSize: 13 }}>None.</div> : (
          <div className="stack" style={{ gap: 6 }}>{roles.map((r, i) => (
            <div key={i} className="row" style={{ gap: 6 }}>
              <input className="field" value={r} onChange={(e) => onRoles(roles.map((x, j) => j === i ? e.target.value : x))} style={{ flex: 1 }} />
              <button className="btn btn-ghost btn-xs" onClick={() => onRoles(roles.filter((_, j) => j !== i))}><Trash2 size={12} /></button>
            </div>))}</div>
        )}
      </Panel>
    )
  }
  if (section.kind === 'facts') {
    return (
      <Panel title={section.title} subtitle={factRows.length ? `${factRows.length}` : section.hint}
        actions={<button className="btn btn-ghost btn-xs" onClick={() => onFacts([...factRows, { k: '', v: '' }])}><Plus size={12} /> Add</button>} pad={factRows.length === 0}>
        {factRows.length === 0 ? <div className="muted" style={{ fontSize: 13 }}>{section.hint ?? 'None.'}</div> : (
          <table className="data-table"><thead><tr><th>Field</th><th>Value</th><th /></tr></thead>
            <tbody>{factRows.map((f, i) => (
              <tr key={i}>
                <td style={{ width: 200 }}><input className="field" value={f.k} onChange={(e) => onFacts(factRows.map((x, j) => j === i ? { ...x, k: e.target.value } : x))} placeholder="cpu" /></td>
                <td><input className="field" value={f.v} onChange={(e) => onFacts(factRows.map((x, j) => j === i ? { ...x, v: e.target.value } : x))} /></td>
                <td><button className="btn btn-ghost btn-xs" onClick={() => onFacts(factRows.filter((_, j) => j !== i))}><Trash2 size={12} /></button></td>
              </tr>))}</tbody>
          </table>
        )}
      </Panel>
    )
  }
  // rows
  const rows = (((req as unknown as Record<string, unknown[]>)[section.block]) ?? []) as Record<string, unknown>[]
  const blank = () => {
    const r: Record<string, unknown> = {}
    section.cols.forEach((c) => { r[c.key] = c.type === 'bool' ? (c.key === 'up') : c.type === 'int' || c.type === 'int64' ? 0 : c.type === 'intlist' ? [] : '' })
    if (section.cols.some((c) => c.key === 'if_index')) r.if_index = rows.reduce((m, x) => Math.max(m, Number(x.if_index) || 0), 0) + 1
    return r
  }
  const addN = (n: number) => onRows(section.block, [...rows, ...Array.from({ length: n }, blank)])
  return (
    <Panel title={section.title} subtitle={rows.length ? `${rows.length}` : section.hint}
      actions={<div className="row" style={{ gap: 6 }}>
        {section.bulk?.map((n) => <button key={n} className="btn btn-ghost btn-xs" onClick={() => addN(n)}>+{n}</button>)}
        <button className="btn btn-ghost btn-xs" onClick={() => onRows(section.block, [...rows, blank()])}><Plus size={12} /> Add</button>
      </div>} pad={rows.length === 0}>
      {rows.length === 0 ? <div className="muted" style={{ fontSize: 13 }}>{section.hint ?? 'None added yet.'}</div> : (
        <table className="data-table"><thead><tr>{section.cols.map((c) => <th key={c.key}>{c.label}</th>)}<th /></tr></thead>
          <tbody>{rows.map((row, i) => (
            <tr key={i}>
              {section.cols.map((c) => <td key={c.key} style={c.w ? { width: c.w } : undefined}>{cellInput(c, row[c.key], (v) => onRows(section.block, rows.map((x, j) => j === i ? { ...x, [c.key]: v } : x)))}</td>)}
              <td><button className="btn btn-ghost btn-xs" onClick={() => onRows(section.block, rows.filter((_, j) => j !== i))}><Trash2 size={12} /></button></td>
            </tr>))}</tbody>
        </table>
      )}
    </Panel>
  )
}

// cellInput renders the right control for a column type.
function cellInput(c: Col, value: unknown, onChange: (v: unknown) => void) {
  switch (c.type) {
    case 'bool':
      return <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} />
    case 'int':
    case 'int64':
      return <input className="field" type="number" value={Number(value) || 0} onChange={(e) => onChange(Number(e.target.value))} />
    case 'select':
      return <select className="field" value={String(value ?? '')} onChange={(e) => onChange(e.target.value)}>{(c.opts ?? []).map((o) => <option key={o} value={o}>{o}</option>)}</select>
    case 'intlist':
      return <input className="field" value={Array.isArray(value) ? (value as number[]).join(',') : ''} onChange={(e) => onChange(e.target.value.split(/[,;\s]+/).map((x) => parseInt(x, 10)).filter((n) => n > 0))} placeholder="20,30" />
    default:
      return <input className={c.type === 'mac' || c.type === 'ip' ? 'field mono' : 'field'} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} />
  }
}

function ImportReport({ report, onClose, onGo }: { report: VirtualImportReport; onClose: () => void; onGo: () => void }) {
  const tone = report.failed > 0 ? 'warn' : ''
  return (
    <Panel title="Import result"
      actions={<><button className="btn btn-ghost btn-xs" onClick={onClose}>Dismiss</button><button className="btn btn-primary btn-xs" onClick={onGo}>View inventory</button></>}>
      <div className={`enc-banner ${tone}`} style={{ marginBottom: report.errors?.length ? 10 : 0 }}>
        Created <strong>{report.created}</strong> · Updated <strong>{report.updated}</strong> · Failed <strong>{report.failed}</strong>
        {report.devices?.length ? ` — ${report.devices.join(', ')}` : ''}
      </div>
      {report.errors?.length ? (
        <table className="data-table"><thead><tr><th>Sheet</th><th>Row</th><th>Field</th><th>Problem</th></tr></thead>
          <tbody>{report.errors.map((e, i) => <tr key={i}><td>{e.sheet}</td><td>{e.row}</td><td>{e.field ?? '—'}</td><td>{e.message}</td></tr>)}</tbody>
        </table>
      ) : null}
    </Panel>
  )
}

function Field({ label, children, wide }: { label: string; children: React.ReactNode; wide?: boolean }) {
  return <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13, gridColumn: wide ? '1 / -1' : undefined }}><span className="muted">{label}</span>{children}</label>
}

// seed builds the initial request, applying ?type and any prefill (connSwitch/port/vlan).
function seed(type: string, params: URLSearchParams): VirtualDeviceReq {
  const r: VirtualDeviceReq = { name: '', category: VIRTUAL_TEMPLATES[type] ? type : '', status: 'up' }
  const connSwitch = params.get('connSwitch') || ''
  const connPort = params.get('connPort') || ''
  if (connSwitch || connPort) r.neighbors = [{ remote_name: connSwitch, remote_port: connPort, protocol: 'manual' }]
  const vlan = params.get('vlan') || ''
  if (vlan) r.vlan = vlan
  return r
}

function factsObj(rows: { k: string; v: string }[]): Record<string, string> {
  const o: Record<string, string> = {}
  rows.forEach(({ k, v }) => { if (k.trim()) o[k.trim()] = v })
  return o
}
