import { useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { RefreshCw, ChevronDown, KeyRound } from 'lucide-react'
import { api, type Credential } from '../api'

// RescanSplit — a re-scan control with two options:
//   1) Re-scan                  → tries ALL eligible credentials (default behaviour)
//   2) Re-scan with credential… → pins the scan to ONE chosen stored credential
// Targets is a single IP or a comma-separated list (for a multi-select re-scan).
// The credential-scoped scan posts credential_ids:[id]; the backend's
// scanCredentialTier pins the scan to exactly that credential.
export function RescanSplit({ targets, label = 'Re-scan', size = 'xs', onMsg }: {
  targets: string
  label?: string
  size?: 'xs' | 'sm'
  onMsg?: (msg: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [pickCred, setPickCred] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)

  // Only load credentials once the operator opens the picker.
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials'), enabled: pickCred })

  const scan = useMutation({
    mutationFn: (credIds: string[]) =>
      api.post('/discovery/scan', { mode: 'targets', targets, ...(credIds.length ? { credential_ids: credIds } : {}) }),
    onSuccess: (_d, credIds) => {
      onMsg?.(credIds.length
        ? 'Re-scan launched with the selected credential — watch Discovery → Scan Jobs.'
        : 'Re-scan launched — watch Discovery → Scan Jobs.')
      setOpen(false); setPickCred(false)
    },
    onError: (e) => onMsg?.((e as Error).message),
  })

  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) { setOpen(false); setPickCred(false) } }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  const disabled = !targets || scan.isPending
  const btnCls = size === 'sm' ? 'btn btn-sm' : 'btn btn-ghost btn-xs'
  const nTargets = targets ? targets.split(',').filter(Boolean).length : 0

  return (
    <span ref={ref} style={{ position: 'relative', display: 'inline-flex' }}>
      <button className={btnCls} disabled={disabled} onClick={() => setOpen((o) => !o)} title="Re-scan options">
        <RefreshCw size={size === 'sm' ? 14 : 12} /> {label} <ChevronDown size={12} />
      </button>
      {open && (
        <div style={menu}>
          {!pickCred ? (
            <>
              <button style={item} disabled={disabled} onClick={() => scan.mutate([])}>
                <RefreshCw size={13} /> <span>Re-scan</span> <small className="muted">try all credentials</small>
              </button>
              <button style={item} onClick={() => setPickCred(true)}>
                <KeyRound size={13} /> <span>Re-scan with credential…</span> <small className="muted">pick one</small>
              </button>
            </>
          ) : (
            <div style={{ maxHeight: 280, overflowY: 'auto' }}>
              <div className="muted" style={{ fontSize: 11, padding: '4px 8px' }}>
                Scan {nTargets > 1 ? `${nTargets} targets` : targets} with:
              </div>
              {creds.isLoading && <div className="loading" style={{ padding: 8, fontSize: 12 }}>Loading credentials…</div>}
              {!creds.isLoading && (creds.data ?? []).length === 0 && (
                <div className="muted" style={{ padding: 8, fontSize: 12 }}>No stored credentials. Add one under Credentials.</div>
              )}
              {(creds.data ?? []).map((c) => (
                <button key={c.id} style={item} disabled={scan.isPending} onClick={() => scan.mutate([c.id])}>
                  <KeyRound size={13} /> <span>{c.name}</span> <small className="muted">{c.kind}{c.weak ? ' · weak' : ''}</small>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </span>
  )
}

const menu: CSSProperties = {
  position: 'absolute', top: '100%', right: 0, zIndex: 40, marginTop: 4, minWidth: 250,
  background: 'var(--surface, #fff)', border: '1px solid var(--border)', borderRadius: 8,
  boxShadow: '0 8px 24px rgba(0,0,0,0.18)', padding: 6,
}
const item: CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, width: '100%', textAlign: 'left',
  padding: '7px 9px', background: 'none', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 13,
}
