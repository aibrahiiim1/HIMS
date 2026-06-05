import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ScanLine, Plus, DownloadCloud, FlaskConical, Trash2 } from 'lucide-react'
import { api, type VendorFingerprint, type FingerprintMatchResp } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager } from '../components/ui'

const KINDS = ['oid', 'service', 'port', 'http', 'ssh']
const kindCls = (k: string) => (k === 'oid' ? 'badge-info' : k === 'service' ? 'badge-lldp' : k === 'http' ? 'badge-up' : k === 'ssh' ? 'badge-warning' : 'badge-unknown')

export function VendorFingerprints() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['vendor-fingerprints'], queryFn: () => api.get<VendorFingerprint[]>('/vendor-fingerprints') })
  const inv = () => qc.invalidateQueries({ queryKey: ['vendor-fingerprints'] })
  const [form, setForm] = useState({ kind: 'oid', pattern: '', vendor: '', device_type: '', confidence: 60 })
  const [msg, setMsg] = useState('')
  const [kindFilter, setKindFilter] = useState('')

  const create = useMutation({
    mutationFn: () => api.post<VendorFingerprint>('/vendor-fingerprints', form),
    onSuccess: () => { setForm({ kind: 'oid', pattern: '', vendor: '', device_type: '', confidence: 60 }); setMsg('Fingerprint added.'); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const seed = useMutation({
    mutationFn: () => api.post<{ created: number; skipped: number; library_size: number }>('/vendor-fingerprints/seed', {}),
    onSuccess: (r) => { setMsg(`Imported standard library: ${r.created} added, ${r.skipped} already present (${r.library_size} total).`); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const toggle = useMutation({ mutationFn: (f: VendorFingerprint) => api.patch(`/vendor-fingerprints/${f.id}`, { ...f, enabled: !f.enabled }), onSuccess: inv })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/vendor-fingerprints/${id}`), onSuccess: inv })

  const rows = q.data ?? []
  const byKind = (k: string) => rows.filter((r) => r.kind === k).length
  const shown = useMemo(() => { const all = q.data ?? []; return kindFilter ? all.filter((r) => r.kind === kindFilter) : all }, [q.data, kindFilter])
  const paged = usePaged(shown, { pageSize: 10 })

  return (
    <div>
      <PageHeader title="Vendor Fingerprints" icon={ScanLine}
        subtitle="A curated library of vendor signatures (SNMP OID / sysDescr / HTTP / SSH / port) for identifying devices"
        actions={<button className="btn btn-primary btn-sm" disabled={seed.isPending} onClick={() => { setMsg(''); seed.mutate() }}><DownloadCloud size={14} /> {seed.isPending ? 'Importing…' : 'Import standard library'}</button>} />

      <div className="kpi-grid">
        <Kpi label="Total" value={rows.length} icon={ScanLine} tone="info" />
        <Kpi label="SNMP OID" value={byKind('oid')} />
        <Kpi label="sysDescr / Port" value={byKind('service') + byKind('port')} />
        <Kpi label="HTTP / SSH" value={byKind('http') + byKind('ssh')} />
      </div>

      {msg && <div className={'enc-banner ' + (msg.includes('library') || msg.includes('added') ? 'info' : 'crit')} style={{ marginBottom: 12 }}>{msg}</div>}

      <MatchTester />

      <Panel title="New Fingerprint" icon={Plus}>
        <div className="row" style={{ flexWrap: 'wrap', gap: 8 }}>
          <select className="field" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
          <input className="field" style={{ flex: 1, minWidth: 220 }} placeholder="pattern (OID prefix / banner substring / port)" value={form.pattern} onChange={(e) => setForm({ ...form, pattern: e.target.value })} />
          <input className="field" placeholder="vendor" value={form.vendor} onChange={(e) => setForm({ ...form, vendor: e.target.value })} />
          <input className="field" placeholder="device type" value={form.device_type} onChange={(e) => setForm({ ...form, device_type: e.target.value })} />
          <input className="field" style={{ width: 90 }} type="number" min={0} max={100} value={form.confidence} onChange={(e) => setForm({ ...form, confidence: Number(e.target.value) })} title="confidence %" />
          <button className="btn btn-primary" disabled={!form.pattern || create.isPending} onClick={() => create.mutate()}>Add</button>
        </div>
      </Panel>

      <Panel title="Library" icon={ScanLine} subtitle={`${shown.length}${kindFilter ? ` ${kindFilter}` : ''}`} pad={false}
        actions={
          <select className="field" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
            <option value="">all kinds</option>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={ScanLine} title="No fingerprints yet" message="Click “Import standard library” for a comprehensive starter set (real enterprise OIDs + banners), or add your own." action={<button className="btn btn-primary btn-sm" onClick={() => seed.mutate()}>Import standard library</button>} />}
        {shown.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Kind</th><th>Pattern</th><th>Vendor</th><th>Device type</th><th>Confidence</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {paged.slice.map((f) => (
                <tr key={f.id}>
                  <td><span className={`badge ${kindCls(f.kind)}`}>{f.kind}</span></td>
                  <td className="mono">{f.pattern}</td>
                  <td>{f.vendor || '—'}</td>
                  <td>{f.device_type || '—'}</td>
                  <td>{f.confidence}%</td>
                  <td>{f.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
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

function MatchTester() {
  const [ev, setEv] = useState({ sysobjectid: '', sysdescr: '', http_server: '', ssh_banner: '', ports: '' })
  const m = useMutation({
    mutationFn: () => api.post<FingerprintMatchResp>('/vendor-fingerprints/match', {
      sysobjectid: ev.sysobjectid || undefined,
      sysdescr: ev.sysdescr || undefined,
      http_server: ev.http_server || undefined,
      ssh_banner: ev.ssh_banner || undefined,
      ports: ev.ports ? ev.ports.split(',').map((x) => Number(x.trim())).filter((n) => n > 0) : undefined,
    }),
  })
  const results = m.data?.results ?? []

  return (
    <Panel title="Match Tester" icon={FlaskConical} subtitle="Paste device evidence to see what the library identifies">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 10 }}>
        <label className="form-field">SNMP sysObjectID<input className="field mono" value={ev.sysobjectid} onChange={(e) => setEv({ ...ev, sysobjectid: e.target.value })} placeholder="1.3.6.1.4.1.9.1.516" /></label>
        <label className="form-field">SNMP sysDescr<input className="field" value={ev.sysdescr} onChange={(e) => setEv({ ...ev, sysdescr: e.target.value })} placeholder="Cisco IOS Software, C2960…" /></label>
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
