import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Settings as SettingsIcon, Radar, Plug, Tags, Palette, Sun, Moon } from 'lucide-react'
import { api } from '../api'
import { PageHeader, Panel } from '../components/ui'

type Settings = Record<string, number>
type Section = 'discovery' | 'collection' | 'classification' | 'appearance'

const SNMP_PRESETS = [1000, 3000, 10000]

function NumRow({ label, hint, k, form, set, suffix }: { label: string; hint: string; k: string; form: Settings; set: (k: string, v: number) => void; suffix: string }) {
  return (
    <div className="set-row">
      <div><div className="set-label">{label}</div><div className="muted" style={{ fontSize: 12 }}>{hint}</div></div>
      <div className="row" style={{ gap: 6 }}>
        <input className="field" style={{ width: 120 }} type="number" value={form[k] ?? 0} onChange={(e) => set(k, Number(e.target.value))} />
        <span className="muted" style={{ fontSize: 12 }}>{suffix}</span>
      </div>
    </div>
  )
}

export function Settings() {
  const qc = useQueryClient()
  const [section, setSection] = useState<Section>('discovery')
  const q = useQuery({ queryKey: ['settings'], queryFn: () => api.get<Settings>('/settings') })
  const [form, setForm] = useState<Settings>({})
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => { if (q.data) setForm(q.data) }, [q.data])
  const save = useMutation({ mutationFn: () => api.put<Settings>('/settings', form), onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }) })
  const set = (k: string, v: number) => setForm((f) => ({ ...f, [k]: v }))

  const SECTIONS: { key: Section; label: string; icon: typeof Radar }[] = [
    { key: 'discovery', label: 'Discovery', icon: Radar },
    { key: 'collection', label: 'Collection & Integrations', icon: Plug },
    { key: 'classification', label: 'Classification', icon: Tags },
    { key: 'appearance', label: 'Appearance', icon: Palette },
  ]

  const saveBar = (
    <div className="row" style={{ marginTop: 16 }}>
      <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{save.isPending ? 'Saving…' : 'Save settings'}</button>
      {save.isSuccess && <span className="badge badge-up">saved</span>}
      {save.error && <span className="error-msg">{(save.error as Error).message}</span>}
      {q.data && <button className="btn btn-ghost" onClick={() => setForm(q.data)}>Reset</button>}
    </div>
  )

  return (
    <div>
      <PageHeader title="System Settings" icon={SettingsIcon} subtitle="Discovery, collection, classification and appearance configuration" />
      <div className="settings-layout">
        <nav className="settings-nav">
          {SECTIONS.map((s) => (
            <button key={s.key} className={'settings-nav-item' + (section === s.key ? ' active' : '')} onClick={() => setSection(s.key)}>
              <s.icon size={16} /> {s.label}
            </button>
          ))}
        </nav>

        <div className="stack" style={{ minWidth: 0 }}>
          {section === 'discovery' && (
            <Panel title="Discovery Scan" icon={Radar} subtitle="timeouts × concurrency set how long a subnet scan takes">
              <div className="set-row">
                <div><div className="set-label">SNMP timeout</div><div className="muted" style={{ fontSize: 12 }}>per SNMP attempt during a scan</div></div>
                <div className="row" style={{ gap: 6 }}>
                  {SNMP_PRESETS.map((ms) => (
                    <button key={ms} className={'seg-chip' + ((form.snmp_timeout_ms ?? 0) === ms ? ' active' : '')} onClick={() => set('snmp_timeout_ms', ms)}>{ms / 1000}s</button>
                  ))}
                  <input className="field" style={{ width: 110 }} type="number" value={form.snmp_timeout_ms ?? 0} onChange={(e) => set('snmp_timeout_ms', Number(e.target.value))} />
                  <span className="muted" style={{ fontSize: 12 }}>ms</span>
                </div>
              </div>
              <NumRow label="TCP connect timeout" hint="per port during scan + aliveness" k="tcp_timeout_ms" form={form} set={set} suffix="ms (100–10000)" />
              <NumRow label="Scan concurrency" hint="hosts probed in parallel" k="scan_concurrency" form={form} set={set} suffix="1–64" />
              {saveBar}
            </Panel>
          )}

          {section === 'collection' && (
            <Panel title="Collection & Integrations" icon={Plug} subtitle="controller / REST / Windows collection timeouts">
              <p className="muted" style={{ fontSize: 12, marginBottom: 8 }}>HTTP covers Redfish/iDRAC, UniFi/Omada/Ruckus/Extreme, ONVIF and CUCM; WinRM covers Hyper-V.</p>
              <NumRow label="HTTP timeout" hint="Redfish / vendor REST / ONVIF / CUCM" k="http_timeout_ms" form={form} set={set} suffix="ms (1000–120000)" />
              <NumRow label="WinRM timeout" hint="Hyper-V (Windows) collection" k="winrm_timeout_ms" form={form} set={set} suffix="ms (5000–300000)" />
              {saveBar}
            </Panel>
          )}

          {section === 'classification' && (
            <>
              <LookupList kind="class" title="Device Classes" hint="Values offered in the Inventory Class dropdown (e.g. Core, Access, Production)." />
              <LookupList kind="vlan" title="VLANs" hint="Values offered in the Inventory VLAN dropdown (e.g. 10, 20, Guest)." />
            </>
          )}

          {section === 'appearance' && <AppearanceSection />}
        </div>
      </div>
    </div>
  )
}

function AppearanceSection() {
  const [theme, setTheme] = useState(() => localStorage.getItem('nims-theme') || 'light')
  const apply = (t: string) => { setTheme(t); localStorage.setItem('nims-theme', t); document.documentElement.setAttribute('data-theme', t) }
  return (
    <Panel title="Appearance" icon={Palette} subtitle="theme preference (stored on this browser)">
      <div className="row">
        <button className={'btn' + (theme === 'light' ? ' btn-primary' : '')} onClick={() => apply('light')}><Sun size={15} /> Light</button>
        <button className={'btn' + (theme === 'dark' ? ' btn-primary' : '')} onClick={() => apply('dark')}><Moon size={15} /> Dark</button>
      </div>
    </Panel>
  )
}

function LookupList({ kind, title, hint }: { kind: string; title: string; hint: string }) {
  const qc = useQueryClient()
  const [val, setVal] = useState('')
  const key = ['lookups', kind]
  const list = useQuery({ queryKey: key, queryFn: () => api.get<{ id: string; value: string }[]>(`/lookups?kind=${kind}`) })
  const refresh = () => qc.invalidateQueries({ queryKey: key })
  const add = useMutation({ mutationFn: () => api.post('/lookups', { kind, value: val.trim() }), onSuccess: () => { setVal(''); refresh() } })
  const del = useMutation({ mutationFn: (id: string) => api.del(`/lookups/${id}`), onSuccess: refresh })
  return (
    <Panel title={title} icon={Tags} subtitle={hint}>
      <div className="row" style={{ marginBottom: 10 }}>
        <input className="field" placeholder={`new ${kind}`} value={val} onChange={(e) => setVal(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && val.trim()) add.mutate() }} />
        <button className="btn btn-primary" disabled={!val.trim() || add.isPending} onClick={() => add.mutate()}>Add</button>
      </div>
      <div className="row">
        {(list.data ?? []).length === 0 && <span className="muted" style={{ fontSize: 12 }}>None yet.</span>}
        {(list.data ?? []).map((it) => (
          <span key={it.id} className="seg-chip">{it.value}<button onClick={() => del.mutate(it.id)} title="remove" style={{ background: 'none', border: 'none', color: 'var(--crit)', cursor: 'pointer', marginLeft: 4 }}>×</button></span>
        ))}
      </div>
    </Panel>
  )
}
