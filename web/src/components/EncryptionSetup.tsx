/* eslint-disable react-refresh/only-export-components */
// Shared deployment-setup kit: co-locates the StartupChecklist/CmdBlock
// components with their runbook + deployment-step helpers. The react-refresh
// rule is an HMR-only nicety, not correctness.
import { useQuery } from '@tanstack/react-query'
import { CircleCheck, TriangleAlert, CircleX, Copy, Download, Laptop, Server, Container, Cloud } from 'lucide-react'
import { api } from '../api'

export interface ChecklistItem { key: string; label: string; status: 'ok' | 'warn' | 'fail'; detail: string; action?: string }
export type DeployMode = 'local' | 'windows' | 'docker' | 'cloud'

export function downloadText(filename: string, text: string) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a'); a.href = url; a.download = filename; a.click(); URL.revokeObjectURL(url)
}

const ico = { ok: CircleCheck, warn: TriangleAlert, fail: CircleX }
const tone = { ok: 'var(--ok)', warn: 'var(--warn)', fail: 'var(--crit)' }

export function StartupChecklist() {
  const q = useQuery({ queryKey: ['startup-checklist'], queryFn: () => api.get<ChecklistItem[]>('/security/startup-checklist'), refetchInterval: 20_000 })
  const items = q.data ?? []
  if (q.isLoading) return <div className="loading">Running checks…</div>
  return (
    <ul className="checklist">
      {items.map((it) => {
        const Icon = ico[it.status]
        return (
          <li key={it.key} className="checklist-item">
            <span className="checklist-ico" style={{ color: tone[it.status] }}><Icon size={18} /></span>
            <div>
              <div className="checklist-label">{it.label}</div>
              <div className="muted" style={{ fontSize: 12 }}>{it.detail}{it.action ? ` — ${it.action}` : ''}</div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

export const DEPLOY_MODES: { key: DeployMode; label: string; icon: typeof Server; blurb: string }[] = [
  { key: 'local', label: 'Local Development', icon: Laptop, blurb: 'A developer machine. Not for production use.' },
  { key: 'windows', label: 'Windows Server / On-Premise', icon: Server, blurb: 'HIMS API runs as a Windows Service on your own server.' },
  { key: 'docker', label: 'Docker / Docker Compose', icon: Container, blurb: 'HIMS runs in containers via docker compose.' },
  { key: 'cloud', label: 'Cloud / Public Hosting', icon: Cloud, blurb: 'Azure App Service, Render/Railway/Fly.io, Kubernetes, or a managed secret store.' },
]

const KEYPH = '<PASTE-YOUR-RECOVERY-KEY>'

// Each guide is plain, copyable text — parameterised only by the (non-secret)
// key placeholder. The real key lives in the recovery file you downloaded.
export function deploymentSteps(mode: DeployMode): { title: string; lines: string[] }[] {
  switch (mode) {
    case 'local':
      return [{ title: 'Developer machine (PowerShell)', lines: [
        '# For developer machines only — never use this for a real deployment.',
        'Get-Process hims-api -ErrorAction SilentlyContinue | Stop-Process -Force',
        `$env:HIMS_ENCRYPTION_KEY = "${KEYPH}"`,
        '$env:HIMS_DATABASE_URL   = "postgres://hims:hims@localhost:5433/hims?sslmode=disable"',
        'go build -C D:\\WebProjects\\HIMS -o "$env:TEMP\\hims-api.exe" ./cmd/hims-api',
        'Start-Process "$env:TEMP\\hims-api.exe" -WindowStyle Hidden',
      ] }]
    case 'windows':
      return [
        { title: '1 — Set the key as a machine-level variable (Run PowerShell as Administrator)', lines: [
          `[Environment]::SetEnvironmentVariable('HIMS_ENCRYPTION_KEY','${KEYPH}','Machine')`,
        ] },
        { title: '2 — Restart the HIMS API Windows Service', lines: [
          'Restart-Service HIMS-API',
        ] },
        { title: '3 — Confirm the service is running', lines: [
          'Get-Service HIMS-API',
        ] },
        { title: 'First-time install (one-off, as Administrator)', lines: [
          '# Installs the API as a service using the bundled helper script.',
          'powershell -ExecutionPolicy Bypass -File deploy\\windows\\install-service.ps1',
          '# The script prompts for the encryption key and registers the HIMS-API service.',
        ] },
      ]
    case 'docker':
      return [
        { title: '1 — Add the key to your .env file (next to docker-compose.yml)', lines: [
          `HIMS_ENCRYPTION_KEY=${KEYPH}`,
        ] },
        { title: '2 — Reference it in docker-compose.yml under the hims-api service', lines: [
          'services:',
          '  hims-api:',
          '    environment:',
          '      - HIMS_ENCRYPTION_KEY=${HIMS_ENCRYPTION_KEY}',
        ] },
        { title: '3 — Restart the API container', lines: [
          'docker compose restart hims-api',
        ] },
        { title: '4 — Confirm', lines: [
          'docker compose ps hims-api',
        ] },
      ]
    case 'cloud':
      return [
        { title: 'Azure App Service', lines: [
          'Portal → your App Service → Settings → Environment variables (Application settings)',
          'Add: HIMS_ENCRYPTION_KEY = ' + KEYPH,
          'Click Apply — App Service restarts automatically and picks up the new value.',
        ] },
        { title: 'Render / Railway / Fly.io', lines: [
          'Render:  Dashboard → Service → Environment → Add HIMS_ENCRYPTION_KEY → Save (triggers redeploy).',
          'Railway: Project → Variables → New Variable HIMS_ENCRYPTION_KEY → service redeploys.',
          'Fly.io:  fly secrets set HIMS_ENCRYPTION_KEY=' + KEYPH + '   (restarts the app).',
        ] },
        { title: 'Kubernetes', lines: [
          `kubectl create secret generic hims-encryption --from-literal=HIMS_ENCRYPTION_KEY='${KEYPH}'`,
          '# reference it in the deployment:',
          '#   env:',
          '#   - name: HIMS_ENCRYPTION_KEY',
          '#     valueFrom: { secretKeyRef: { name: hims-encryption, key: HIMS_ENCRYPTION_KEY } }',
          'kubectl rollout restart deployment/hims-api',
        ] },
        { title: 'Managed secret stores (AWS / GCP / Azure)', lines: [
          'Store the key in AWS Secrets Manager / GCP Secret Manager / Azure Key Vault,',
          'then inject it into the API container/app as the HIMS_ENCRYPTION_KEY env var at deploy time.',
          'Restart / redeploy from the hosting platform after setting it.',
        ] },
      ]
  }
}

export function buildRunbook(mode: DeployMode | 'disaster'): string {
  const header = `HIMS — ${({ windows: 'Windows Server', docker: 'Docker', cloud: 'Cloud Hosting', local: 'Local Development', disaster: 'Disaster Recovery' } as Record<string, string>)[mode]} Runbook\n` +
    `Generated from the HIMS Encryption Setup Wizard\n${'='.repeat(60)}\n\n`
  if (mode === 'disaster') {
    return header + [
      'PURPOSE',
      'Recover the HIMS credential-encryption system after a lost or rotated key.',
      '',
      'BACKGROUND',
      '- All credential secrets are encrypted with AES-256-GCM using HIMS_ENCRYPTION_KEY.',
      '- The key lives only in the API process environment. It is never stored in the database.',
      '- Losing the key makes existing credential secrets unrecoverable (inventory/monitoring/',
      '  topology/reports keep working).',
      '',
      'IF THE KEY IS AVAILABLE (server rebuild / migration)',
      '1. Restore the database.',
      '2. Set HIMS_ENCRYPTION_KEY to the SAME key the credentials were encrypted with.',
      '3. Start/restart the API.',
      '4. Administration → Encryption → Status: confirm Enabled + fingerprint matches; run Validate.',
      '',
      'IF THE KEY IS LOST',
      '1. Administration → Encryption → Credential Recovery → Reset Credential Secrets (type RESET CREDENTIALS).',
      '   This clears only the secret values; records, assignments and groups are preserved.',
      '2. Administration → Encryption → Key Management → Generate key. Save + back up the new key.',
      '3. Set HIMS_ENCRYPTION_KEY to the new key in your deployment env and restart (see your deployment runbook).',
      '4. Encryption → Credential Recovery → Credentials Needing Re-entry: re-enter each secret.',
      '',
      'PREVENTION',
      '- Immediately back up the key to your secrets manager AND an offline copy after generating it.',
      '- Re-validate after every rotation.',
    ].join('\n') + '\n'
  }
  const steps = deploymentSteps(mode)
  const body = steps.map((s) => `## ${s.title}\n${s.lines.join('\n')}`).join('\n\n')
  return header +
    'STEP 1 — Generate or obtain your encryption key (Administration → Encryption → Key Management → Generate).\n' +
    '         Save the recovery file securely; the key is shown only once.\n\n' +
    'STEP 2 — Configure + restart for this deployment:\n\n' + body +
    '\n\nSTEP 3 — Validate: Administration → Encryption → Status should show Enabled and the fingerprint should match.\n'
}

export function CmdBlock({ lines }: { lines: string[] }) {
  const text = lines.join('\n')
  return (
    <div className="cmd-block">
      <button className="cmd-copy" title="Copy" onClick={() => navigator.clipboard?.writeText(text)}><Copy size={13} /></button>
      <pre>{text}</pre>
    </div>
  )
}

export function DownloadBtn({ filename, text, label }: { filename: string; text: string; label: string }) {
  return <button className="btn btn-ghost btn-sm" onClick={() => downloadText(filename, text)}><Download size={14} /> {label}</button>
}
