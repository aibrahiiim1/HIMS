import { useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type RelayAgentDetail, type AgentJob, type Location } from '../api'
import { PageHeader, Panel, Kpi, DefList, StatusPill, timeAgo } from '../components/ui'
import { Radar, ArrowLeft } from 'lucide-react'

const ghost: React.CSSProperties = { padding: '4px 10px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }

const CAP_LABEL: Record<string, string> = {
  winrm: 'Windows (WinRM)', wmi: 'Windows (WMI/DCOM)', ssh: 'Linux (SSH)',
  snmp: 'SNMP', onvif: 'CCTV (ONVIF)', vsphere: 'VMware (vSphere)',
}

// Relay Agent detail — full operational view of one site collector: status,
// assigned site, capabilities, heartbeat, last error, and running / recent /
// failed jobs, with test + enable/disable + delete actions.
export function AgentDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const nav = useNavigate()
  const [msg, setMsg] = useState('')

  const detail = useQuery({
    queryKey: ['relay-agent', id],
    queryFn: () => api.get<RelayAgentDetail>(`/agents/${id}`),
    refetchInterval: 10_000,
    enabled: !!id,
  })
  const jobs = useQuery({
    queryKey: ['agent-jobs', id],
    queryFn: () => api.get<AgentJob[]>(`/agents/${id}/jobs`),
    refetchInterval: 5_000,
    enabled: !!id,
  })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const refresh = () => { qc.invalidateQueries({ queryKey: ['relay-agent', id] }); qc.invalidateQueries({ queryKey: ['agent-jobs', id] }) }

  const test = useMutation({
    mutationFn: () => api.post(`/agents/${id}/test`, {}),
    onSuccess: () => { setMsg('Test job queued — the agent runs it on its next poll (~8s).'); refresh() },
    onError: (e) => setMsg((e as Error).message),
  })
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.patch(`/agents/${id}`, { enabled }),
    onSuccess: refresh,
  })
  const setSite = useMutation({
    mutationFn: (location_id: string) => api.patch(`/agents/${id}`, { location_id }),
    onSuccess: refresh,
  })
  const del = useMutation({
    mutationFn: () => api.del(`/agents/${id}`),
    onSuccess: () => nav('/agents'),
  })

  if (detail.isLoading) return <div className="loading">Loading…</div>
  if (detail.error || !detail.data) return <div className="error-msg">{(detail.error as Error)?.message ?? 'Agent not found'}</div>
  const a = detail.data.agent
  const locName = (lid?: string) => locs.data?.find((l) => l.id === lid)?.name
  const jobList = jobs.data ?? []
  const running = jobList.filter((j) => j.status === 'queued' || j.status === 'dispatched')
  const recent = jobList.filter((j) => j.status === 'done' || j.status === 'failed')
  const failed = jobList.filter((j) => j.status === 'failed')

  return (
    <div>
      <PageHeader
        title={a.name}
        subtitle="HIMS Relay Agent / Site Collector"
        icon={Radar}
        actions={
          <span style={{ display: 'inline-flex', gap: 8 }}>
            <Link to="/agents" style={{ ...ghost, textDecoration: 'none', display: 'inline-flex', alignItems: 'center', gap: 4 }}><ArrowLeft size={13} /> All agents</Link>
            <button style={ghost} disabled={test.isPending} onClick={() => test.mutate()}>Test</button>
            <button style={ghost} onClick={() => toggle.mutate(!a.enabled)}>{a.enabled ? 'Disable' : 'Enable'}</button>
            <button style={danger} onClick={() => { if (confirm(`Delete relay agent "${a.name}"? Its token stops working immediately.`)) del.mutate() }}>Delete</button>
          </span>
        }
      />
      {msg && <div className="muted" style={{ fontSize: 12, marginBottom: 10 }}>{msg}</div>}

      <div className="kpi-grid">
        <Kpi label="Status" value={a.enabled ? (a.online ? 'Online' : 'Offline') : 'Disabled'} tone={a.online ? 'ok' : a.enabled ? 'crit' : 'default'} icon={Radar} />
        <Kpi label="Running jobs" value={detail.data.running_jobs} tone={detail.data.running_jobs > 0 ? 'info' : 'default'} />
        <Kpi label="Failed jobs" value={detail.data.failed_jobs} tone={detail.data.failed_jobs > 0 ? 'warn' : 'ok'} />
        <Kpi label="Last heartbeat" value={timeAgo(a.last_heartbeat)} />
      </div>

      <Panel title="Agent" icon={Radar}>
        <DefList items={[
          { label: 'Status', value: <StatusPill status={a.online ? 'online' : a.enabled ? 'down' : 'unknown'} label={a.enabled ? (a.online ? 'Online' : 'Offline') : 'Disabled'} /> },
          { label: 'Assigned site', value: (
            <select style={{ padding: '4px 6px', fontSize: 12, borderRadius: 6, border: '1px solid #ccc' }} value={a.location_id ?? ''} onChange={(e) => setSite.mutate(e.target.value)}>
              <option value="">— unassigned —</option>
              {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </select>
          ) },
          { label: 'Hostname', value: a.hostname || '—' },
          { label: 'IP', value: a.ip || '—' },
          { label: 'OS', value: a.os || '—' },
          { label: 'Version', value: a.version || '—' },
          { label: 'Capabilities', value: a.capabilities.length ? a.capabilities.map((c) => <span key={c} className="badge badge-unknown" style={{ marginRight: 3 }}>{CAP_LABEL[c] ?? c}</span>) : '—' },
          { label: 'Last heartbeat', value: a.last_heartbeat ? `${timeAgo(a.last_heartbeat)} (${a.last_heartbeat.replace('T', ' ').slice(0, 19)})` : 'never' },
          { label: 'Last error', value: a.last_error ? <span className="error-msg">{a.last_error}</span> : <span className="muted">none</span> },
          { label: 'Site name', value: locName(a.location_id) || <span className="muted">unassigned</span> },
        ]} />
      </Panel>

      <Panel title="Running jobs" icon={Radar} subtitle={`${running.length} in flight`}>
        {running.length === 0 ? <div className="muted">No jobs in flight.</div> : <JobsTable jobs={running} />}
      </Panel>

      {failed.length > 0 && (
        <Panel title="Failed jobs" icon={Radar} subtitle={`${failed.length}`}>
          <JobsTable jobs={failed} />
        </Panel>
      )}

      <Panel title="Recent jobs" icon={Radar} subtitle={`${recent.length}`}>
        {recent.length === 0 ? <div className="muted">No completed jobs yet. Use <strong>Test</strong> or route a scan through this agent.</div> : <JobsTable jobs={recent} />}
      </Panel>
    </div>
  )
}

function JobsTable({ jobs }: { jobs: AgentJob[] }) {
  return (
    <table>
      <thead><tr><th>Kind</th><th>Protocol</th><th>Target</th><th>Status</th><th>Category</th><th>Error</th><th>Created</th></tr></thead>
      <tbody>
        {jobs.map((j) => (
          <tr key={j.id}>
            <td>{j.kind}</td>
            <td>{j.protocol || '—'}</td>
            <td className="mono" style={{ fontSize: 12 }}>{j.target || '—'}</td>
            <td><span className={`badge ${j.status === 'done' ? 'badge-up' : j.status === 'failed' ? 'badge-down' : 'badge-warning'}`}>{j.status}</span></td>
            <td style={{ fontSize: 12 }}>{j.category || '—'}</td>
            <td className="error-msg" style={{ fontSize: 11, whiteSpace: 'normal', maxWidth: 280 }}>{j.error || ''}</td>
            <td className="muted" style={{ fontSize: 12 }}>{timeAgo(j.created_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
