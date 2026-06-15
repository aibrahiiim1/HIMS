import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, AlertTriangle, KeyRound } from 'lucide-react'
import { api, type Credential } from '../api'

// CctvCollect is the per-device camera/NVR/DVR collection control. The operator
// picks which web credentials HIMS should TRY (each in turn); the first that
// authenticates is bound. Only ONVIF / HTTP-Basic credentials can authenticate a
// camera/NVR, so only those are offered. Because each failed attempt counts
// toward a Hikvision IP lockout, selecting more than three credentials of the
// SAME kind raises a warning popup before the attempt runs.
type CollectResp = { collected: boolean; reason?: string; detail?: string; category?: string; credential_used?: string }

const WEB_KINDS = ['onvif', 'http_basic']
const SAME_KIND_WARN = 3

export function CctvCollect({ deviceId, boundCredId, generalCredId, compact, label }: {
  deviceId: string
  boundCredId?: string | null // preferred CCTV (ONVIF/ISAPI) credential
  generalCredId?: string | null // general/SNMP bound credential (for the dual-credential display)
  compact?: boolean
  label?: string
}) {
  const qc = useQueryClient()
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const webCreds = useMemo(() => (creds.data ?? []).filter((c) => WEB_KINDS.includes(c.kind)), [creds.data])
  const credName = (id?: string | null) => (id ? (creds.data ?? []).find((c) => c.id === id) : undefined)
  const cctvCred = credName(boundCredId)
  const genCred = credName(generalCredId)

  // Default selection = the credential bound to the device (if it's a web cred).
  const [sel, setSel] = useState<Set<string> | null>(null)
  const selected = useMemo(
    () => sel ?? new Set(boundCredId && webCreds.some((c) => c.id === boundCredId) ? [boundCredId] : []),
    [sel, boundCredId, webCreds],
  )
  const [warn, setWarn] = useState<{ kind: string; count: number }[] | null>(null)

  const collect = useMutation({
    mutationFn: (ids: string[]) => api.post<CollectResp>(`/devices/${deviceId}/collect-cctv`, { credential_ids: ids }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['nvr', deviceId] })
      qc.invalidateQueries({ queryKey: ['camera', deviceId] })
      qc.invalidateQueries({ queryKey: ['devices', 'all'] })
    },
  })

  const toggle = (id: string) => {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSel(next)
  }

  // Kinds with more than the same-kind threshold selected (drives the warning).
  const overLimit = useMemo(() => {
    const byKind: Record<string, number> = {}
    for (const id of selected) {
      const c = webCreds.find((x) => x.id === id)
      if (c) byKind[c.kind] = (byKind[c.kind] || 0) + 1
    }
    return Object.entries(byKind).filter(([, n]) => n > SAME_KIND_WARN).map(([kind, count]) => ({ kind, count }))
  }, [selected, webCreds])

  const run = () => {
    if (selected.size === 0 || collect.isPending) return
    if (overLimit.length > 0) { setWarn(overLimit); return }
    collect.mutate([...selected])
  }
  const confirmRun = () => { setWarn(null); collect.mutate([...selected]) }

  return (
    <div>
      {!compact && (genCred || cctvCred) && (
        <div className="row" style={{ flexWrap: 'wrap', gap: 16, fontSize: 12.5, marginBottom: 10 }}>
          <span><KeyRound size={12} /> <strong>CCTV / ISAPI credential:</strong> {cctvCred ? `${cctvCred.name} · ${cctvCred.kind}` : <span className="muted">none yet — pick one below</span>}</span>
          <span className="muted"><KeyRound size={12} /> General / SNMP bound: {genCred ? `${genCred.name} · ${genCred.kind}` : '—'}</span>
        </div>
      )}
      {!compact && (
        <p className="muted" style={{ fontSize: 13, marginBottom: 10 }}>
          Select the web credential(s) to try. HIMS tries each in turn over ONVIF/ISAPI and binds the first that authenticates — the ONVIF integration user is separate from the device web login. This CCTV credential is kept separate from the SNMP credential, so SNMP discovery never overwrites it.
        </p>
      )}

      {webCreds.length === 0 ? (
        <div className="muted" style={{ fontSize: 13 }}>No ONVIF / HTTP-Basic credentials exist yet — add one on the Credentials page, then collect.</div>
      ) : (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 10 }}>
          {webCreds.map((c) => {
            const on = selected.has(c.id)
            return (
              <label key={c.id} className="chip" style={{ cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 6, borderColor: on ? 'var(--accent, #3b82f6)' : undefined, background: on ? 'var(--accent-soft, rgba(59,130,246,.12))' : undefined }}>
                <input type="checkbox" checked={on} onChange={() => toggle(c.id)} />
                <KeyRound size={12} /> {c.name}
                <span className="muted" style={{ fontSize: 11 }}>· {c.kind}{c.id === boundCredId ? ' · bound' : ''}</span>
              </label>
            )
          })}
        </div>
      )}

      <div className="row" style={{ gap: 10, alignItems: 'center' }}>
        <button className={`btn btn-primary ${compact ? 'btn-sm' : ''}`} disabled={collect.isPending || selected.size === 0} onClick={run}>
          <RefreshCw size={14} className={collect.isPending ? 'spin' : ''} /> {collect.isPending ? 'Collecting…' : (label ?? 'Collect')}
        </button>
        {selected.size > 0 && <span className="muted" style={{ fontSize: 12 }}>🖐 {selected.size} manually-selected credential{selected.size > 1 ? 's' : ''} — this one-off collect overrides any subnet-scoped credentials</span>}
        {overLimit.length > 0 && <span style={{ fontSize: 12, color: 'var(--warn, #d97706)' }}><AlertTriangle size={12} /> {overLimit.map((o) => `${o.count} ${o.kind}`).join(', ')} — lockout risk</span>}
      </div>

      {collect.isError && <div className="enc-banner crit" style={{ marginTop: 12 }}>{(collect.error as Error).message}</div>}
      {collect.data && <CollectResult data={collect.data} />}

      {warn && (
        <div className="modal-backdrop" style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }} onClick={() => setWarn(null)}>
          <div className="modal-panel" style={{ width: 460, maxWidth: '92%', background: 'var(--surface)', borderRadius: 10, padding: 20, boxShadow: '0 8px 40px rgba(0,0,0,.4)' }} onClick={(e) => e.stopPropagation()}>
            <div className="row" style={{ gap: 10, alignItems: 'center', marginBottom: 10 }}>
              <AlertTriangle size={20} style={{ color: 'var(--warn, #d97706)' }} />
              <strong style={{ fontSize: 15 }}>Many credentials of the same type</strong>
            </div>
            <p style={{ fontSize: 13, lineHeight: 1.5 }}>
              You selected {warn.map((o) => `${o.count} ${o.kind}`).join(' and ')} credential(s). Trying more than {SAME_KIND_WARN} credentials of the <strong>same type</strong> against a camera/NVR can trip a <strong>Hikvision IP lockout</strong> after a few failed logins — the device may then reject even the correct credential for a while.
            </p>
            <p className="muted" style={{ fontSize: 12.5, marginBottom: 16 }}>Prefer selecting just the credential(s) you believe are the device's web login.</p>
            <div className="row" style={{ justifyContent: 'flex-end', gap: 8 }}>
              <button className="btn" onClick={() => setWarn(null)}>Cancel</button>
              <button className="btn btn-primary" onClick={confirmRun}>Try anyway ({selected.size})</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function CollectResult({ data }: { data: CollectResp }) {
  const lockout = (data.detail || '').toLowerCase().includes('lockout') || data.reason === 'auth_failed'
  const cls = data.collected ? 'ok' : lockout ? 'warn' : 'crit'
  return (
    <div className={`enc-banner ${cls}`} style={{ marginTop: 12 }}>
      {data.collected
        ? `Collected${data.category ? ` (${data.category})` : ''}${data.credential_used ? ` via ${data.credential_used}` : ''}: ${data.detail || ''}`
        : `${data.reason || 'failed'} — ${data.detail || ''}`}
    </div>
  )
}
