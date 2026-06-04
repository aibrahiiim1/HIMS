import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { FileCode, DownloadCloud, GitCompare, RefreshCw, CircleCheck, FileDiff } from 'lucide-react'
import {
  api, type Device, type ConfigBackup, type ConfigOverview, type ConfigDiff, type ConfigBackupContent,
} from '../api'
import { PageHeader, Panel, Kpi, EmptyState, timeAgo } from '../components/ui'

// Drivers HIMS can pull a running-config from over SSH (mirrors
// internal/config.CommandFor on the server). Only these devices can be backed
// up; everything else is filtered out of the picker.
const SUPPORTED = new Set([
  'cisco_ios', 'aruba_hpe', 'extreme_switch', 'fortigate', 'huawei_vrp',
  'arista_eos', 'juniper_junos', 'paloalto_panos',
])

const shortHash = (h: string) => h.slice(0, 12)
const fmtSize = (n: number) => (n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`)

// Config Backup (#10) + Drift (#11). Captures device running-configs over SSH,
// stores them AES-256-GCM encrypted, versions them, and diffs any two versions.
// A capture needs the device to have an 'ssh' credential bound; the server
// auto-selects the per-vendor command and retries with legacy KEX for old gear.
export function ConfigBackups() {
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const selected = params.get('device') ?? ''
  const setSelected = (id: string) => setParams(id ? { device: id } : {}, { replace: true })

  const overview = useQuery({ queryKey: ['config-overview'], queryFn: () => api.get<ConfigOverview>('/config/overview') })
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api.get<Device[]>('/devices') })

  const targets = useMemo(
    () => (devices.data ?? []).filter((d) => d.driver && SUPPORTED.has(d.driver)),
    [devices.data],
  )

  return (
    <div>
      <PageHeader title="Config Backup & Drift" icon={FileCode}
        subtitle="Pull device running-configs over SSH, version them encrypted, and track configuration drift across captures" />

      <div className="kpi-grid">
        <Kpi label="Devices Backed Up" value={overview.data?.devices_backed_up ?? '—'} icon={FileCode} tone="info"
          sub={targets.length ? `of ${targets.length} eligible` : undefined} />
        <Kpi label="Total Versions" value={overview.data?.total_backups ?? '—'} icon={FileDiff} />
        <Kpi label="Changed (24h)" value={overview.data?.changed_today ?? '—'} icon={GitCompare}
          tone={(overview.data?.changed_today ?? 0) > 0 ? 'warn' : 'default'} />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '340px 1fr', gap: 16, alignItems: 'start' }}>
        <Panel title="Eligible Devices" icon={FileCode} subtitle={`${targets.length}`} pad={false}>
          {devices.isLoading && <div className="loading">Loading…</div>}
          {devices.data && targets.length === 0 && (
            <EmptyState icon={FileCode} title="No eligible devices"
              message="Config backup works on switches and firewalls with a recognised driver (Cisco IOS, Aruba/HPE, Extreme, FortiGate, Huawei). None are classified yet." />
          )}
          {targets.length > 0 && (
            <table className="data-table">
              <thead><tr><th>Device</th><th>Driver</th></tr></thead>
              <tbody>
                {targets.map((d) => (
                  <tr key={d.id} className={d.id === selected ? 'row-selected' : ''} style={{ cursor: 'pointer' }}
                    onClick={() => setSelected(d.id)}>
                    <td className="cell-name">{d.name}<div className="mono muted" style={{ fontSize: 11 }}>{d.primary_ip ?? '—'}</div></td>
                    <td><span className="badge badge-unknown">{d.driver}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>

        {selected
          ? <DeviceConfig deviceId={selected} deviceName={targets.find((d) => d.id === selected)?.name ?? ''}
              onChanged={() => { qc.invalidateQueries({ queryKey: ['config-overview'] }) }} />
          : <RecentActivity overview={overview.data} onPick={setSelected} />}
      </div>
    </div>
  )
}

function RecentActivity({ overview, onPick }: { overview?: ConfigOverview; onPick: (id: string) => void }) {
  const recent = overview?.recent ?? []
  return (
    <Panel title="Recent Captures" icon={RefreshCw} subtitle={`${recent.length}`} pad={false}>
      {overview && recent.length === 0 && (
        <EmptyState icon={FileCode} title="No backups captured yet"
          message="Select a device on the left and choose “Back up now” to capture its first running-config version." />
      )}
      {recent.length > 0 && (
        <table className="data-table">
          <thead><tr><th>Device</th><th>When</th><th>By</th><th>Size</th><th>Change</th></tr></thead>
          <tbody>
            {recent.map((b) => (
              <tr key={b.id} style={{ cursor: 'pointer' }} onClick={() => onPick(b.device_id)}>
                <td className="cell-name">{b.device_name}</td>
                <td className="muted">{timeAgo(b.captured_at)}</td>
                <td className="muted">{b.captured_by}</td>
                <td className="mono">{fmtSize(b.size_bytes)}</td>
                <td>{b.changed ? <span className="badge badge-warning">changed</span> : <span className="badge badge-up">no change</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

function DeviceConfig({ deviceId, deviceName, onChanged }: { deviceId: string; deviceName: string; onChanged: () => void }) {
  const qc = useQueryClient()
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [pick, setPick] = useState<string[]>([])
  const [diff, setDiff] = useState<ConfigDiff | null>(null)

  const versions = useQuery({ queryKey: ['config-backups', deviceId], queryFn: () => api.get<ConfigBackup[]>(`/devices/${deviceId}/config-backups`) })

  const capture = useMutation({
    mutationFn: () => api.post<ConfigBackup>(`/devices/${deviceId}/config-backups`, {}),
    onSuccess: (b) => {
      setMsg({ ok: true, text: b.changed ? `Captured — configuration changed (${fmtSize(b.size_bytes)})` : `Captured — no change since last backup (${fmtSize(b.size_bytes)})` })
      qc.invalidateQueries({ queryKey: ['config-backups', deviceId] })
      onChanged()
    },
    onError: (e) => setMsg({ ok: false, text: (e as Error).message }),
  })

  const runDiff = useMutation({
    mutationFn: ([a, b]: string[]) => api.get<ConfigDiff>(`/config-backups/diff?a=${a}&b=${b}`),
    onSuccess: (d) => setDiff(d),
    onError: (e) => setMsg({ ok: false, text: (e as Error).message }),
  })

  const download = async (id: string) => {
    try {
      const c = await api.get<ConfigBackupContent>(`/config-backups/${id}/content`)
      const blob = new Blob([c.content], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${deviceName || deviceId}-${c.captured_at.slice(0, 19).replace(/[:T]/g, '')}.cfg`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      setMsg({ ok: false, text: (e as Error).message })
    }
  }

  const toggle = (id: string) => setPick((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id].slice(-2)))

  const list = versions.data ?? []
  return (
    <div style={{ display: 'grid', gap: 16 }}>
      <Panel title={`Versions — ${deviceName}`} icon={FileCode} subtitle={list.length ? `${list.length}` : undefined}
        actions={<>
          <button className="btn btn-ghost btn-sm" disabled={pick.length !== 2 || runDiff.isPending} onClick={() => runDiff.mutate(pick)}><GitCompare size={14} /> Compare selected</button>
          <button className="btn btn-primary btn-sm" disabled={capture.isPending} onClick={() => { setMsg(null); capture.mutate() }}><RefreshCw size={14} /> {capture.isPending ? 'Capturing…' : 'Back up now'}</button>
        </>}
        pad={false}>
        {msg && <div className={'enc-banner ' + (msg.ok ? 'info' : 'crit')} style={{ margin: 12 }}>{msg.ok ? <CircleCheck size={14} /> : null} {msg.text}</div>}
        {versions.isLoading && <div className="loading">Loading…</div>}
        {versions.data && list.length === 0 && !msg && (
          <EmptyState icon={FileCode} title="No versions yet"
            message="Choose “Back up now” to capture the first running-config. The device must have an 'ssh' credential bound; the per-vendor command is selected automatically." />
        )}
        {list.length > 0 && (
          <table className="data-table">
            <thead><tr><th style={{ width: 36 }}></th><th>Captured</th><th>By</th><th>Hash</th><th>Size</th><th>Change</th><th></th></tr></thead>
            <tbody>
              {list.map((b) => (
                <tr key={b.id} className={pick.includes(b.id) ? 'row-selected' : ''}>
                  <td><input type="checkbox" checked={pick.includes(b.id)} onChange={() => toggle(b.id)} /></td>
                  <td>{timeAgo(b.captured_at)}</td>
                  <td className="muted">{b.captured_by}</td>
                  <td className="mono" title={b.sha256}>{shortHash(b.sha256)}</td>
                  <td className="mono">{fmtSize(b.size_bytes)}</td>
                  <td>{b.changed ? <span className="badge badge-warning">changed</span> : <span className="badge badge-up">no change</span>}</td>
                  <td className="cell-actions"><button className="btn btn-ghost btn-xs" onClick={() => download(b.id)}><DownloadCloud size={12} /> Download</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {diff && <DiffPanel diff={diff} onClose={() => setDiff(null)} />}
    </div>
  )
}

function DiffPanel({ diff, onClose }: { diff: ConfigDiff; onClose: () => void }) {
  return (
    <Panel title="Configuration Diff" icon={FileDiff}
      subtitle={`+${diff.added} / −${diff.removed}`}
      actions={<button className="btn btn-ghost btn-sm" onClick={onClose}>Close</button>}
      pad={false}>
      <div className="muted" style={{ padding: '8px 12px', fontSize: 12 }}>
        <span className="mono">{diff.a.sha256.slice(0, 12)}</span> ({new Date(diff.a.captured_at).toLocaleString()})
        {'  →  '}
        <span className="mono">{diff.b.sha256.slice(0, 12)}</span> ({new Date(diff.b.captured_at).toLocaleString()})
      </div>
      {diff.added === 0 && diff.removed === 0
        ? <EmptyState icon={CircleCheck} title="Identical" message="These two versions have no configuration differences." />
        : (
          <pre className="config-diff">
            {diff.lines.map((l, i) => {
              const ch = String.fromCharCode(l.op)
              const cls = ch === '+' ? 'diff-add' : ch === '-' ? 'diff-del' : 'diff-ctx'
              return <div key={i} className={cls}>{ch === ' ' ? ' ' : ch} {l.text}</div>
            })}
          </pre>
        )}
    </Panel>
  )
}
