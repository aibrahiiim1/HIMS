import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import cytoscape, { type Core } from 'cytoscape'
import { Network, Cable, RefreshCw, Layers, ImageDown, FileDown } from 'lucide-react'
import { api, saveBlob, type TopologyGraph } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'
import { LAYER_COLOR, layerColor, CONF_COLOR } from '../components/topologyColors'
import { topologyPngBlob, topologyPdfBlob, type TopologyExportMeta } from '../lib/topologyExport'

export function TopologyPage() {
  const qc = useQueryClient()
  const [msg, setMsg] = useState('')
  const { data, isLoading, error } = useQuery({
    queryKey: ['topology-graph'],
    queryFn: () => api.get<TopologyGraph>('/topology/graph'),
  })
  const rebuild = useMutation({
    mutationFn: () => api.post<{ devices_processed: number; links_built: number; stale_removed: number }>('/topology/rebuild', {}),
    onSuccess: (d) => {
      setMsg(`Rebuilt: ${d.links_built} links across ${d.devices_processed} devices${d.stale_removed ? `, pruned ${d.stale_removed} stale` : ''}.`)
      qc.invalidateQueries({ queryKey: ['topology-graph'] })
    },
    onError: (e) => setMsg((e as Error).message),
  })
  const ref = useRef<HTMLDivElement>(null)
  const cyRef = useRef<Core | null>(null)
  const [exporting, setExporting] = useState<'' | 'png' | 'pdf'>('')

  const layers = data?.layers ?? {}
  const nodeCount = data?.nodes.length ?? 0
  const edgeCount = data?.edges.length ?? 0
  const lowConf = useMemo(() => (data?.edges ?? []).filter((e) => e.confidence === 'low').length, [data])

  // Export the ALREADY-RENDERED graph (cy.png of the live instance) — no re-layout,
  // no synthetic links. Filename: HIMS-topology-<YYYY-MM-DD-HHmm>.<ext>.
  const exportMap = async (fmt: 'png' | 'pdf') => {
    const cy = cyRef.current
    if (!cy || nodeCount === 0) return
    setExporting(fmt)
    try {
      const now = new Date()
      const pad = (n: number) => String(n).padStart(2, '0')
      const stampFile = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}`
      const meta: TopologyExportMeta = {
        title: 'HIMS — Network Topology',
        subtitle: `${nodeCount} nodes · ${edgeCount} links${lowConf ? ` · ${lowConf} low-confidence` : ''} · full map`,
        stamp: now.toLocaleString(),
        layers,
        theme: (document.documentElement.getAttribute('data-theme') as 'light' | 'dark') || 'light',
      }
      if (fmt === 'png') {
        saveBlob(await topologyPngBlob(cy, meta), `HIMS-topology-${stampFile}.png`)
      } else {
        saveBlob(await topologyPdfBlob(cy, meta), `HIMS-topology-${stampFile}.pdf`)
      }
    } catch (e) {
      setMsg(`Export failed: ${(e as Error).message}`)
    } finally {
      setExporting('')
    }
  }

  useEffect(() => {
    if (!ref.current || !data || data.nodes.length === 0) return
    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...data.nodes.map((n) => ({ data: { id: n.id, label: n.name, color: layerColor(n.layer), layer: n.layer } })),
        ...data.edges.map((e, i) => ({
          data: { id: `e${i}`, source: e.source_id, target: e.target_id, label: e.if_name ?? '' },
          classes: `conf-${e.confidence}`,
        })),
      ],
      style: [
        { selector: 'node', style: {
          'background-color': 'data(color)', label: 'data(label)', color: '#fff',
          'font-size': 10, 'text-valign': 'center', 'text-halign': 'center',
          width: 56, height: 56, 'text-wrap': 'wrap', 'text-max-width': '50px', 'font-weight': 600,
          'border-width': 3, 'border-color': '#0f172a', 'border-opacity': 0.35,
        } },
        { selector: 'edge', style: {
          width: 2.5, 'line-color': '#94a3b8', 'curve-style': 'bezier',
          label: 'data(label)', 'font-size': 8, color: '#64748b', 'target-arrow-shape': 'none',
        } },
        { selector: 'edge.conf-high', style: { 'line-color': CONF_COLOR.high, 'line-style': 'solid' } },
        { selector: 'edge.conf-medium', style: { 'line-color': CONF_COLOR.medium, 'line-style': 'dashed' } },
        { selector: 'edge.conf-low', style: { 'line-color': CONF_COLOR.low, 'line-style': 'dotted' } },
        { selector: 'node:selected', style: { 'border-color': '#111', 'border-opacity': 1, 'border-width': 4 } },
      ],
      layout: { name: 'cose', animate: false, padding: 30 },
    })
    cyRef.current = cy
    // Device neighborhood: tapping a node highlights it + its direct neighbors.
    cy.on('tap', 'node', (ev) => {
      const node = ev.target
      const hood = node.closedNeighborhood()
      cy.elements().style('opacity', 0.18)
      hood.style('opacity', 1)
    })
    cy.on('tap', (ev) => { if (ev.target === cy) cy.elements().style('opacity', 1) })
    return () => { cyRef.current = null; cy.destroy() }
  }, [data])

  const hasData = nodeCount > 0

  return (
    <div>
      <PageHeader
        title="Network Topology" icon={Network}
        subtitle="Layer-aware map from LLDP/CDP — core/distribution/access detection, link confidence, auto stale-pruning"
        actions={
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <button className="btn btn-ghost" disabled={!hasData || exporting !== ''} onClick={() => exportMap('png')}
              title="Export the full network map as a PNG image (title, timestamp + legend included)">
              <ImageDown size={15} /> {exporting === 'png' ? 'Exporting…' : 'PNG'}
            </button>
            <button className="btn btn-ghost" disabled={!hasData || exporting !== ''} onClick={() => exportMap('pdf')}
              title="Export the full network map as a PDF for sharing/reports">
              <FileDown size={15} /> {exporting === 'pdf' ? 'Exporting…' : 'PDF'}
            </button>
            <button className="btn btn-primary" disabled={rebuild.isPending} onClick={() => rebuild.mutate()}>
              <RefreshCw size={15} className={rebuild.isPending ? 'spin' : ''} /> {rebuild.isPending ? 'Rebuilding…' : 'Rebuild links'}
            </button>
          </div>
        }
      />
      {msg && <div className="enc-banner info" style={{ marginBottom: 12 }}>{msg}</div>}

      <div className="kpi-grid">
        <Kpi label="Mapped Nodes" value={nodeCount} icon={Network} tone="info" />
        <Kpi label="Links" value={edgeCount} icon={Cable} tone="default" sub={lowConf ? `${lowConf} low-confidence` : 'all corroborated'} />
        <Kpi label="Core / Distribution" value={`${layers.core ?? 0} / ${layers.distribution ?? 0}`} icon={Layers} tone="default" />
        <Kpi label="Access / Edge" value={`${layers.access ?? 0} / ${(layers.edge ?? 0) + (layers.gateway ?? 0)}`} icon={Layers} tone="default" />
      </div>

      <Panel
        title="Topology Map" icon={Network}
        subtitle="Nodes coloured by layer · edges by confidence · click a node to focus its neighborhood"
        actions={
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, fontSize: 11 }}>
            {Object.entries(LAYER_COLOR).filter(([k]) => (layers[k] ?? 0) > 0).map(([k, c]) => (
              <span key={k} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                <i style={{ width: 9, height: 9, borderRadius: 9, background: c, display: 'inline-block' }} /> {k}
              </span>
            ))}
          </div>
        }
      >
        {isLoading && <div className="loading">Loading topology…</div>}
        {error && <div className="error-msg">{(error as Error).message}</div>}
        {data && nodeCount === 0 && (
          <EmptyState icon={Network} title="No topology links yet" message="Discover switches to populate LLDP/CDP neighbors, then Rebuild links to build the map." />
        )}
        <div ref={ref} className="topology-wrap" style={{ display: hasData ? 'block' : 'none' }} />
      </Panel>
    </div>
  )
}
