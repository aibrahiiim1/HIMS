import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ScanLine, Plus } from 'lucide-react'
import { api, type VendorFingerprint } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

const KINDS = ['oid', 'service', 'port', 'http', 'ssh']

export function VendorFingerprints() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['vendor-fingerprints'], queryFn: () => api.get<VendorFingerprint[]>('/vendor-fingerprints') })
  const inv = () => qc.invalidateQueries({ queryKey: ['vendor-fingerprints'] })
  const [form, setForm] = useState({ kind: 'oid', pattern: '', vendor: '', device_type: '', confidence: 60 })
  const [msg, setMsg] = useState('')

  const create = useMutation({
    mutationFn: () => api.post<VendorFingerprint>('/vendor-fingerprints', form),
    onSuccess: () => { setForm({ kind: 'oid', pattern: '', vendor: '', device_type: '', confidence: 60 }); setMsg('Fingerprint added.'); inv() },
    onError: (e) => setMsg((e as Error).message),
  })
  const toggle = useMutation({ mutationFn: (f: VendorFingerprint) => api.patch(`/vendor-fingerprints/${f.id}`, { ...f, enabled: !f.enabled }), onSuccess: inv })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/vendor-fingerprints/${id}`), onSuccess: inv })

  const rows = q.data ?? []
  const byKind = (k: string) => rows.filter((r) => r.kind === k).length

  return (
    <div>
      <PageHeader title="Vendor Fingerprints" icon={ScanLine} subtitle="Operator-managed signatures the classifier matches to a vendor + device type" />
      <div className="kpi-grid">
        <Kpi label="Total" value={rows.length} icon={ScanLine} tone="info" />
        <Kpi label="OID / SNMP" value={byKind('oid')} tone="default" />
        <Kpi label="Service / Port" value={byKind('service') + byKind('port')} tone="default" />
        <Kpi label="HTTP / SSH" value={byKind('http') + byKind('ssh')} tone="default" />
      </div>

      <Panel title="New Fingerprint" icon={Plus}>
        <div className="row">
          <select className="field" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
          <input className="field" style={{ flex: 1, minWidth: 200 }} placeholder="pattern (OID prefix / banner substring / port / regex)" value={form.pattern} onChange={(e) => setForm({ ...form, pattern: e.target.value })} />
          <input className="field" placeholder="vendor" value={form.vendor} onChange={(e) => setForm({ ...form, vendor: e.target.value })} />
          <input className="field" placeholder="device type" value={form.device_type} onChange={(e) => setForm({ ...form, device_type: e.target.value })} />
          <input className="field" style={{ width: 90 }} type="number" min={0} max={100} value={form.confidence} onChange={(e) => setForm({ ...form, confidence: Number(e.target.value) })} title="confidence %" />
          <button className="btn btn-primary" disabled={!form.pattern || create.isPending} onClick={() => create.mutate()}>Add</button>
        </div>
        {msg && <div className="muted" style={{ fontSize: 12, marginTop: 8 }}>{msg}</div>}
      </Panel>

      <Panel title="Fingerprints" icon={ScanLine} subtitle={`${rows.length}`} pad={false}>
        {q.isLoading && <div className="loading">Loading…</div>}
        {q.data && rows.length === 0 && <EmptyState icon={ScanLine} title="No fingerprints yet" message="Add OID/service/port/HTTP/SSH signatures to drive vendor and device-type classification." />}
        {rows.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Kind</th><th>Pattern</th><th>Vendor</th><th>Device type</th><th>Confidence</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {rows.map((f) => (
                <tr key={f.id}>
                  <td><span className="badge badge-lldp">{f.kind}</span></td>
                  <td className="mono">{f.pattern}</td>
                  <td>{f.vendor || '—'}</td>
                  <td>{f.device_type || '—'}</td>
                  <td>{f.confidence}%</td>
                  <td>{f.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(f)}>{f.enabled ? 'Disable' : 'Enable'}</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(f.id)}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
