import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { BookOpen, Search } from 'lucide-react'
import { api } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

const API_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

type Operation = { summary?: string; tags?: string[]; parameters?: { name: string }[] }
type OpenAPI = {
  info?: { title?: string; version?: string; description?: string }
  paths?: Record<string, Record<string, Operation>>
}
type Endpoint = { method: string; path: string; summary: string; tag: string; params: string[] }

const METHODS = ['get', 'post', 'put', 'patch', 'delete']
const methodCls = (m: string) => ({ get: 'badge-up', post: 'badge-info', put: 'badge-warning', patch: 'badge-warning', delete: 'badge-down' }[m] ?? 'badge-unknown')

function flatten(spec: OpenAPI): Endpoint[] {
  const out: Endpoint[] = []
  for (const [path, ops] of Object.entries(spec.paths ?? {})) {
    for (const m of METHODS) {
      const op = ops[m]
      if (!op) continue
      out.push({ method: m, path, summary: op.summary ?? '', tag: op.tags?.[0] ?? 'general', params: (op.parameters ?? []).map((p) => p.name) })
    }
  }
  return out.sort((a, b) => (a.tag === b.tag ? a.path.localeCompare(b.path) : a.tag.localeCompare(b.tag)))
}

export function ApiDocs() {
  const spec = useQuery({ queryKey: ['openapi'], queryFn: () => api.get<OpenAPI>('/openapi.json') })
  const [q, setQ] = useState('')

  const endpoints = useMemo(() => (spec.data ? flatten(spec.data) : []), [spec.data])
  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    return s ? endpoints.filter((e) => e.path.toLowerCase().includes(s) || e.summary.toLowerCase().includes(s) || e.tag.includes(s)) : endpoints
  }, [endpoints, q])

  const byTag = useMemo(() => {
    const m = new Map<string, Endpoint[]>()
    for (const e of filtered) { const a = m.get(e.tag) ?? []; a.push(e); m.set(e.tag, a) }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [filtered])

  return (
    <div>
      <PageHeader title="API Documentation" icon={BookOpen}
        subtitle="Generated from the live router — always matches the deployed endpoints"
        actions={<a className="btn btn-sm" href={`${API_BASE}/openapi.json`} target="_blank" rel="noreferrer">openapi.json</a>} />

      <div className="kpi-grid">
        <Kpi label="Endpoints" value={endpoints.length} icon={BookOpen} tone="info" />
        <Kpi label="Resource Groups" value={byTag.length || new Set(endpoints.map((e) => e.tag)).size} />
        <Kpi label="Version" value={spec.data?.info?.version ?? '—'} />
        <Kpi label="Base URL" value="/api/v1" />
      </div>

      <Panel title="Overview" icon={BookOpen}>
        <p className="muted" style={{ fontSize: 13, whiteSpace: 'pre-wrap', margin: 0 }}>{spec.data?.info?.description ?? 'Loading…'}</p>
        <div className="row" style={{ marginTop: 12, maxWidth: 420 }}>
          <span className="row" style={{ gap: 6, flex: 1 }}><Search size={14} /><input className="field" style={{ flex: 1 }} placeholder="Filter endpoints…" value={q} onChange={(e) => setQ(e.target.value)} /></span>
        </div>
      </Panel>

      {spec.isLoading && <div className="loading">Loading API spec…</div>}
      {spec.data && filtered.length === 0 && <EmptyState icon={Search} title="No matching endpoints" message="Adjust your filter." />}

      {byTag.map(([tag, eps]) => (
        <Panel key={tag} title={tag} icon={BookOpen} subtitle={`${eps.length}`} pad={false}>
          <table className="data-table">
            <thead><tr><th style={{ width: 80 }}>Method</th><th>Path</th><th>Summary</th></tr></thead>
            <tbody>
              {eps.map((e, i) => (
                <tr key={i}>
                  <td><span className={`badge ${methodCls(e.method)}`}>{e.method.toUpperCase()}</span></td>
                  <td className="mono">{e.path}</td>
                  <td className="muted">{e.summary}{e.params.length ? <span style={{ marginLeft: 6 }}>· params: {e.params.join(', ')}</span> : null}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      ))}
    </div>
  )
}
