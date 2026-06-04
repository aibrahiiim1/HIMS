import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, Cpu } from 'lucide-react'
import { api, type OSInventoryBundle } from '../api'

function fmtBytes(n?: number | null): string {
  if (n == null || n === 0) return 'Not collected'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}
const val = (v?: string | number | null) => (v == null || v === '' ? <span className="muted">Not collected</span> : <>{v}</>)
function fmtUptime(s?: number | null): string {
  if (!s) return 'Not collected'
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600)
  return `${d}d ${h}h`
}

// DeepOSInventory renders the authenticated deep OS inventory (WinRM/SSH) for a
// device: a summary plus disks/network/services/processes/software/roles/events.
// Absent data shows "Not collected" / "Not collected yet" — never fabricated. A
// Collect button runs an on-demand collection (needs a bound credential).
export function DeepOSInventory({ deviceId }: { deviceId: string }) {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['os-inventory', deviceId], queryFn: () => api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`) })
  const collect = useMutation({
    mutationFn: () => api.post(`/devices/${deviceId}/collect-os`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['os-inventory', deviceId] }),
  })

  const b = q.data
  const inv = b?.inventory ?? null
  const collectBtn = (
    <button className="btn btn-sm" disabled={collect.isPending} onClick={() => collect.mutate()}>
      <RefreshCw size={13} /> {collect.isPending ? 'Collecting…' : inv ? 'Re-collect' : 'Collect OS'}
    </button>
  )
  const err = collect.error ? (collect.error as Error).message : null

  return (
    <div className="card">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2 style={{ margin: 0, display: 'inline-flex', gap: 8, alignItems: 'center' }}><Cpu size={17} /> Deep OS Inventory</h2>
        <span style={{ display: 'inline-flex', gap: 10, alignItems: 'center' }}>
          {inv && <span className="muted" style={{ fontSize: 12 }}>via <strong>{inv.collection_method}</strong> · {new Date(inv.collected_at).toLocaleString()}</span>}
          {collectBtn}
        </span>
      </div>
      {err && <p className="error-msg" style={{ marginTop: 8 }}>{err}</p>}

      {q.isLoading && <div className="loading">Loading…</div>}

      {b && !inv && (
        <p className="muted" style={{ marginTop: 10 }}>
          Not collected yet. Bind a working credential (WinRM for Windows, SSH for Linux) and click
          <strong> Collect OS</strong> to gather OS, hardware, disks, network, services, processes and software.
        </p>
      )}

      {b && inv && (
        <>
          <dl className="kv" style={{ marginTop: 12 }}>
            <div><dt>OS</dt><dd>{val(inv.os_caption)}</dd></div>
            <div><dt>Version / build</dt><dd>{val(inv.os_version)}{inv.os_build ? ` (${inv.os_build})` : ''}</dd></div>
            <div><dt>Edition / arch</dt><dd>{val(inv.os_edition || inv.os_arch)}</dd></div>
            {inv.kernel && <div><dt>Kernel</dt><dd>{inv.kernel}</dd></div>}
            <div><dt>Hostname</dt><dd>{val(inv.hostname)}</dd></div>
            <div><dt>Domain / FQDN</dt><dd>{val(inv.fqdn || inv.domain || inv.workgroup)}</dd></div>
            <div><dt>Uptime</dt><dd>{fmtUptime(inv.uptime_seconds)}</dd></div>
            <div><dt>Timezone</dt><dd>{val(inv.timezone)}</dd></div>
            <div><dt>Manufacturer / model</dt><dd>{val([inv.manufacturer, inv.model].filter(Boolean).join(' ') || null)}</dd></div>
            <div><dt>Serial</dt><dd>{val(inv.serial)}</dd></div>
            <div><dt>BIOS</dt><dd>{val(inv.bios_version)}</dd></div>
            <div><dt>CPU</dt><dd>{val(inv.cpu_model)}{inv.cpu_cores ? ` · ${inv.cpu_cores} cores` : ''}{inv.cpu_sockets ? ` / ${inv.cpu_sockets} sockets` : ''}</dd></div>
            <div><dt>RAM</dt><dd>{fmtBytes(inv.ram_total_bytes)}</dd></div>
          </dl>

          {b.roles.length > 0 && (
            <div style={{ marginTop: 6 }}>
              <span className="muted">Detected roles: </span>
              {b.roles.map((r) => <span key={r.role} className="badge badge-access" style={{ marginRight: 6 }}>{r.role}</span>)}
            </div>
          )}

          {(inv.events_error_24h != null || inv.events_warning_24h != null || inv.events_critical_24h != null) && (
            <p style={{ marginTop: 10, fontSize: 13 }}>
              <strong>Event log (24h):</strong>{' '}
              <span className="badge badge-down">{inv.events_critical_24h ?? 0} critical</span>{' '}
              <span className="badge badge-warning">{inv.events_error_24h ?? 0} error</span>{' '}
              <span className="badge badge-unknown">{inv.events_warning_24h ?? 0} warning</span>
              {inv.last_critical_event ? <span className="muted"> · last critical: {inv.last_critical_event}</span> : null}
            </p>
          )}

          <Section title={`Disks / Volumes (${b.disks.length})`} empty={b.disks.length === 0}>
            <table><thead><tr><th>Name</th><th>FS</th><th>Total</th><th>Free</th><th>Model</th></tr></thead>
              <tbody>{b.disks.map((d, i) => <tr key={i}><td>{d.name}</td><td>{d.filesystem || '—'}</td><td>{fmtBytes(d.total_bytes)}</td><td>{fmtBytes(d.free_bytes)}</td><td>{d.model || '—'}</td></tr>)}</tbody>
            </table>
          </Section>

          <Section title={`Network (${b.nics.length})`} empty={b.nics.length === 0}>
            <table><thead><tr><th>Interface</th><th>MAC</th><th>IP(s)</th><th>Gateway</th><th>DNS</th><th>DHCP</th></tr></thead>
              <tbody>{b.nics.map((n, i) => <tr key={i}><td>{n.name}</td><td className="mono" style={{ fontSize: 12 }}>{n.mac || '—'}</td><td className="mono" style={{ fontSize: 12 }}>{n.ip_addresses || '—'}</td><td>{n.gateway || '—'}</td><td className="mono" style={{ fontSize: 12 }}>{n.dns_servers || '—'}</td><td>{n.dhcp_enabled ? 'yes' : 'no'}</td></tr>)}</tbody>
            </table>
          </Section>

          <Section title={`Services (${b.services.length})`} empty={b.services.length === 0}>
            <table><thead><tr><th>Name</th><th>Status</th><th>Start</th><th>Description</th></tr></thead>
              <tbody>{b.services.slice(0, 200).map((sv, i) => <tr key={i}><td>{sv.display_name || sv.name}</td><td>{sv.status || '—'}</td><td>{sv.start_type || '—'}</td><td className="muted" style={{ fontSize: 12 }}>{sv.description || ''}</td></tr>)}</tbody>
            </table>
          </Section>

          <Section title={`Top processes (${b.processes.length})`} empty={b.processes.length === 0}>
            <table><thead><tr><th>Process</th><th>PID</th><th>Memory</th><th>CPU%</th></tr></thead>
              <tbody>{b.processes.slice(0, 50).map((p) => <tr key={p.pid}><td>{p.name}</td><td>{p.pid}</td><td>{fmtBytes(p.mem_bytes)}</td><td>{p.cpu_pct ?? '—'}</td></tr>)}</tbody>
            </table>
          </Section>

          <Section title={`Installed software (${b.software.length})`} empty={b.software.length === 0}>
            <table><thead><tr><th>Name</th><th>Version</th><th>Publisher</th></tr></thead>
              <tbody>{b.software.slice(0, 300).map((sw, i) => <tr key={i}><td>{sw.name}</td><td>{sw.version || '—'}</td><td className="muted" style={{ fontSize: 12 }}>{sw.publisher || ''}</td></tr>)}</tbody>
            </table>
          </Section>
        </>
      )}
    </div>
  )
}

function Section({ title, empty, children }: { title: string; empty: boolean; children: React.ReactNode }) {
  return (
    <details style={{ marginTop: 12 }} open={!empty}>
      <summary style={{ cursor: 'pointer', fontWeight: 600 }}>{title}</summary>
      <div style={{ marginTop: 8 }}>{empty ? <span className="muted">Not collected yet.</span> : children}</div>
    </details>
  )
}
