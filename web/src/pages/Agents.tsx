import { Fragment, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  api, saveBlob, type RelayAgent, type RelayAgentCreated, type RelayAgentDetail, type AgentJob,
  type Location, type EncryptionStatus, type InstallerAvailability,
} from '../api'
import { PageHeader, Panel, timeAgo } from '../components/ui'
import { Radar, Server, KeyRound, Download, ChevronRight, CheckCircle2, Circle, ShieldAlert, Terminal } from 'lucide-react'

// HIMS Relay Agent / Site Collector — the operator screen for the single,
// official, installable collector that replaces the older standalone helper
// scripts (native PowerShell collector, WMI/DCOM collector). One agent runs on a
// trusted machine inside a site; HIMS registers it, hands it credentialed jobs
// (pull model, NAT-friendly), and persists the structured inventory it returns.
// Secrets never appear here: the enrollment token is shown exactly once at
// creation, and only its SHA-256 hash is stored.

const btn: React.CSSProperties = {
  padding: '8px 16px', background: '#1565c0', color: '#fff', border: 'none',
  borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
}
const input: React.CSSProperties = {
  padding: '8px 10px', border: '1px solid #ccc', borderRadius: 6, fontSize: 13, width: '100%',
}
const ghost: React.CSSProperties = { padding: '3px 8px', background: 'transparent', color: '#90caf9', border: '1px solid #90caf9', borderRadius: 6, cursor: 'pointer', fontSize: 12 }
const danger: React.CSSProperties = { ...ghost, color: '#ef9a9a', borderColor: '#ef9a9a' }

const CAP_LABEL: Record<string, string> = {
  winrm: 'Windows (WinRM)', wmi: 'Windows (WMI/DCOM)', ssh: 'Linux (SSH)',
  snmp: 'SNMP', onvif: 'CCTV (ONVIF)', vsphere: 'VMware (vSphere)',
}

function AgentStatusBadge({ a }: { a: RelayAgent }) {
  if (!a.enabled) return <span className="badge badge-unknown">disabled</span>
  if (a.online) return <span className="badge badge-up">online</span>
  if (a.status === 'registered' && !a.last_heartbeat) return <span className="badge badge-warning">awaiting first contact</span>
  return <span className="badge badge-down">offline</span>
}

function EncryptionGate() {
  const q = useQuery({ queryKey: ['enc-status'], queryFn: () => api.get<EncryptionStatus>('/security/encryption/status'), retry: 0 })
  if (!q.data || q.data.enabled) return null
  return (
    <div className="enc-banner crit" style={{ marginBottom: 16 }}>
      <span>🔒</span>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 700 }}>Credential storage is disabled — no encryption key is configured</div>
        <div style={{ fontSize: 12, marginTop: 2 }}>Relay Agents are handed credentials decrypted from HIMS per job; without a key, jobs that need credentials cannot run. Set <code>HIMS_ENCRYPTION_KEY</code> and restart the API.</div>
      </div>
      <Link className="btn btn-sm" to="/security/encryption" style={{ whiteSpace: 'nowrap' }}>Configure Encryption →</Link>
    </div>
  )
}

export function Agents() {
  const qc = useQueryClient()
  const [showNew, setShowNew] = useState(false)
  const [created, setCreated] = useState<RelayAgentCreated | null>(null)
  const [jobsFor, setJobsFor] = useState<string | null>(null)
  const [testMsg, setTestMsg] = useState<Record<string, string>>({})

  const list = useQuery({
    queryKey: ['relay-agents'],
    queryFn: () => api.get<RelayAgent[]>('/agents'),
    refetchInterval: 15_000, // keep online/heartbeat fresh
  })
  const locs = useQuery({ queryKey: ['locations-all'], queryFn: () => api.get<Location[]>('/locations/all') })
  const availability = useQuery({ queryKey: ['agent-installer-availability'], queryFn: () => api.get<InstallerAvailability>('/agents/installer-availability') })
  const refresh = () => qc.invalidateQueries({ queryKey: ['relay-agents'] })
  const locName = (id?: string) => locs.data?.find((l) => l.id === id)?.name

  const test = useMutation({
    mutationFn: (id: string) => api.post<{ job_id: string; status: string }>(`/agents/${id}/test`, {}),
    onSuccess: (_d, id) => { setTestMsg((m) => ({ ...m, [id]: 'Test job queued — the agent runs it on its next poll (~8s).' })); refresh() },
    onError: (e, id) => setTestMsg((m) => ({ ...m, [id]: (e as Error).message })),
  })
  const toggle = useMutation({
    mutationFn: (a: RelayAgent) => api.patch(`/agents/${a.id}`, { enabled: !a.enabled }),
    onSuccess: refresh,
  })
  const setSite = useMutation({
    mutationFn: ({ id, location_id }: { id: string; location_id: string }) => api.patch(`/agents/${id}`, { location_id }),
    onSuccess: refresh,
  })
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/agents/${id}`),
    onSuccess: () => { setJobsFor(null); refresh() },
  })

  return (
    <div>
      <PageHeader
        title="Relay Agents"
        subtitle="The single, official site collector — replaces the standalone helper scripts"
        icon={Radar}
        actions={<button style={btn} onClick={() => { setShowNew((v) => !v); setCreated(null) }}>{showNew ? 'Cancel' : '+ Register agent'}</button>}
      />

      <EncryptionGate />

      <Panel title="What a Relay Agent does" icon={Server} className="mb">
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          Install <strong>one</strong> Relay Agent on a trusted machine inside a site (a domain-joined
          Windows box or Windows Server; Linux for SSH/SNMP). It registers with HIMS, then <strong>pulls</strong>
          {' '}credentialed collection jobs (no inbound firewall rule to the agent), runs them locally —
          modern <strong>WinRM</strong> and legacy <strong>WMI/DCOM</strong> for Windows, with SSH/SNMP/CCTV/VMware
          as it gains capabilities — and posts structured inventory back, which HIMS persists to OS Inventory.
          It authenticates with a per-agent token and never logs or stores the credentials it is handed.
          This is the <strong>preferred</strong> collection path; direct collection from the HIMS server still
          works where the network allows it.
        </p>
      </Panel>

      {showNew && !created && (
        <NewAgentForm
          locations={locs.data ?? []}
          onCreated={(c) => { setCreated(c); setShowNew(false); refresh() }}
          onCancel={() => setShowNew(false)}
        />
      )}

      {created && (
        <InstallWizard
          created={created}
          locName={locName}
          availability={availability.data}
          onTokenRegenerated={(t) => setCreated({ ...created, token: t })}
          onDone={() => setCreated(null)}
        />
      )}

      <Panel title="Registered agents" icon={Radar} actions={list.data ? <span className="muted" style={{ fontSize: 12 }}>{list.data.length} total</span> : undefined}>
        {list.isLoading && <div className="loading">Loading…</div>}
        {list.error && <div className="error-msg">{(list.error as Error).message}</div>}
        {list.data && list.data.length === 0 && (
          <div className="muted">No relay agents yet. Click <strong>Register agent</strong> to mint a token and install one on a site machine.</div>
        )}
        {list.data && list.data.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Name</th><th>Site</th><th>Host</th><th>IP</th><th>OS</th><th>Version</th>
                <th>Capabilities</th><th>Status</th><th>Last heartbeat</th><th></th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((a) => (
                <Fragment key={a.id}>
                  <tr>
                    <td><Link to={`/agents/${a.id}`}><strong>{a.name}</strong></Link>{a.failed_jobs ? <span className="badge badge-down" style={{ marginLeft: 6 }}>{a.failed_jobs} failed</span> : null}{a.last_error && <div className="error-msg" style={{ fontSize: 11, whiteSpace: 'normal', maxWidth: 220 }}>{a.last_error}</div>}</td>
                    <td style={{ minWidth: 150 }}>
                      <select style={{ ...input, fontSize: 12, padding: '4px 6px' }} value={a.location_id ?? ''} onChange={(e) => setSite.mutate({ id: a.id, location_id: e.target.value })}>
                        <option value="">— unassigned —</option>
                        {(locs.data ?? []).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
                      </select>
                    </td>
                    <td>{a.hostname || <span className="muted">—</span>}</td>
                    <td className="mono" style={{ fontSize: 12 }}>{a.ip || '—'}</td>
                    <td style={{ fontSize: 12 }}>{a.os || <span className="muted">—</span>}</td>
                    <td style={{ fontSize: 12 }}>{a.version || <span className="muted">—</span>}</td>
                    <td style={{ fontSize: 11, maxWidth: 180, whiteSpace: 'normal' }}>
                      {a.capabilities.length === 0 ? <span className="muted">—</span> : a.capabilities.map((c) => <span key={c} className="badge badge-unknown" style={{ marginRight: 3 }}>{CAP_LABEL[c] ?? c}</span>)}
                    </td>
                    <td><AgentStatusBadge a={a} /></td>
                    <td style={{ fontSize: 12 }} className="muted">{timeAgo(a.last_heartbeat)}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button style={ghost} disabled={test.isPending} onClick={() => test.mutate(a.id)}>Test</button>{' '}
                      <button style={ghost} onClick={() => setJobsFor(jobsFor === a.id ? null : a.id)}>{jobsFor === a.id ? 'Hide jobs' : 'Jobs'}</button>{' '}
                      <button style={ghost} onClick={() => toggle.mutate(a)}>{a.enabled ? 'Disable' : 'Enable'}</button>{' '}
                      <button style={danger} onClick={() => { if (confirm(`Delete relay agent "${a.name}"? Its token stops working immediately.`)) del.mutate(a.id) }}>Delete</button>
                      {testMsg[a.id] && <div className="muted" style={{ fontSize: 11, marginTop: 4, whiteSpace: 'normal', maxWidth: 320 }}>{testMsg[a.id]}</div>}
                    </td>
                  </tr>
                  {jobsFor === a.id && (
                    <tr>
                      <td colSpan={10} style={{ background: 'var(--surface-2)' }}><AgentJobs agentId={a.id} /></td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
        {del.error && <div className="error-msg" style={{ marginTop: 8 }}>{(del.error as Error).message}</div>}
      </Panel>

      <AdvancedInstall />
    </div>
  )
}

function NewAgentForm({ locations, onCreated, onCancel }: {
  locations: Location[]; onCreated: (c: RelayAgentCreated) => void; onCancel: () => void
}) {
  const [name, setName] = useState('')
  const [locationID, setLocationID] = useState('')
  const create = useMutation({
    mutationFn: () => api.post<RelayAgentCreated>('/agents', { name: name.trim(), location_id: locationID }),
    onSuccess: onCreated,
  })
  return (
    <Panel title="Register a new agent" icon={KeyRound} className="mb">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(240px,1fr))', gap: 12 }}>
        <label>Agent name<input style={input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Hotel A — site collector" /></label>
        <label>Assign to site (optional)
          <select style={input} value={locationID} onChange={(e) => setLocationID(e.target.value)}>
            <option value="">— unassigned —</option>
            {locations.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
          </select>
        </label>
      </div>
      <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>
        Registering mints a one-time enrollment <strong>token</strong>. It is shown once on the next screen — copy it
        into the install command. Only its hash is stored; HIMS can never display it again. Assigning a site lets HIMS
        prefer this agent for devices in that site during scans.
      </p>
      <div style={{ marginTop: 12 }}>
        <button style={btn} disabled={!name.trim() || create.isPending} onClick={() => create.mutate()}>
          {create.isPending ? 'Registering…' : 'Register & mint token'}
        </button>{' '}
        <button style={ghost} onClick={onCancel}>Cancel</button>
        {create.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(create.error as Error).message}</span>}
      </div>
    </Panel>
  )
}

// InstallWizard — the operator's Download → Install → Online flow shown right
// after registering an agent. No build tools: it downloads a ready installer
// package (binary + service installer + prefilled token/URL/site) and tracks the
// agent until it reports online. The enrollment token is shown/downloaded once;
// "Regenerate token" mints a fresh one (and a fresh installer) if it was lost.
function InstallWizard({ created, locName, availability, onTokenRegenerated, onDone }: {
  created: RelayAgentCreated
  locName: (id?: string) => string | undefined
  availability?: InstallerAvailability
  onTokenRegenerated: (token: string) => void
  onDone: () => void
}) {
  const agentId = created.agent.id
  const token = created.token
  const [copied, setCopied] = useState('')
  const [dlErr, setDlErr] = useState('')
  const [dling, setDling] = useState('')
  const [tested, setTested] = useState(false)
  const copy = (key: string, text: string) => { navigator.clipboard?.writeText(text).then(() => { setCopied(key); setTimeout(() => setCopied(''), 1500) }) }

  // Live status: poll this agent so the checklist + "Online" step update on their own.
  const status = useQuery({
    queryKey: ['relay-agent', agentId],
    queryFn: () => api.get<RelayAgentDetail>(`/agents/${agentId}`),
    refetchInterval: 5_000,
  })
  const online = !!status.data?.agent.online
  const heard = !!status.data?.agent.last_heartbeat

  const download = async (os: 'windows' | 'linux') => {
    setDlErr(''); setDling(os)
    try {
      const blob = await api.postBlob(`/agents/${agentId}/installer`, { token, os })
      saveBlob(blob, `hims-relay-agent-${slug(created.agent.name)}-${os}.zip`)
    } catch (e) { setDlErr((e as Error).message) } finally { setDling('') }
  }
  const regen = useMutation({
    mutationFn: () => api.post<{ token: string }>(`/agents/${agentId}/regenerate-token`, {}),
    onSuccess: (d) => onTokenRegenerated(d.token),
    onError: (e) => setDlErr((e as Error).message),
  })
  const test = useMutation({
    mutationFn: () => api.post(`/agents/${agentId}/test`, {}),
    onSuccess: () => setTested(true),
  })

  const winReady = availability ? availability.windows : true
  const silentCmd = 'install.cmd /silent'

  const steps: { label: string; done: boolean; active: boolean }[] = [
    { label: 'Register agent', done: true, active: false },
    { label: 'Download the installer', done: dling === '' && copied !== '' ? false : true, active: !heard },
    { label: 'Copy to the site machine & run install.cmd as Administrator', done: heard, active: !heard },
    { label: 'Agent reports a heartbeat', done: heard, active: !heard },
    { label: 'Test the agent', done: tested, active: heard && !tested },
    { label: 'Ready — agent is online', done: online, active: online },
  ]

  return (
    <Panel
      title={`Set up “${created.agent.name}”`} icon={Download} className="mb"
      subtitle={created.agent.location_id ? `for ${locName(created.agent.location_id) ?? 'site'}` : 'unassigned'}
      actions={<button style={ghost} onClick={onDone}>Done</button>}
    >
      {/* Security: token shown/downloaded once. */}
      <div className="enc-banner" style={{ background: '#332b1a', borderColor: '#7a5a1a', marginBottom: 14 }}>
        <ShieldAlert size={18} />
        <div style={{ fontSize: 12 }}>
          The installer below embeds this agent's <strong>one-time enrollment token</strong> — the downloaded file is
          sensitive until installed. Copy it to the target machine over a trusted channel and delete it once the agent
          is online. It contains <strong>no device credentials</strong>; HIMS sends those to the agent per job only.
        </div>
      </div>

      {/* Step checklist */}
      <ol className="install-steps" style={{ listStyle: 'none', padding: 0, margin: '0 0 14px' }}>
        {steps.map((s, i) => (
          <li key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', fontSize: 13 }}>
            {s.done ? <CheckCircle2 size={16} color="var(--ok)" /> : <Circle size={16} color={s.active ? 'var(--brand)' : 'var(--text-faint)'} />}
            <span style={{ fontWeight: s.active ? 600 : 400, color: s.done ? 'var(--text)' : undefined }}>{i + 1}. {s.label}</span>
          </li>
        ))}
      </ol>

      {/* Primary action: download the ready installer */}
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
        {winReady ? (
          <button style={btn} disabled={dling === 'windows'} onClick={() => download('windows')}>
            {dling === 'windows' ? 'Preparing…' : '⬇ Download Windows Installer'}
          </button>
        ) : (
          <span className="muted" style={{ fontSize: 12, maxWidth: 360 }}>
            Windows installer not available yet — the agent binary isn't staged on this HIMS server.
            Ask your administrator to run <code className="mono">deploy/build-agents.ps1</code>.
          </span>
        )}
        {availability?.linux && (
          <button style={ghost} disabled={dling === 'linux'} onClick={() => download('linux')}>
            {dling === 'linux' ? 'Preparing…' : '⬇ Download Linux Installer'}
          </button>
        )}
        <button style={ghost} onClick={() => regen.mutate()} disabled={regen.isPending} title="Mint a new token and download a fresh installer (the old one stops working)">
          {regen.isPending ? 'Regenerating…' : 'Regenerate token'}
        </button>
      </div>
      {dlErr && <div className="error-msg" style={{ marginTop: 8, fontSize: 12 }}>{dlErr}</div>}

      <div style={{ fontSize: 13, marginTop: 12, lineHeight: 1.6 }}>
        <strong>On the site machine</strong> (a trusted Windows / domain-joined box): unzip, right-click
        {' '}<code>install.cmd</code> → <strong>Run as administrator</strong>. The agent installs as the Windows service
        {' '}<em>HIMS Relay Agent</em> (auto-start, restart-on-failure) and appears online here within ~30s.
      </div>

      {/* Silent install command for IT/admin deployment */}
      <div style={{ marginTop: 10 }}>
        <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Silent / unattended deployment (run from the unzipped folder, elevated):</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <code className="mono" style={{ flex: 1, padding: '8px 10px', background: 'var(--surface-3)', borderRadius: 6, fontSize: 12 }}>{silentCmd}</code>
          <button style={ghost} onClick={() => copy('silent', silentCmd)}>{copied === 'silent' ? 'Copied!' : 'Copy'}</button>
        </div>
      </div>

      {/* Live confirmation + test */}
      <div style={{ marginTop: 14, display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
        <span className={`badge ${online ? 'badge-up' : heard ? 'badge-warning' : 'badge-unknown'}`}>
          {online ? 'Agent online' : heard ? 'Agent heard, warming up' : 'Waiting for the agent…'}
        </span>
        <button style={ghost} disabled={!online || test.isPending} onClick={() => test.mutate()} title={online ? 'Queue a no-op job to confirm round-trip' : 'Available once the agent is online'}>
          {test.isPending ? 'Testing…' : 'Test agent'}
        </button>
        {tested && <span className="muted" style={{ fontSize: 12 }}>Test job queued — watch it complete under the agent's Jobs.</span>}
        <Link to={`/agents/${agentId}`} style={{ fontSize: 12 }}>Open agent detail →</Link>
      </div>

      {/* Token, available once for password managers / advanced use */}
      <details style={{ marginTop: 12 }}>
        <summary style={{ cursor: 'pointer', fontSize: 12 }} className="muted">Show enrollment token (shown once)</summary>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 6 }}>
          <code className="mono" style={{ flex: 1, padding: '8px 10px', background: 'var(--surface-3)', borderRadius: 6, wordBreak: 'break-all', fontSize: 12 }}>{token}</code>
          <button style={ghost} onClick={() => copy('token', token)}>{copied === 'token' ? 'Copied!' : 'Copy token'}</button>
        </div>
      </details>
    </Panel>
  )
}

function slug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'agent'
}

function AgentJobs({ agentId }: { agentId: string }) {
  const jobs = useQuery({
    queryKey: ['agent-jobs', agentId],
    queryFn: () => api.get<AgentJob[]>(`/agents/${agentId}/jobs`),
    refetchInterval: 5_000,
  })
  if (jobs.isLoading) return <div className="loading">Loading jobs…</div>
  if (jobs.error) return <div className="error-msg">{(jobs.error as Error).message}</div>
  if (!jobs.data || jobs.data.length === 0) return <div className="muted" style={{ padding: 8 }}>No jobs yet. Use <strong>Test</strong>, or scan/collect a device routed via this agent.</div>
  return (
    <table style={{ margin: 4 }}>
      <thead><tr><th>Kind</th><th>Protocol</th><th>Target</th><th>Status</th><th>Category</th><th>Error</th><th>Created</th></tr></thead>
      <tbody>
        {jobs.data.map((j) => (
          <tr key={j.id}>
            <td>{j.kind}</td>
            <td>{j.protocol || '—'}</td>
            <td className="mono" style={{ fontSize: 12 }}>{j.target || '—'}</td>
            <td><span className={`badge ${j.status === 'done' ? 'badge-up' : j.status === 'failed' ? 'badge-down' : 'badge-warning'}`}>{j.status}</span></td>
            <td style={{ fontSize: 12 }}>{j.category || '—'}</td>
            <td className="error-msg" style={{ fontSize: 11, whiteSpace: 'normal', maxWidth: 260 }}>{j.error || ''}</td>
            <td className="muted" style={{ fontSize: 12 }}>{timeAgo(j.created_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// AdvancedInstall — the manual/no-installer path, collapsed by default. The
// default experience is Register → Download → Install; this is for advanced
// operators and developers only.
function AdvancedInstall() {
  return (
    <Panel title="Advanced manual installation" icon={Terminal}>
      <details>
        <summary style={{ cursor: 'pointer', fontSize: 13 }} className="muted">
          The recommended path is <strong>Register → Download Installer → Run as administrator</strong>. Expand only if
          you need to run the agent by hand or build it from source.
        </summary>
        <div style={{ fontSize: 13, lineHeight: 1.7, marginTop: 10 }}>
          <p className="muted" style={{ margin: '0 0 8px' }}>
            <ChevronRight size={12} style={{ verticalAlign: '-1px' }} /> The downloaded installer already contains the
            agent binary and prefilled config — no build step is required for normal use.
          </p>
          <strong>Run by hand (no service):</strong> from the unzipped package on the target machine:
          <pre className="mono" style={{ background: 'var(--surface-3)', padding: 10, borderRadius: 6, fontSize: 12, overflowX: 'auto' }}>{`set HIMS_URL=${window.location.origin}
set HIMS_AGENT_TOKEN=<token from this agent>
set HIMS_AGENT_NAME=<agent name>
hims-agent.exe -console`}</pre>
          <strong>Build from source (developers / to stage server binaries):</strong>
          <pre className="mono" style={{ background: 'var(--surface-3)', padding: 10, borderRadius: 6, fontSize: 12, overflowX: 'auto' }}>{`# stage both OS binaries into the HIMS dist dir so the UI can serve installers
deploy/build-agents.ps1            # Windows (PowerShell)
deploy/build-agents.sh             # Linux/macOS`}</pre>
          <div className="muted" style={{ fontSize: 12, display: 'flex', gap: 6, alignItems: 'center' }}>
            <Download size={13} /> WMI/DCOM collection requires the agent on Windows; WinRM works from either OS. The
            agent never persists credentials — HIMS sends them per job and the agent discards them after each run.
          </div>
        </div>
      </details>
    </Panel>
  )
}
