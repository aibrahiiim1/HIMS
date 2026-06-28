import { useMemo, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { ShieldOff, Pencil, RefreshCw, KeyRound, Boxes } from 'lucide-react'
import { api, type Device, MGMT_BADGE } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, usePaged, Pager, colorFor } from '../components/ui'
import { ReachabilityBadge, ManagementBadge } from '../components/StatusBadges'
import { EditDevice } from '../components/EditDevice'
import { ExportDevicesButton } from '../components/ExportDevicesButton'

// Unmanaged = HIMS SEES the device (it's in inventory, often Online) but has NO
// proven authenticated access/collection. Strict proven-only management model —
// open ports never count. Distinct from Missing Classification (identity gap).
const MGMT_STATES = ['unmanaged', 'credential_failed', 'not_authorized', 'web_authenticated', 'needs_credential', 'needs_agent', 'agent_offline', 'collection_failed', 'partially_managed'] as const

// next-action guidance per management state. The remediation is class-specific — only a
// TRUE credential_failed (every applicable credential cleanly rejected) says "fix the
// rejected credential"; a host where a credential authenticated never does.
const ACTION: Record<string, string> = {
  unmanaged: 'Bind & prove a credential, or assign an agent',
  needs_credential: 'Bind a credential for this device class, then test',
  credential_failed: 'Credential was rejected by the host (wrong username/password). Update or replace the credential.',
  not_authorized: 'Credential authenticated but is NOT authorized on this host. Check local policy, UAC remote restrictions, WinRM/DCOM permissions or group membership, or use a credential authorized on this host — not a wrong password.',
  web_authenticated: 'Web/identity credential works. Deep OS management is not available yet — add a Windows/Linux/SNMP management credential if deep inventory is required.',
  collection_failed: 'Host/listener reachable issue or transient — credential not the cause. Check firewall/listener/power, then re-test.',
  needs_agent: 'Credential authenticated but needs an agent/deep collector (legacy WSMan / WMI-DCOM). Install/assign a Relay Agent to the site.',
  agent_offline: "Bring the site's Relay Agent back online",
  partially_managed: 'Some methods work; add the missing one for full coverage',
}

// More-specific remediation keyed on the failure sub-reason (management_reason), used when
// the management state alone is too generic. A broken host WMI repository, for example, is a
// host-side repair — not the firewall/credential fix the generic collection_failed text implies.
const REASON_ACTION: Record<string, string> = {
  wmi_namespace_broken: 'Host WMI repository (root\\cimv2) is broken or unavailable — a valid credential authenticated but cannot collect, and no credential was rejected. Repair WMI ON THE HOST (winmgmt /salvagerepository, then /resetrepository if needed), or the OS is too old for supported remoting. This is a host-side fix.',
  credential_or_wmi_access: 'A CREDENTIAL issue, not host WMI breakage: one credential was rejected (wrong username/password for this host) while another authenticated but lacked WMI rights. Verify the local AND domain admin credentials match this host, and that the authenticating account has WMI/DCOM rights — before assuming the host WMI is broken.',
  wmi_collection_failed: 'The agent reached the host but WMI/DCOM collection failed (DCOM/RPC blocked or WMI access). Check the host firewall (RPC dynamic ports), DCOM, and WMI permissions — or enable WinRM (Enable-PSRemoting).',
  transport_unreachable: 'The agent could not reach WinRM/RPC on the host (port closed/filtered or listener disabled). Check the host firewall/listener — NOT a credential problem.',
  winrm_access_denied: 'A valid credential AUTHENTICATED over WinRM but the host DENIED the session (UAC remote-token filtering, the account is not a local admin, or WinRM RootSDDL). This is NOT a wrong password. For a non-domain LOCAL admin set LocalAccountTokenFilterPolicy=1 (HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Policies\\System), or use a domain/host-authorized admin account.',
  wmi_access_denied: 'A valid credential was reached over WMI/DCOM (port 135) but the host DENIED access (namespace/DCOM permissions). This is NOT a wrong password. Grant the account remote DCOM + WMI (root\\cimv2) rights, and for a non-domain LOCAL admin set LocalAccountTokenFilterPolicy=1. If WinRM (5985) is closed, enable it (Enable-PSRemoting) so the agent can use the full-token native path.',
  winrm_and_wmi_denied: 'A valid credential was reached but BOTH transports denied access — WinRM session denied AND WMI/DCOM access denied. This is NOT a wrong password. Grant the account remote-logon + DCOM/WMI rights and set LocalAccountTokenFilterPolicy=1 (non-domain local admin), or use a domain/host-authorized admin.',
}

// actionFor picks the most specific honest next-action: the sub-reason text when present,
// otherwise the per-state default.
function actionFor(d: Device): string {
  if (d.management_reason && REASON_ACTION[d.management_reason]) return REASON_ACTION[d.management_reason]
  return ACTION[d.management ?? ''] ?? 'Bind & prove a credential'
}

export function UnmanagedDevices() {
  const [sp, setSp] = useSearchParams()
  const filter = sp.get('management') ?? ''
  const [editDev, setEditDev] = useState<Device | null>(null)
  const [q, setQ] = useState('')

  // Server returns every non-managed device (proven-only) for management=not_managed.
  const { data, isLoading } = useQuery({
    queryKey: ['devices', 'unmanaged'],
    queryFn: () => api.get<Device[]>('/devices?management=not_managed'),
  })
  const rescan = useMutation({
    mutationFn: (ip: string) => api.post('/discovery/scan', { mode: 'targets', targets: ip }),
  })

  const counts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const d of data ?? []) c[d.management ?? 'unmanaged'] = (c[d.management ?? 'unmanaged'] ?? 0) + 1
    return c
  }, [data])

  const rows = useMemo(() => {
    let r = data ?? []
    if (filter) r = r.filter((d) => filter === 'online_unmanaged' ? d.reachability === 'online' : d.management === filter)
    const t = q.trim().toLowerCase()
    if (t) r = r.filter((d) => d.name.toLowerCase().includes(t) || (d.primary_ip ?? '').includes(t))
    return r
  }, [data, filter, q])
  const paged = usePaged(rows, { pageSize: 15 })
  const setFilter = (v: string) => { const n = new URLSearchParams(sp); if (v) n.set('management', v); else n.delete('management'); setSp(n, { replace: true }) }

  return (
    <div>
      <PageHeader title="Unmanaged Devices" subtitle="Devices HIMS can see but cannot manage — no proven authenticated access. Online does NOT mean Managed; open ports never count." icon={ShieldOff}
        actions={<ExportDevicesButton devices={rows} filename="unmanaged-devices" />} />
      <div className="kpi-grid">
        <Kpi label="Unmanaged" value={(data ?? []).length} icon={ShieldOff} tone={(data ?? []).length > 0 ? 'warn' : 'ok'} sub="no proven access" />
        <Kpi label="Credential failed" value={counts['credential_failed'] ?? 0} icon={KeyRound} tone={(counts['credential_failed'] ?? 0) > 0 ? 'crit' : 'default'} />
        <Kpi label="Needs credential" value={counts['needs_credential'] ?? 0} icon={KeyRound} tone="default" />
        <Kpi label="Needs / offline agent" value={(counts['needs_agent'] ?? 0) + (counts['agent_offline'] ?? 0)} icon={Boxes} tone="default" />
      </div>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', margin: '0 0 12px' }}>
        <button className={'seg-chip' + (filter === '' ? ' active' : '')} onClick={() => setFilter('')}>All</button>
        {MGMT_STATES.map((s) => (
          <button key={s} className={'seg-chip' + (filter === s ? ' active' : '')} onClick={() => setFilter(s)}>
            {MGMT_BADGE[s]?.label ?? s} <span className="seg-count">{counts[s] ?? 0}</span>
          </button>
        ))}
        <button className={'seg-chip' + (filter === 'online_unmanaged' ? ' active' : '')} onClick={() => setFilter('online_unmanaged')}>Online but unmanaged</button>
      </div>

      <Panel title="Unmanaged" subtitle="Strict proven-only management. Fix access here; classification problems live under Missing Classification." pad={false}>
        <div style={{ padding: '8px 10px' }}>
          <input placeholder="Filter by name / IP…" value={q} onChange={(e) => { setQ(e.target.value); paged.setPage(0) }} style={{ padding: '6px 10px', fontSize: 13, width: 300, maxWidth: '100%' }} />
        </div>
        {isLoading && <div className="loading">Loading…</div>}
        {data && rows.length === 0 && <EmptyState icon={ShieldOff} title="Nothing unmanaged here" message="Every device matching this filter has a proven management method." />}
        {rows.length > 0 && (
          <>
          <table className="data-table">
            <thead><tr>
              <th>Device</th><th>IP</th><th>Category</th><th>Vendor</th><th>Reachability</th><th>Management</th><th>Required action</th><th></th>
            </tr></thead>
            <tbody>
              {paged.slice.map((d) => (
                <tr key={d.id}>
                  <td><div className="dev-cell"><span className="dev-avatar" style={{ background: colorFor(d.category) }}>{(d.name || '?').charAt(0).toUpperCase()}</span>
                    <div className="dev-meta"><Link className="cell-name" to={`/devices/${d.id}`}>{d.name}</Link>{d.hostname && <small>{d.hostname}</small>}</div></div></td>
                  <td className="mono">{d.primary_ip ?? '—'}</td>
                  <td style={{ textTransform: 'capitalize' }}>{(d.category || 'unknown').replace(/_/g, ' ')}</td>
                  <td>{d.vendor || '—'}</td>
                  <td><ReachabilityBadge value={d.reachability} /></td>
                  <td><ManagementBadge value={d.management} managedBy={d.managed_by} reason={d.management_reason} /></td>
                  <td className="muted" style={{ fontSize: 11 }}>{actionFor(d)}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <Link className="btn btn-ghost btn-xs" to={`/devices/${d.id}`} title="Open device — bind credential / test / repair">Open</Link>{' '}
                    <button className="btn btn-ghost btn-xs" onClick={() => setEditDev(d)} title="Edit device"><Pencil size={12} /></button>{' '}
                    <button className="btn btn-ghost btn-xs" disabled={!d.primary_ip || rescan.isPending} onClick={() => d.primary_ip && rescan.mutate(d.primary_ip)} title="Re-scan this device"><RefreshCw size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager page={paged.page} pages={paged.pages} total={paged.total} pageSize={paged.pageSize} onPage={paged.setPage} />
          </>
        )}
      </Panel>
      {editDev && <EditDevice device={editDev} onClose={() => setEditDev(null)} />}
    </div>
  )
}
