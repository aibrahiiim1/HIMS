import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { DatabaseBackup, ShieldCheck, CircleCheck, CircleX, DownloadCloud, FileUp, ClipboardList, Trash2 } from 'lucide-react'
import { api, type DRReadiness, type BackupRun } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'

const API_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'
const fmtBytes = (n: number) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`)

export function BackupRestore() {
  const qc = useQueryClient()
  const dr = useQuery({ queryKey: ['dr-readiness'], queryFn: () => api.get<DRReadiness>('/admin/dr-readiness'), refetchInterval: 60_000 })
  const runs = useQuery({ queryKey: ['backup-runs'], queryFn: () => api.get<BackupRun[]>('/admin/backup/runs') })
  const inv = () => { qc.invalidateQueries({ queryKey: ['dr-readiness'] }); qc.invalidateQueries({ queryKey: ['backup-runs'] }) }

  const [valResult, setValResult] = useState<string | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const d = dr.data

  const validate = useMutation({
    mutationFn: async (text: string) => api.postText<{ ok: boolean; error?: string; summary?: { total_rows: number; tables: { table: string; rows: number }[] } }>('/admin/backup/validate', text, 'application/json'),
    onSuccess: (r) => setValResult(r.ok ? `✓ Valid archive — ${r.summary?.tables.length} tables, ${r.summary?.total_rows} rows` : `✗ ${r.error}`),
    onError: (e) => setValResult('✗ ' + (e as Error).message),
  })
  const onFile = (f: File | undefined) => { if (!f) return; setValResult(null); f.text().then((t) => validate.mutate(t)) }

  const okCount = (d?.checklist ?? []).filter((c) => c.ok).length

  return (
    <div>
      <PageHeader title="Backup & Restore" icon={DatabaseBackup}
        subtitle="DR readiness, configuration snapshots, restore validation and backup history"
        actions={<a className="btn btn-primary btn-sm" href={`${API_BASE}/admin/backup/export`} onClick={() => setTimeout(inv, 800)}><DownloadCloud size={14} /> Download config backup</a>} />

      <div className="kpi-grid">
        <Kpi label="DR Checks Passing" value={d ? `${okCount}/${d.checklist.length}` : '—'} icon={ShieldCheck} tone={d && okCount === d.checklist.length ? 'ok' : 'warn'} />
        <Kpi label="Last Backup" value={d?.last_backup_at ? timeAgo(d.last_backup_at) : 'never'} icon={DatabaseBackup} tone={d?.recent_backup ? 'ok' : 'crit'} sub={d?.last_backup_kind || undefined} />
        <Kpi label="Encryption Key" value={d?.key_loaded ? 'loaded' : 'missing'} icon={ShieldCheck} tone={d?.key_loaded ? 'ok' : 'crit'} />
        <Kpi label="Protected Records" value={d ? `${d.device_count} dev · ${d.credential_count} cred` : '—'} icon={ClipboardList} />
      </div>

      <Panel title="Disaster Recovery Checklist" icon={ShieldCheck}>
        {dr.isLoading && <div className="loading">Loading…</div>}
        {d && (
          <table className="data-table">
            <tbody>
              {d.checklist.map((c, i) => (
                <tr key={i}>
                  <td style={{ width: 36 }}>{c.ok ? <CircleCheck size={16} style={{ color: 'var(--ok,#16a34a)' }} /> : <CircleX size={16} style={{ color: 'var(--crit)' }} />}</td>
                  <td className="cell-name">{c.item}</td>
                  <td className="muted">{c.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {d?.key_loaded && <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Key fingerprint: <span className="mono">{d.key_fingerprint}</span></p>}
      </Panel>

      <div className="grid-2">
        <Panel title="Validate a Backup" icon={FileUp} subtitle="restore pre-check">
          <p className="muted" style={{ fontSize: 13, marginBottom: 10 }}>Upload a HIMS config snapshot (.json) to confirm it is well-formed before relying on it for restore.</p>
          <input ref={fileRef} type="file" accept="application/json,.json" style={{ display: 'none' }} onChange={(e) => onFile(e.target.files?.[0])} />
          <button className="btn" disabled={validate.isPending} onClick={() => fileRef.current?.click()}><FileUp size={14} /> {validate.isPending ? 'Validating…' : 'Choose file…'}</button>
          {valResult && <div className={'enc-banner ' + (valResult.startsWith('✓') ? 'info' : 'crit')} style={{ marginTop: 10 }}>{valResult}</div>}
        </Panel>

        <Panel title="Record External Backup" icon={DatabaseBackup} subtitle="off-box pg_dump">
          <RecordExternal onDone={inv} />
        </Panel>
      </div>

      <Panel title="Backup History" icon={ClipboardList} subtitle={`${runs.data?.length ?? 0}`} pad={false}>
        {runs.data && runs.data.length === 0 && <EmptyState icon={DatabaseBackup} title="No backups yet" message="Download a config snapshot, or record an external pg_dump." />}
        {runs.data && runs.data.length > 0 && (
          <table className="data-table">
            <thead><tr><th>When</th><th>Kind</th><th>Status</th><th>Tables</th><th>Rows</th><th>Size</th><th>Detail</th></tr></thead>
            <tbody>
              {runs.data.map((r) => (
                <tr key={r.id}>
                  <td className="muted">{timeAgo(r.at)}</td>
                  <td>{r.kind === 'external_pg_dump' ? <span className="badge badge-trunk">pg_dump</span> : <span className="badge badge-info">config</span>}</td>
                  <td>{r.status === 'success' ? <span className="badge badge-up">success</span> : <span className="badge badge-down">failed</span>}</td>
                  <td>{r.tables || '—'}</td>
                  <td>{r.rows || '—'}</td>
                  <td className="mono">{fmtBytes(r.size_bytes)}</td>
                  <td className="muted">{r.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <DangerZone />
    </div>
  )
}

interface DbResetCategory { key: string; label: string; count: number }
interface DbResetSummary { categories: DbResetCategory[]; protected: string[] }

// DangerZone — initialize/reset the database by wiping selected data categories. Destructive +
// irreversible: requires checking categories AND typing ERASE. Admin-only (rbac.manage). The
// backend wipes all selected categories in one transaction and never touches users/auth/encryption.
function DangerZone() {
  const qc = useQueryClient()
  const sum = useQuery({ queryKey: ['db-reset-summary'], queryFn: () => api.get<DbResetSummary>('/admin/database/summary') })
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [confirm, setConfirm] = useState('')
  const [result, setResult] = useState<string>('')
  const reset = useMutation({
    mutationFn: () => api.post<{ reset: boolean; deleted: Record<string, number> }>('/admin/database/reset', { categories: [...sel], confirm }),
    onSuccess: (r) => {
      const lines = Object.entries(r.deleted).map(([k, n]) => `${k}: ${n} rows`).join(' · ')
      setResult(`Done — ${lines || 'nothing matched'}.`)
      setSel(new Set()); setConfirm('')
      qc.invalidateQueries() // refresh everything; the data changed fleet-wide
    },
    onError: (e: unknown) => setResult('Failed: ' + (e instanceof Error ? e.message : String(e))),
  })
  const toggle = (k: string) => setSel((s) => { const n = new Set(s); if (n.has(k)) n.delete(k); else n.add(k); return n })
  const ready = sel.size > 0 && confirm.trim().toUpperCase() === 'ERASE'

  return (
    <Panel title="Danger Zone — Initialize / Reset Database" icon={Trash2} className="panel-danger"
      subtitle="wipe selected data to empty">
      <p className="muted" style={{ fontSize: 13 }}>
        Select the data to permanently delete, then type <b>ERASE</b> to confirm. This is <b>irreversible</b> — export a backup first.
        Users, roles, sessions, the encryption key, schema migrations, app settings and site/subnet definitions are never wiped here.
      </p>
      {sum.isLoading && <div className="loading">Loading categories…</div>}
      {sum.data && (
        <div style={{ display: 'grid', gridTemplateColumns: '1fr', gap: 6, margin: '10px 0' }}>
          {sum.data.categories.map((c) => (
            <label key={c.key} style={{ display: 'flex', gap: 8, alignItems: 'flex-start', padding: '6px 8px', border: '1px solid #3a2a2a', borderRadius: 6, cursor: 'pointer' }}>
              <input type="checkbox" checked={sel.has(c.key)} onChange={() => toggle(c.key)} style={{ marginTop: 3 }} />
              <span style={{ fontSize: 13 }}>
                <b>{c.key}</b> <span className="badge badge-unknown" style={{ fontSize: 10 }}>{c.count} rows</span>
                <br /><small className="muted">{c.label}</small>
              </span>
            </label>
          ))}
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <input className="field" placeholder='Type ERASE to confirm' value={confirm} onChange={(e) => setConfirm(e.target.value)}
          style={{ width: 200, padding: '6px 10px', border: '1px solid #2a3a47', borderRadius: 6, fontSize: 13 }} />
        <button className="btn btn-sm" style={{ background: '#b91c1c', color: '#fff' }} disabled={!ready || reset.isPending}
          onClick={() => { if (window.confirm(`Permanently delete ${sel.size} categor${sel.size === 1 ? 'y' : 'ies'}? This cannot be undone.`)) reset.mutate() }}>
          <Trash2 size={14} /> {reset.isPending ? 'Wiping…' : `Delete selected (${sel.size})`}
        </button>
        {sel.size > 0 && <button className="btn btn-ghost btn-sm" onClick={() => { setSel(new Set()); setConfirm('') }}>Clear</button>}
      </div>
      {result && <div className="banner" style={{ marginTop: 10, fontSize: 13 }}>{result}</div>}
    </Panel>
  )
}

function RecordExternal({ onDone }: { onDone: () => void }) {
  const [size, setSize] = useState('')
  const [detail, setDetail] = useState('')
  const m = useMutation({
    mutationFn: () => api.post('/admin/backup/record-external', { size_bytes: Number(size) || 0, detail }),
    onSuccess: () => { setSize(''); setDetail(''); onDone() },
  })
  return (
    <>
      <p className="muted" style={{ fontSize: 13, marginBottom: 10 }}>After running <span className="mono">pg_dump</span> of the HIMS database to off-site storage, log it here so DR readiness reflects it.</p>
      <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
        <input className="field" style={{ width: 140 }} type="number" placeholder="size (bytes)" value={size} onChange={(e) => setSize(e.target.value)} />
        <input className="field" style={{ flex: 1, minWidth: 180 }} placeholder="location / note (e.g. s3://backups/…)" value={detail} onChange={(e) => setDetail(e.target.value)} />
        <button className="btn btn-primary" disabled={m.isPending} onClick={() => m.mutate()}>Record</button>
      </div>
    </>
  )
}
