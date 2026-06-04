import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Send, Plus, Trash2, FlaskConical, MessageSquare, Mail, Webhook, Hash, CircleCheck, CircleX } from 'lucide-react'
import { api, type NotificationChannel, type NotificationLogEntry } from '../api'
import { PageHeader, Panel, EmptyState, timeAgo } from '../components/ui'

const TYPES = [
  { key: 'slack', label: 'Slack', icon: Hash },
  { key: 'teams', label: 'Microsoft Teams', icon: MessageSquare },
  { key: 'telegram', label: 'Telegram', icon: Send },
  { key: 'webhook', label: 'Webhook', icon: Webhook },
  { key: 'email', label: 'Email (SMTP)', icon: Mail },
] as const

const sevCls = (s: string) => (s === 'critical' ? 'badge-down' : s === 'warning' ? 'badge-warning' : 'badge-unknown')

export function Notifications() {
  const qc = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [testMsg, setTestMsg] = useState('')
  const channels = useQuery({ queryKey: ['notification-channels'], queryFn: () => api.get<NotificationChannel[]>('/notification-channels') })
  const log = useQuery({ queryKey: ['notification-log'], queryFn: () => api.get<NotificationLogEntry[]>('/notification-log'), refetchInterval: 20_000 })
  const inv = () => { qc.invalidateQueries({ queryKey: ['notification-channels'] }); qc.invalidateQueries({ queryKey: ['notification-log'] }) }

  const toggle = useMutation({ mutationFn: (c: NotificationChannel) => api.patch(`/notification-channels/${c.id}`, { enabled: !c.enabled }), onSuccess: inv })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/notification-channels/${id}`), onSuccess: inv })
  const test = useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean; detail: string }>(`/notification-channels/${id}/test`, {}),
    onSuccess: (d) => { setTestMsg((d.ok ? '✓ ' : '✗ ') + d.detail); inv() },
    onError: (e) => setTestMsg('✗ ' + (e as Error).message),
  })

  const list = channels.data ?? []
  return (
    <div>
      <PageHeader title="Notifications" icon={Send}
        subtitle="Deliver alerts to Slack, Teams, Telegram, webhooks or email — severity-filtered, quiet-hours aware"
        actions={<button className="btn btn-primary btn-sm" onClick={() => setShowForm((v) => !v)}><Plus size={14} /> {showForm ? 'Cancel' : 'New channel'}</button>} />

      {testMsg && <div className={'enc-banner ' + (testMsg.startsWith('✓') ? 'info' : 'crit')} style={{ marginBottom: 12 }}>{testMsg}</div>}
      {showForm && <ChannelForm onDone={() => { setShowForm(false); inv() }} />}

      <Panel title="Channels" icon={Send} subtitle={`${list.length}`} pad={false}>
        {channels.isLoading && <div className="loading">Loading…</div>}
        {channels.data && list.length === 0 && <EmptyState icon={Send} title="No notification channels" message="Add a channel to start delivering alerts." action={<button className="btn btn-primary btn-sm" onClick={() => setShowForm(true)}>New channel</button>} />}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th>Name</th><th>Type</th><th>Target</th><th>Min severity</th><th>Quiet hours</th><th>Enabled</th><th></th></tr></thead>
            <tbody>
              {list.map((c) => (
                <tr key={c.id}>
                  <td className="cell-name">{c.name}</td>
                  <td>{TYPES.find((t) => t.key === c.type)?.label ?? c.type}</td>
                  <td className="mono">{c.target_hint}</td>
                  <td><span className={`badge ${sevCls(c.min_severity)}`}>{c.min_severity}+</span></td>
                  <td className="muted">{c.quiet_start && c.quiet_end ? `${c.quiet_start}–${c.quiet_end}` : '—'}</td>
                  <td>{c.enabled ? <span className="badge badge-up">enabled</span> : <span className="badge badge-disabled">disabled</span>}</td>
                  <td className="cell-actions">
                    <button className="btn btn-ghost btn-xs" disabled={test.isPending} onClick={() => { setTestMsg(''); test.mutate(c.id) }}><FlaskConical size={12} /> Test</button>
                    <button className="btn btn-ghost btn-xs" onClick={() => toggle.mutate(c)}>{c.enabled ? 'Disable' : 'Enable'}</button>
                    <button className="btn btn-ghost btn-xs" style={{ color: 'var(--crit)' }} onClick={() => del.mutate(c.id)}><Trash2 size={12} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Delivery Log" icon={Send} subtitle={`${log.data?.length ?? 0}`} pad={false}>
        {log.data && log.data.length === 0 && <EmptyState icon={Send} title="No deliveries yet" message="Sent, failed and test notifications appear here." />}
        {log.data && log.data.length > 0 && (
          <table className="data-table">
            <thead><tr><th>When</th><th>Status</th><th>Detail</th></tr></thead>
            <tbody>
              {log.data.map((e) => (
                <tr key={e.id}>
                  <td className="muted">{timeAgo(e.at)}</td>
                  <td>{e.status === 'sent' || e.status === 'test'
                    ? <span className="badge badge-up"><CircleCheck size={11} /> {e.status}</span>
                    : <span className="badge badge-down"><CircleX size={11} /> {e.status}</span>}</td>
                  <td className="muted" style={{ maxWidth: 600, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{e.detail || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}

function ChannelForm({ onDone }: { onDone: () => void }) {
  const [type, setType] = useState<string>('slack')
  const [name, setName] = useState('')
  const [minSeverity, setMinSeverity] = useState('warning')
  const [quietStart, setQuietStart] = useState('')
  const [quietEnd, setQuietEnd] = useState('')
  // target fields
  const [url, setUrl] = useState('')
  const [token, setToken] = useState('')
  const [chatId, setChatId] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('587')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')

  const m = useMutation({
    mutationFn: () => {
      const target: Record<string, unknown> = {}
      if (type === 'slack' || type === 'teams' || type === 'webhook') target.url = url
      if (type === 'telegram') { target.token = token; target.chat_id = chatId }
      if (type === 'email') { target.host = host; target.port = Number(port) || 587; target.username = username; target.password = password; target.from = from; target.to = to.split(',').map((x) => x.trim()).filter(Boolean) }
      return api.post('/notification-channels', {
        name, type, min_severity: minSeverity,
        quiet_start: quietStart || null, quiet_end: quietEnd || null, target,
      })
    },
    onSuccess: onDone,
  })

  return (
    <Panel title="New Notification Channel" icon={Plus}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 12 }}>
        <label className="form-field">Type
          <select className="field" value={type} onChange={(e) => setType(e.target.value)}>
            {TYPES.map((t) => <option key={t.key} value={t.key}>{t.label}</option>)}
          </select>
        </label>
        <label className="form-field">Name<input className="field" value={name} onChange={(e) => setName(e.target.value)} placeholder="NOC Slack" /></label>
        <label className="form-field">Minimum severity
          <select className="field" value={minSeverity} onChange={(e) => setMinSeverity(e.target.value)}>
            <option value="info">info+</option><option value="warning">warning+</option><option value="critical">critical only</option>
          </select>
        </label>
        <label className="form-field">Quiet hours start (HH:MM)<input className="field" value={quietStart} onChange={(e) => setQuietStart(e.target.value)} placeholder="22:00" /></label>
        <label className="form-field">Quiet hours end (HH:MM)<input className="field" value={quietEnd} onChange={(e) => setQuietEnd(e.target.value)} placeholder="07:00" /></label>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 12, marginTop: 12 }}>
        {(type === 'slack' || type === 'teams' || type === 'webhook') && (
          <label className="form-field" style={{ gridColumn: '1 / -1' }}>Incoming webhook URL<input className="field" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://hooks.slack.com/services/…" /></label>
        )}
        {type === 'telegram' && (<>
          <label className="form-field">Bot token<input className="field" type="password" value={token} onChange={(e) => setToken(e.target.value)} /></label>
          <label className="form-field">Chat ID<input className="field" value={chatId} onChange={(e) => setChatId(e.target.value)} /></label>
        </>)}
        {type === 'email' && (<>
          <label className="form-field">SMTP host<input className="field" value={host} onChange={(e) => setHost(e.target.value)} /></label>
          <label className="form-field">Port<input className="field" type="number" value={port} onChange={(e) => setPort(e.target.value)} /></label>
          <label className="form-field">Username<input className="field" value={username} onChange={(e) => setUsername(e.target.value)} /></label>
          <label className="form-field">Password<input className="field" type="password" value={password} onChange={(e) => setPassword(e.target.value)} /></label>
          <label className="form-field">From<input className="field" value={from} onChange={(e) => setFrom(e.target.value)} placeholder="hims@example.com" /></label>
          <label className="form-field" style={{ gridColumn: '1 / -1' }}>Recipients (comma-separated)<input className="field" value={to} onChange={(e) => setTo(e.target.value)} placeholder="noc@example.com, oncall@example.com" /></label>
        </>)}
      </div>

      <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>The webhook URL / token / password is encrypted at rest and never shown again. Use Test after saving to confirm delivery.</p>
      <div style={{ marginTop: 8 }}>
        <button className="btn btn-primary" disabled={!name || m.isPending} onClick={() => m.mutate()}>{m.isPending ? 'Saving…' : 'Create channel'}</button>
        {m.error && <span className="error-msg" style={{ marginLeft: 12 }}>{(m.error as Error).message}</span>}
      </div>
    </Panel>
  )
}
