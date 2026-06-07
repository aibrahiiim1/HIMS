import { useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ScanLine, Plus, DownloadCloud, FlaskConical, Trash2, Upload, Download, Pencil, X } from 'lucide-react'
import { api, saveBlob, type VendorFingerprint, type FingerprintMatchResp, type FingerprintTestResp, type FingerprintImportResp, type Device } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager } from '../components/ui'

const KINDS = ['oid', 'service', 'sysname', 'port', 'http', 'ssh']
const kindCls = (k: string) =>
  k === 'oid' ? 'badge-info'
    : k === 'service' ? 'badge-lldp'
      : k === 'sysname' ? 'badge-info'
        : k === 'http' ? 'badge-up'
          : k === 'ssh' ? 'badge-warning'
            : 'badge-unknown'

type FpForm = { id?: string; kind: string; pattern: string; vendor: string; device_type: string; model: string; confidence: number; priority: number }
const emptyForm: FpForm = { kind: 'oid', pattern: '', vendor: '', device_type: '', model: '', confidence: 60, priority: 100 }

export function VendorFingerprints() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['vendor-fingerprints'], queryFn: () => api.get<VendorFingerprint[]>('/vendor-fingerprints') })
  const inv = () => qc.invalidateQueries({ queryKey: ['vendor-fingerprints'] })
  const [form, setForm] = useState<FpForm>(emptyForm)
  const [msg, setMsg] = useState('')
  const [kindFilter, setKindFilter] = useState('')
  const [sourceFilter, setSourceFilter] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)
  const editing = !!form.id

  const save = useMutation({
    mutationFn: () => editing
      ? api.patch<VendorFingerprint>(`/vendor-fingerprints/${form.id}`, { ...form, enabled: true })
      : api.post<VendorFingerprint>('/vendor-fingerprints', form),
    onSuccess: () => { setForm(emptyForm); setMsg(editing ? 'Fingerprint updated.' : 'Fingerprint added.'); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const seed = useMutation({
    mutationFn: () => api.post<{ created: number; skipped: number; library_size: number }>('/vendor-fingerprints/seed', {}),
    onSuccess: (r) => { setMsg(`Imported standard library: ${r.created} added, ${r.skipped} already present (${r.library_size} total).`); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const toggle = useMutation({ mutationFn: (f: VendorFingerprint) => api.patch(`/vendor-fingerprints/${f.id}`, { ...f, enabled: !f.enabled }), onSuccess: inv })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/vendor-fingerprints/${id}`), onSuccess: inv })
  const importMut = useMutation({
    mutationFn: (body: { text: string; csv: boolean }) =>
      api.postText<FingerprintImportResp>('/vendor-fingerprints/import', body.text, body.csv ? 'text/csv' : 'application/json'),
    onSuccess: (r) => { setMsg(`Imported ${r.imported} fingerprint(s)${r.failed ? `, ${r.failed} failed` : ''}.`); inv() },
    onError: (e) => setMsg((e as Error).message),
  })

  const exportFile = async (format: 'json' | 'csv') => {
    try {
      const blob = await api.getBlob(`/vendor-fingerprints/export?format=${format}`)
      saveBlob(blob, `vendor-fingerprints.${format}`)
    } catch (e) { setMsg((e as Error).message) }
  }
  const onPickFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (!f) return
    const csv = f.name.toLowerCase().endsWith('.csv')
    f.text().then((text) => importMut.mutate({ text, csv }))
    e.target.value = ''
  }

  const rows = q.data ?? []
  const byKind = (k: string) => rows.filter((r) => r.kind === k).length
  const userCount = rows.filter((r) => r.source === 'user').length
  const shown = useMemo(() => {
    let all = q.data ?? []
    if (kindFilter) all = all.filter((r) => r.kind === kindFilter)
    if (sourceFilter) all = all.filter((r) => r.source === sourceFilter)
    return all
  }, [q.data, kindFilter, sourceFilter])
  const paged = usePaged(shown, { pageSize: 12 })

  return (
    <div>
      <PageHeader title="Vendor Fingerprints" icon={ScanLine}
        subtitle="A curated, operator-editable library of vendor signatures (SNMP OID / sysDescr / sysName / HTTP / SSH / port) that drives device classification"
        actions={
          <div className="row" style={{ gap: 8 }}>
            <button className="btn btn-ghost btn-sm" onClick={() => exportFile('json')}><Download size={14} /> Export JSON</button>
            <button className="btn btn-ghost btn-sm" onClick={() => exportFile('csv')}><Download size={14} /> Export CSV</button>
            <button className="btn btn-ghost btn-sm" onClick={() => fileRef.current?.click()}><Upload size={14} /> Import</button>
            <input ref={fileRef} type="file" accept=".json,.csv" style={{ display: 'none' }} onChange={onPickFile} />
            <button className="btn btn-primary btn-sm" disabled={seed.isPending} onClick={() => { setMsg(''); seed.mutate() }}><DownloadCloud size={14} /> {seed.isPending ? 'Importing…' : 'Import standard library'}</button>
          </div>} />

      <div className="kpi-grid">
        <Kpi label="Total" value={rows.length} icon={ScanLine} tone="info" />
        <Kpi label="User-defined" value={userCount} tone={userCount ? 'ok' : undefined} />
        <Kpi label="SNMP OID" value={byKind('oid')} />
        <Kpi label="sysDescr / sysName" value={byKind('service') + byKind('sysname')} />
      </div>

      {msg && <div className={'enc-banner ' + (/added|updated|library|Imported|present/.test(msg) ? 'info' : 'crit')} style={{ marginBottom: 12 }}>{msg}</div>}

      <TestAgainstDevice />
      <MatchTester />

      <Panel title={editing ? 'Edit Fingerprint' : 'New Fingerprint'} icon={editing ? Pencil : Plus}
        actions={editing ? <button className="btn btn-ghost btn-xs" onClick={() => setForm(emptyForm)}><X size={12} /> Cancel edit</button> : undefined}>
        <div className="row" style={{ flexWrap: 'wrap', gap: 8 }}>
          <select className="field" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
          <input className="field" style={{ flex: 1, minWidth: 200 }} placeholder="pattern (OID prefix / banner substring / port)" value={form.pattern} onChange={(e) => setForm({ ...form, pattern: e.target.value })} />
          <input className="field" placeholder="vendor" value={form.vendor} onChange={(e) => setForm({ ...form, vendor: e.target.value })} />
          <input className="field" placeholder="device type" value={form.device_type} onChange={(e) => setForm({ ...form, device_type: e.target.value })} />
          <input className="field" placeholder="model (optional)" value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} />
          <input className="field" style={{ width: 84 }} type="number" min={0} max={100} value={form.confidence} onChange={(e) => setForm({ ...form, confidence: Number(e.target.value) })} title="confidence %" />
          <input className="field" style={{ width: 84 }} type="number" min={1} value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })} title="priority (lower runs first)" />
          <button className="btn btn-primary" disabled={!form.pattern || save.isPending} onClick={() => save.mutate()}>{editing ? 'Save' : 'Add'}</button>
        </div>
        <p className="muted" style={{ fontSize: 12, marginTop: 6 }}>User-defined rules outrank the built-in catalog at equal confidence. Lower priority runs first among ties.</p>
      </Panel>

      <Panel title="Library" icon={ScanLine} subtitle={`${shown.length} shown`} pad={false}
        actions={
          <div className="row" style={{ gap: 6 }}>
            <select className="field" value={sourceFilter} onChange={(e) => setSourceFilter(e.target.value)}>
              <option value="">all sources</option>
              <option value="user">user-defined</option>
              <option value="builtin">built-in</option>
            </select>
            <select className="field" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
              <option value="">all kinds</option>
              {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </div>}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={ScanLine} title="No fingerprints yet" message="Click “Import standard library” for a comprehensive starter set (real enterprise OIDs + banners), or add your own." action={<button className="btn btn-primary btn-sm" onClick={() => seed.mutate()}>Import standard library</button>} />}
        {shown.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Kind</th><th>Pattern</th><th>Vendor</th><th>Device type</th><th>Model</th><th>Conf</th><th>Prio</th><th>Source</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {paged.slice.map((f) => (
                <tr key={f.id} className={form.id === f.id ? 'row-selected' : ''}>
                  <td><span className={`badge ${kindCls(f.kind)}`}>{f.kind}</span></td>
                  <td className="mono">{f.pattern}</td>
                  <td>{f.vendor || '—'}</td>
                  <td>{f.device_type || '—'}</td>
                  <td>{f.model || '—'}</td>
                  <td>{f.confidence}%</td>
                  <td>{f.priority}</td>
                  <td>{f.source === 'user' ? <span className="badge badge-up">user</span> : <span className="badge badge-unknown">built-in</span>}</td>
                  <td>{f.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" onClick={() => setForm({ id: f.id, kind: f.kind, pattern: f.pattern, vendor: f.vendor, device_type: f.device_type, model: f.model || '', confidence: f.confidence, priority: f.priority })}><Pencil size={12} /></button>
                    <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(f)}>{f.enabled ? 'Disable' : 'Enable'}</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(f.id)}><Trash2 size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {shown.length > 0 && <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />}
      </Panel>
    </div>
  )
}

// TestAgainstDevice runs the live library against a real device's STORED SNMP
// identity (by IP or device), showing matched/raw SNMP/rule/vendor+category+model.
function TestAgainstDevice() {
  const [ip, setIp] = useState('')
  const devs = useQuery({ queryKey: ['devices', 'fp-test'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const t = useMutation({
    mutationFn: (target: string) => api.post<FingerprintTestResp>('/vendor-fingerprints/test-device', { ip: target }),
  })
  const r = t.data
  return (
    <Panel title="Test Fingerprint Against Device" icon={FlaskConical} subtitle="Re-evaluate the library against a real device's stored SNMP identity (no re-probe)">
      <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
        <input className="field mono" style={{ minWidth: 240 }} list="fp-device-ips" placeholder="device IP, e.g. 172.21.96.100" value={ip} onChange={(e) => setIp(e.target.value)} />
        <datalist id="fp-device-ips">
          {(devs.data ?? []).filter((d) => d.primary_ip).map((d) => <option key={d.id} value={d.primary_ip!}>{d.name} ({d.category})</option>)}
        </datalist>
        <button className="btn btn-primary btn-sm" disabled={!ip || t.isPending} onClick={() => t.mutate(ip.trim())}><FlaskConical size={13} /> {t.isPending ? 'Testing…' : 'Test'}</button>
      </div>
      {t.isError && <p className="enc-banner crit" style={{ marginTop: 10 }}>{(t.error as Error).message}</p>}
      {r && (
        <div style={{ marginTop: 12 }}>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 8 }}>
            <strong>{r.device_name}</strong>
            <span className="muted">currently:</span>
            <span className="badge badge-unknown">{r.current_category || 'unknown'}</span>
            <span className="muted">{r.current_vendor || '—'}{r.current_model ? ` / ${r.current_model}` : ''}</span>
          </div>
          <table className="data-table" style={{ marginBottom: 10 }}>
            <tbody>
              <tr><th style={{ width: 160 }}>sysObjectID</th><td className="mono">{r.raw_snmp.sysobjectid || '—'}</td></tr>
              <tr><th>sysDescr</th><td className="mono" style={{ whiteSpace: 'normal' }}>{r.raw_snmp.sysdescr || '—'}</td></tr>
              <tr><th>sysName</th><td className="mono">{r.raw_snmp.sysname || '—'}</td></tr>
            </tbody>
          </table>
          {!r.matched && <p className="muted">No fingerprint matched this device's stored SNMP evidence. Bind/collect SNMP first, or add a fingerprint.</p>}
          {r.matched && r.top && (
            <div className="enc-banner info">
              <strong>Matched</strong> by <span className="mono">{r.top.rule}</span> → <strong>{r.top.vendor || '—'}</strong>
              {' '}/ <span className="badge badge-up">{r.top.category || r.top.vendor}</span>
              {r.top.model ? <> / model <strong>{r.top.model}</strong></> : null}
              {' '}at <strong>{r.top.confidence}%</strong> confidence
              {(r.top.category && r.top.category !== r.current_category) ? <span style={{ marginLeft: 8 }} className="badge badge-warning">re-scan to apply</span> : null}
            </div>
          )}
          {r.matched && r.results.length > 1 && (
            <table className="data-table" style={{ marginTop: 10 }}>
              <thead><tr><th>Vendor</th><th>Device type</th><th>Confidence</th><th>Matched by</th></tr></thead>
              <tbody>
                {r.results.map((x, i) => (
                  <tr key={i} className={i === 0 ? 'row-selected' : ''}>
                    <td>{x.vendor || '—'}</td><td>{x.device_type || '—'}</td><td>{x.confidence}%</td>
                    <td className="mono muted">{x.kind}: {x.pattern}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </Panel>
  )
}

function MatchTester() {
  const [ev, setEv] = useState({ sysobjectid: '', sysdescr: '', sysname: '', http_server: '', ssh_banner: '', ports: '' })
  const m = useMutation({
    mutationFn: () => api.post<FingerprintMatchResp>('/vendor-fingerprints/match', {
      sysobjectid: ev.sysobjectid || undefined,
      sysdescr: ev.sysdescr || undefined,
      sysname: ev.sysname || undefined,
      http_server: ev.http_server || undefined,
      ssh_banner: ev.ssh_banner || undefined,
      ports: ev.ports ? ev.ports.split(',').map((x) => Number(x.trim())).filter((n) => n > 0) : undefined,
    }),
  })
  const results = m.data?.results ?? []

  return (
    <Panel title="Match Tester" icon={FlaskConical} subtitle="Paste raw evidence to see what the library identifies">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 10 }}>
        <label className="form-field">SNMP sysObjectID<input className="field mono" value={ev.sysobjectid} onChange={(e) => setEv({ ...ev, sysobjectid: e.target.value })} placeholder="1.3.6.1.4.1.9.1.516" /></label>
        <label className="form-field">SNMP sysDescr<input className="field" value={ev.sysdescr} onChange={(e) => setEv({ ...ev, sysdescr: e.target.value })} placeholder="Cisco IOS Software, C2960…" /></label>
        <label className="form-field">SNMP sysName<input className="field" value={ev.sysname} onChange={(e) => setEv({ ...ev, sysname: e.target.value })} placeholder="XIQC.example.com" /></label>
        <label className="form-field">HTTP Server header<input className="field" value={ev.http_server} onChange={(e) => setEv({ ...ev, http_server: e.target.value })} placeholder="App-webs/" /></label>
        <label className="form-field">SSH banner<input className="field" value={ev.ssh_banner} onChange={(e) => setEv({ ...ev, ssh_banner: e.target.value })} placeholder="SSH-2.0-OpenSSH_8.0" /></label>
        <label className="form-field">Open ports (comma)<input className="field" value={ev.ports} onChange={(e) => setEv({ ...ev, ports: e.target.value })} placeholder="22, 9100" /></label>
      </div>
      <div style={{ marginTop: 10 }}>
        <button className="btn btn-primary btn-sm" disabled={m.isPending} onClick={() => m.mutate()}><FlaskConical size={13} /> {m.isPending ? 'Matching…' : 'Match'}</button>
      </div>
      {m.data && (
        results.length === 0
          ? <p className="muted" style={{ fontSize: 13, marginTop: 10 }}>No fingerprints matched this evidence.</p>
          : (
            <table className="data-table" style={{ marginTop: 12 }}>
              <thead><tr><th>Vendor</th><th>Device type</th><th>Confidence</th><th>Matched by</th></tr></thead>
              <tbody>
                {results.map((r, i) => (
                  <tr key={i} className={i === 0 ? 'row-selected' : ''}>
                    <td className="cell-name">{r.vendor || '—'}{i === 0 && <span className="badge badge-up" style={{ marginLeft: 6 }}>best</span>}</td>
                    <td>{r.device_type ? <span className="badge badge-unknown">{r.device_type}</span> : '—'}</td>
                    <td>{r.confidence}%</td>
                    <td className="mono muted">{r.kind}: {r.pattern}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
      )}
    </Panel>
  )
}
