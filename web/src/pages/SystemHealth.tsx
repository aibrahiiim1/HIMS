import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, ListChecks, RefreshCw } from 'lucide-react'
import { api, type RuntimeInfo } from '../api'
import { PageHeader, Panel } from '../components/ui'
import { StartupChecklist } from '../components/EncryptionSetup'

const ENC: Record<string, { cls: string; label: string }> = {
  enabled: { cls: 'badge-up', label: 'Enabled' },
  pending_restart: { cls: 'badge-warning', label: 'Pending restart' },
  missing_key: { cls: 'badge-down', label: 'Key missing' },
  no_metadata: { cls: 'badge-unknown', label: 'Not configured' },
  fingerprint_mismatch: { cls: 'badge-down', label: 'Fingerprint mismatch' },
  invalid_key: { cls: 'badge-down', label: 'Invalid key' },
}

export function SystemHealth() {
  const q = useQuery({
    queryKey: ['system-runtime'],
    queryFn: () => api.get<RuntimeInfo>('/system/runtime'),
    refetchInterval: 10_000,
  })
  const r = q.data
  const enc = r ? (ENC[r.encryption_state] ?? { cls: 'badge-unknown', label: r.encryption_state }) : null

  return (
    <div>
      <PageHeader title="System Health" icon={Activity} subtitle="Identity of the active API process, its encryption state, and startup checks" />
      <div className="stack">
        <Panel title="API Runtime Identity" icon={Activity} subtitle={r ? `PID ${r.process_id}` : undefined}>
          {q.isLoading && <div className="loading">Loading…</div>}
          {q.isError && <div className="enc-banner crit">Could not reach the API runtime endpoint.</div>}
          {r && (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 14 }}>
                <KV label="Process ID (PID)" mono value={String(r.process_id)} />
                <KV label="Encryption state" value={<span className={`badge ${enc!.cls}`}>{enc!.label}</span>} />
                <KV label="Key ID" mono value={r.key_id || '—'} />
                <KV label="Started at" value={r.started_at ? new Date(r.started_at).toLocaleString() : '—'} />
                <KV label="Uptime" value={r.uptime || '—'} />
                <KV label="Port" mono value={r.port || '—'} />
                <KV label="Environment" value={r.environment} />
                <KV label="Hostname" mono value={r.hostname} />
                <KV label="Version" mono value={r.api_version} />
                <KV label="Git commit" mono value={r.git_commit} />
                <KV label="Database" mono full value={r.database_url_redacted || '—'} />
              </div>
              <p className="muted" style={{ fontSize: 12, marginTop: 14 }}>
                This is the exact process answering API requests. Only one <code>hims-api</code> can own the port at a time — it claims the listen socket at startup and exits if another instance already holds it. If the encryption state above isn&apos;t what you expect, confirm this PID matches the instance you started.
              </p>
            </>
          )}
        </Panel>

        <Panel title="Startup Checklist" icon={ListChecks} subtitle="Live readiness checks — single instance, port owner, encryption">
          <StartupChecklist />
          <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
            <RefreshCw size={12} style={{ verticalAlign: '-1px' }} /> Re-runs automatically every 20s.
          </p>
        </Panel>
      </div>
    </div>
  )
}

function KV({ label, value, mono, full }: { label: string; value: ReactNode; mono?: boolean; full?: boolean }) {
  return (
    <div style={full ? { gridColumn: '1 / -1' } : undefined}>
      <div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 4 }}>{label}</div>
      <div style={{ fontSize: 14, fontFamily: mono ? 'var(--font-mono, monospace)' : undefined, wordBreak: 'break-all' }}>{value}</div>
    </div>
  )
}
