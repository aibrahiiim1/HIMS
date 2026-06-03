import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'

type Settings = Record<string, number>

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const ghost: React.CSSProperties = {
  padding: '4px 12px', background: 'transparent', color: '#90caf9',
  border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 13,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: 120,
}

// Preset chips for the SNMP timeout (the one that most affects scan speed).
const SNMP_PRESETS = [1000, 3000, 10000]

function Row({ label, hint, children }: { label: string; hint: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 0', borderBottom: '1px solid #2a2a2a' }}>
      <div style={{ width: 220 }}>
        <div style={{ fontWeight: 600 }}>{label}</div>
        <div className="muted" style={{ fontSize: 12 }}>{hint}</div>
      </div>
      {children}
    </div>
  )
}

export function Settings() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['settings'], queryFn: () => api.get<Settings>('/settings') })
  const [form, setForm] = useState<Settings>({})

  useEffect(() => { if (q.data) setForm(q.data) }, [q.data])

  const save = useMutation({
    mutationFn: () => api.put<Settings>('/settings', form),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }),
  })

  const set = (k: string, v: number) => setForm((f) => ({ ...f, [k]: v }))
  const num = (k: string) => form[k] ?? 0

  return (
    <div>
      <div className="card">
        <h2>Settings</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Timeouts &amp; concurrency used by discovery and collection. Lower timeouts make a
          subnet scan finish faster but can miss slow devices; raise them on congested or
          high-latency networks.
        </p>

        <h3 style={{ marginTop: 8 }}>Discovery scan</h3>
        <p className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
          A subnet scan probes each host over <b>SNMP</b> and a few <b>TCP</b> ports — these two
          timeouts (× concurrency) set how long a scan takes. <b>SSH is not used by discovery</b>,
          so it has no scan setting.
        </p>

        <Row label="SNMP timeout" hint="per SNMP attempt during a scan">
          <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            {SNMP_PRESETS.map((ms) => (
              <button key={ms} style={{ ...ghost, ...(num('snmp_timeout_ms') === ms ? { background: '#1565c0', color: '#fff', borderColor: '#1565c0' } : {}) }} onClick={() => set('snmp_timeout_ms', ms)}>
                {ms / 1000}s
              </button>
            ))}
            <input style={input} type="number" value={num('snmp_timeout_ms')} onChange={(e) => set('snmp_timeout_ms', Number(e.target.value))} />
            <span className="muted" style={{ fontSize: 12 }}>ms (200–30000)</span>
          </div>
        </Row>

        <Row label="TCP connect timeout" hint="per port during the scan + aliveness check">
          <input style={input} type="number" value={num('tcp_timeout_ms')} onChange={(e) => set('tcp_timeout_ms', Number(e.target.value))} />
          <span className="muted" style={{ fontSize: 12 }}>ms (100–10000)</span>
        </Row>

        <Row label="Scan concurrency" hint="hosts probed in parallel (default for new scans)">
          <input style={input} type="number" value={num('scan_concurrency')} onChange={(e) => set('scan_concurrency', Number(e.target.value))} />
          <span className="muted" style={{ fontSize: 12 }}>1–64</span>
        </Row>

        <h3 style={{ marginTop: 18 }}>Collection (controller imports)</h3>
        <p className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
          Used by the controller/host imports (not the subnet scan): <b>HTTP</b> covers
          Redfish/iDRAC, UniFi/Omada/Ruckus/Extreme, ONVIF and CUCM; <b>WinRM</b> covers Hyper-V.
        </p>

        <Row label="HTTP timeout" hint="Redfish / vendor REST / ONVIF / CUCM">
          <input style={input} type="number" value={num('http_timeout_ms')} onChange={(e) => set('http_timeout_ms', Number(e.target.value))} />
          <span className="muted" style={{ fontSize: 12 }}>ms (1000–120000)</span>
        </Row>

        <Row label="WinRM timeout" hint="Hyper-V (Windows) collection">
          <input style={input} type="number" value={num('winrm_timeout_ms')} onChange={(e) => set('winrm_timeout_ms', Number(e.target.value))} />
          <span className="muted" style={{ fontSize: 12 }}>ms (5000–300000)</span>
        </Row>

        <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
          <button style={btn} disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'Saving…' : 'Save settings'}
          </button>
          {save.isSuccess && <span className="badge badge-up">saved</span>}
          {save.error && <span className="error-msg">{(save.error as Error).message}</span>}
          {q.data && <button style={ghost} onClick={() => setForm(q.data)}>Reset</button>}
        </div>
      </div>
    </div>
  )
}
