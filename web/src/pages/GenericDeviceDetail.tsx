import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  HelpCircle, Fingerprint, KeyRound, Radar, ShieldCheck, AlertTriangle,
  ClipboardList, History, Network, ExternalLink,
} from 'lucide-react'
import {
  api, type Device, type AuthMe, type Credential, type CredTestResponse,
  type OSInventoryBundle, type DataQualityReport, type AuditEntry, type WorkOrder,
} from '../api'
import { Panel, EmptyState, timeAgo } from '../components/ui'
import { DeviceHeader } from '../components/DeviceHeader'
import { ClassificationCard } from '../components/ClassificationCard'
import { DeepOSInventory } from '../components/DeepOSInventory'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'

// GenericDeviceDetail is the operator-useful fallback template for devices with
// no type-specific page — primarily "unknown" (discovered/pingable but not yet
// classified) plus any category without a dedicated view. It deliberately shows
// NO switch-specific data (ports / VLANs / MAC tables / neighbors); instead it
// gives an operator everything needed to identify, classify and enrich the
// device: identity, classification + evidence (with Reclassify), quick actions
// (re-scan, credential testing), credential binding, deep OS inventory if it is
// a Windows/Linux host, data-quality warnings, linked work orders, and recent
// activity.
export function GenericDeviceDetail() {
  const { id: routeId } = useParams<{ id: string }>()
  const id = routeId ?? ''
  return (
    <div>
      <DeviceHeader deviceId={id} icon={HelpCircle} />
      <IdentityCard deviceId={id} />
      <QuickActions deviceId={id} />
      <div style={{ marginBottom: 16 }}><ClassificationCard deviceId={id} /></div>
      <DataQualityCard deviceId={id} />
      {/* No alwaysShow: the OS panel surfaces only when the device actually is a
          Windows/Linux host or already has inventory. */}
      <DeepOSInventory deviceId={id} />
      <DeviceCredentialHealth deviceId={id} />
      <DeviceOps deviceId={id} />
      <WorkOrdersCard deviceId={id} />
      <ActivityCard deviceId={id} />
    </div>
  )
}

const val = (v?: string | null) => (v ? <>{v}</> : <span className="muted">Not collected</span>)

// IdentityCard surfaces the raw identity fields an operator scans first. MAC is
// not a device-row column, so it's taken best-effort from the first collected
// NIC (deep OS inventory) when present.
function IdentityCard({ deviceId }: { deviceId: string }) {
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const osinv = useQuery({ queryKey: ['os-inventory', deviceId], queryFn: () => api.get<OSInventoryBundle>(`/devices/${deviceId}/os-inventory`) })
  const d = (devices.data ?? []).find((x) => x.id === deviceId)
  const mac = (osinv.data?.nics ?? []).map((n) => n.mac).find((m) => !!m) ?? null

  const rows: [string, React.ReactNode][] = [
    ['IP address', d?.primary_ip ? <span className="mono">{d.primary_ip}</span> : <span className="muted">no IP</span>],
    ['Hostname', val(d?.hostname)],
    ['MAC', mac ? <span className="mono">{mac}</span> : <span className="muted">Not collected</span>],
    ['Vendor', val(d?.vendor)],
    ['Model', val(d?.model)],
    ['Serial', val(d?.serial)],
    ['OS', val(d?.os_version)],
    ['Category', <span className="badge">{(d?.category ?? 'unknown').replace(/_/g, ' ')}</span>],
  ]
  return (
    <Panel title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><Fingerprint size={15} /> Identity</span>}>
      <dl className="kv" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: '8px 18px', margin: 0 }}>
        {rows.map(([k, v]) => (
          <div key={k}><dt className="muted" style={{ fontSize: 11 }}>{k}</dt><dd style={{ margin: 0 }}>{v}</dd></div>
        ))}
      </dl>
    </Panel>
  )
}

// QuickActions gives operators the enrichment shortcuts a freshly-discovered
// device needs: re-run discovery against its IP, and test every stored
// credential against it (which protocol/credential actually authenticates).
// Write actions are gated to admins / devices.write; everything else is
// read-only links. Reclassify lives in the Classification card; Collect OS lives
// in the Deep OS Inventory card.
function QuickActions({ deviceId }: { deviceId: string }) {
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me') })
  const devices = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  const creds = useQuery({ queryKey: ['credentials'], queryFn: () => api.get<Credential[]>('/credentials') })
  const d = (devices.data ?? []).find((x) => x.id === deviceId)
  const ip = d?.primary_ip ?? ''
  const canWrite = !!(me.data?.admin || me.data?.permissions?.includes('devices.write'))

  const rescan = useMutation({
    mutationFn: () => api.post<{ id: string }>('/discovery/scan', { mode: 'targets', targets: ip }),
  })
  const testCreds = useMutation({
    mutationFn: () => api.post<CredTestResponse>('/credentials/test', {
      credential_ids: (creds.data ?? []).map((c) => c.id),
      device_ids: [deviceId],
    }),
  })

  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><ShieldCheck size={15} /> Operator actions</span>}
      actions={
        <span style={{ display: 'inline-flex', gap: 6 }}>
          <Link className="btn btn-xs btn-ghost" to="/credentials"><KeyRound size={13} /> Credentials</Link>
          <Link className="btn btn-xs btn-ghost" to="/data-quality"><AlertTriangle size={13} /> Data Quality</Link>
        </span>
      }
    >
      {!canWrite && <p className="muted" style={{ fontSize: 12 }}>Read-only — you need the <code>devices.write</code> permission to run discovery or test credentials.</p>}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
        <button className="btn btn-sm" disabled={!canWrite || !ip || rescan.isPending} title={ip ? `Re-run discovery against ${ip}` : 'Device has no IP to scan'} onClick={() => rescan.mutate()}>
          <Radar size={13} /> {rescan.isPending ? 'Starting…' : 'Run discovery (re-scan IP)'}
        </button>
        <button className="btn btn-sm" disabled={!canWrite || (creds.data ?? []).length === 0 || testCreds.isPending} title="Test every stored credential against this device" onClick={() => testCreds.mutate()}>
          <KeyRound size={13} /> {testCreds.isPending ? 'Testing…' : `Test credentials (${(creds.data ?? []).length})`}
        </button>
      </div>

      {rescan.isSuccess && (
        <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Scan started. <Link to="/discovery">View progress in Discovery →</Link></p>
      )}
      {rescan.error && <p className="error-msg" style={{ marginTop: 8 }}>{(rescan.error as Error).message}</p>}

      {testCreds.data && (
        testCreds.data.results.length === 0
          ? <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>No credential pairs were testable for this device.</p>
          : (
            <table className="data-table" style={{ marginTop: 10 }}>
              <thead><tr><th>Credential</th><th>Kind</th><th>Protocol</th><th>Result</th><th>Detail</th></tr></thead>
              <tbody>
                {testCreds.data.results.map((r, i) => (
                  <tr key={i}>
                    <td className="cell-name">{r.credential_name}</td>
                    <td>{r.kind}</td>
                    <td>{r.protocol || '—'}</td>
                    <td><span className={`badge badge-${r.success ? 'up' : r.category === 'auth_failed' ? 'down' : 'unknown'}`}>{r.success ? 'success' : r.category}</span></td>
                    <td className="muted" style={{ fontSize: 12 }}>{r.detail}{r.latency_ms ? ` · ${r.latency_ms}ms` : ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
      )}
      {testCreds.error && <p className="error-msg" style={{ marginTop: 8 }}>{(testCreds.error as Error).message}</p>}
    </Panel>
  )
}

// DataQualityCard shows only the data-quality issues this device is flagged in,
// pulled from the global report (devices are listed per issue).
function DataQualityCard({ deviceId }: { deviceId: string }) {
  const dq = useQuery({ queryKey: ['data-quality'], queryFn: () => api.get<DataQualityReport>('/data-quality') })
  const mine = (dq.data?.issues ?? [])
    .map((iss) => ({ iss, dev: iss.devices.find((d) => d.id === deviceId) }))
    .filter((x) => !!x.dev)

  if (dq.isLoading || mine.length === 0) {
    // Hide entirely when clean — no noise for a healthy device.
    return null
  }
  return (
    <Panel title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><AlertTriangle size={15} /> Data quality</span>} subtitle={`${mine.length} issue(s)`}>
      <table className="data-table">
        <thead><tr><th>Issue</th><th>Severity</th><th>Detail</th></tr></thead>
        <tbody>
          {mine.map(({ iss, dev }) => (
            <tr key={iss.key}>
              <td className="cell-name" title={iss.description}>{iss.label}</td>
              <td><span className={`badge badge-${iss.severity === 'high' || iss.severity === 'critical' ? 'down' : iss.severity === 'medium' ? 'warning' : 'unknown'}`}>{iss.severity}</span></td>
              <td className="muted" style={{ fontSize: 12 }}>{dev?.note || iss.description}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  )
}

// WorkOrdersCard lists work orders linked to this device.
function WorkOrdersCard({ deviceId }: { deviceId: string }) {
  const q = useQuery({ queryKey: ['device-wos', deviceId], queryFn: () => api.get<WorkOrder[]>(`/devices/${deviceId}/work-orders`) })
  const wos = q.data ?? []
  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><ClipboardList size={15} /> Work orders</span>}
      subtitle={wos.length ? `${wos.length}` : undefined}
      actions={<Link className="btn btn-xs btn-ghost" to="/work-orders"><ExternalLink size={13} /> All</Link>}
    >
      {q.isLoading ? <div className="loading">Loading…</div>
        : wos.length === 0 ? <EmptyState icon={ClipboardList} title="No work orders" message="No maintenance work orders are linked to this device." />
          : (
            <table className="data-table">
              <thead><tr><th>Title</th><th>Type</th><th>Priority</th><th>Status</th><th>Opened</th></tr></thead>
              <tbody>
                {wos.map((w) => (
                  <tr key={w.id}>
                    <td className="cell-name"><Link to="/work-orders">{w.title}</Link></td>
                    <td>{w.problem_type}</td>
                    <td>{w.priority}</td>
                    <td><span className={`badge badge-${w.status === 'resolved' || w.status === 'closed' ? 'up' : w.status === 'open' ? 'down' : 'warning'}`}>{w.status}</span></td>
                    <td className="muted">{timeAgo(w.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
    </Panel>
  )
}

// ActivityCard shows recent audit entries scoped to this device. The audit
// endpoint has no entity_id filter, so we pull recent device-scoped entries and
// filter client-side by entity_id.
function ActivityCard({ deviceId }: { deviceId: string }) {
  const q = useQuery({
    queryKey: ['audit-device', deviceId],
    queryFn: () => api.get<AuditEntry[]>('/audit-log?entity_type=device&limit=500'),
  })
  const mine = (q.data ?? []).filter((a) => a.entity_id === deviceId).slice(0, 10)
  return (
    <Panel
      title={<span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><History size={15} /> Recent activity</span>}
      actions={<Link className="btn btn-xs btn-ghost" to="/audit-log"><ExternalLink size={13} /> Audit log</Link>}
    >
      {q.isLoading ? <div className="loading">Loading…</div>
        : mine.length === 0 ? <EmptyState icon={Network} title="No recent activity" message="Operator actions on this device (re-scan, reclassify, credential changes) will appear here." />
          : (
            <table className="data-table">
              <thead><tr><th>When</th><th>Action</th><th>Summary</th><th>Actor</th></tr></thead>
              <tbody>
                {mine.map((a) => (
                  <tr key={a.id}>
                    <td className="muted" title={a.at}>{timeAgo(a.at)}</td>
                    <td className="mono" style={{ fontSize: 12 }}>{a.action}</td>
                    <td>{a.summary || '—'}</td>
                    <td>{a.actor}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
    </Panel>
  )
}
