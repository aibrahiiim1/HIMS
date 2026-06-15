import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Credential, type MonitoringCheck } from '../api'

const btn: React.CSSProperties = {
  padding: '6px 12px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 13, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '6px 8px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13,
}

// DeviceOps is a reusable per-device admin panel: register a monitoring check
// and bind a credential. Both reuse existing endpoints (POST /monitoring/checks,
// PUT /devices/{id}/credential, GET /credentials).
export function DeviceOps({ deviceId }: { deviceId: string }) {
  const qc = useQueryClient()
  const [kind, setKind] = useState('tcp')
  const [port, setPort] = useState('')
  const [credId, setCredId] = useState('')

  const checks = useQuery({ queryKey: ['dev-checks', deviceId], queryFn: () => api.get<MonitoringCheck[]>(`/devices/${deviceId}/monitoring/checks`) })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })

  const addCheck = useMutation({
    mutationFn: () => api.post('/monitoring/checks', {
      device_id: deviceId, kind, target_port: port ? Number(port) : null,
    }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['dev-checks', deviceId] }),
  })
  const bind = useMutation({
    mutationFn: () => api.put(`/devices/${deviceId}/credential`, { credential_id: credId || null }),
  })

  return (
    <div className="card">
      <h2>Operations</h2>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 10 }}>
        <span className="muted" style={{ width: 110 }}>Add monitor:</span>
        <select style={input} value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="tcp">tcp</option><option value="snmp">snmp</option>
        </select>
        <input style={{ ...input, width: 90 }} placeholder="port" value={port} onChange={(e) => setPort(e.target.value)} />
        <button style={btn} disabled={addCheck.isPending} onClick={() => addCheck.mutate()}>Add check</button>
        {addCheck.error && <span className="error-msg">{(addCheck.error as Error).message}</span>}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        <span className="muted" style={{ width: 110 }}>Bind credential:</span>
        <select style={input} value={credId} onChange={(e) => setCredId(e.target.value)}>
          <option value="">(none)</option>
          {(creds.data ?? []).map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind})</option>)}
        </select>
        <button style={btn} disabled={bind.isPending} onClick={() => bind.mutate()}>
          {bind.isPending ? 'Binding…' : 'Bind'}
        </button>
        {bind.isSuccess && <span className="muted">bound ✓</span>}
      </div>
      {checks.data && checks.data.length > 0 && (() => {
        const all = checks.data!
        const extras = all.filter((c) => c.role === 'supplemental')
        const extrasNotOk = extras.filter((c) => c.last_status === 'down' || c.last_status === 'warning')
        return (
          <>
            {extras.length > 0 && (
              <div className="muted" style={{ marginTop: 10, fontSize: 12 }}>
                This device has <strong>{all.length} checks</strong> ({all.length - extras.length} reachability + {extras.length} extra).
                {extrasNotOk.length > 0 && (
                  <span style={{ color: 'var(--warn)' }}> ⚠ {extrasNotOk.length} extra check{extrasNotOk.length > 1 ? 's are' : ' is'} not OK — this marks the device <strong>Degraded</strong> (needs attention) and lowers its health, but does <strong>not</strong> mark it offline or change the offline count.</span>
                )}
              </div>
            )}
            <table style={{ marginTop: 10 }}>
              <thead><tr><th>Check</th><th>Port</th><th>Role</th><th>Status</th><th>Interval</th></tr></thead>
              <tbody>
                {all.map((c) => (
                  <tr key={c.id}>
                    <td style={{ textTransform: 'uppercase' }}>{c.kind}</td>
                    <td>{c.target_port ?? '—'}</td>
                    <td>
                      {c.role === 'supplemental'
                        ? <span className="badge badge-unknown" title="Extra check — a failure marks the device Degraded (needs attention) and lowers its health, but never marks it offline or changes the offline count">Extra</span>
                        : <span className="badge badge-up" title="Reachability check — drives the device's online/offline status">Reachability</span>}
                    </td>
                    <td><span className={`badge badge-${c.last_status === 'up' ? 'up' : c.last_status === 'down' ? 'down' : c.last_status === 'warning' ? 'warning' : 'unknown'}`}>{c.last_status}</span></td>
                    <td>{c.interval_seconds}s</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )
      })()}
    </div>
  )
}
