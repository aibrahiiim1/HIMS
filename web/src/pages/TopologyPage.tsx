import { useEffect, useMemo, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import cytoscape from 'cytoscape'
import { Network, Share2, Cable, Workflow } from 'lucide-react'
import { api, type TopologyLink } from '../api'
import { PageHeader, Panel, Kpi, EmptyState } from '../components/ui'

export function TopologyPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['topology-links'],
    queryFn: () => api.get<TopologyLink[]>('/topology/links'),
  })
  const ref = useRef<HTMLDivElement>(null)

  const stats = useMemo(() => {
    const nodes = new Set<string>(), stubs = new Set<string>()
    const protos: Record<string, number> = {}
    for (const l of data ?? []) {
      nodes.add(l.local_device_id)
      const remoteID = l.remote_device_id ?? `stub:${l.remote_sys_name ?? l.remote_ip ?? 'unknown'}`
      nodes.add(remoteID)
      if (!l.remote_device_id) stubs.add(remoteID)
      protos[l.link_source] = (protos[l.link_source] ?? 0) + 1
    }
    return { nodeCount: nodes.size, stubCount: stubs.size, linkCount: (data ?? []).length, protos }
  }, [data])

  useEffect(() => {
    if (!ref.current || !data || data.length === 0) return
    const nodes = new Map<string, { id: string; label: string }>()
    const edges: { source: string; target: string; label: string }[] = []
    for (const l of data) {
      nodes.set(l.local_device_id, { id: l.local_device_id, label: l.local_device_name })
      const remoteID = l.remote_device_id ?? `stub:${l.remote_sys_name ?? l.remote_ip ?? 'unknown'}`
      if (!nodes.has(remoteID)) nodes.set(remoteID, { id: remoteID, label: l.remote_device_name ?? l.remote_sys_name ?? l.remote_ip ?? '?' })
      edges.push({ source: l.local_device_id, target: remoteID, label: l.local_if_name ?? '' })
    }
    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...[...nodes.values()].map((n) => ({ data: { id: n.id, label: n.label } })),
        ...edges.map((e, i) => ({ data: { id: `e${i}`, source: e.source, target: e.target, label: e.label } })),
      ],
      style: [
        { selector: 'node', style: {
          'background-color': '#2563eb', label: 'data(label)', color: '#fff',
          'font-size': 10, 'text-valign': 'center', 'text-halign': 'center',
          width: 58, height: 58, 'text-wrap': 'wrap', 'text-max-width': '52px', 'font-weight': 600,
          'border-width': 3, 'border-color': '#1d4ed8',
        } },
        { selector: 'node[id ^= "stub:"]', style: { 'background-color': '#94a3b8', 'border-color': '#64748b' } },
        { selector: 'edge', style: {
          width: 2, 'line-color': '#94a3b8', 'curve-style': 'bezier',
          label: 'data(label)', 'font-size': 8, color: '#64748b', 'target-arrow-shape': 'none',
        } },
      ],
      layout: { name: 'cose', animate: false, padding: 30 },
    })
    return () => cy.destroy()
  }, [data])

  const hasData = data && data.length > 0

  return (
    <div>
      <PageHeader title="Network Topology" icon={Network} subtitle="Layer-2/3 map from LLDP/CDP neighbors and MAC/ARP correlation" />

      <div className="kpi-grid">
        <Kpi label="Mapped Nodes" value={stats.nodeCount} icon={Network} tone="info" />
        <Kpi label="Links" value={stats.linkCount} icon={Cable} tone="default" />
        <Kpi label="External Neighbors" value={stats.stubCount} icon={Share2} tone="default" sub="not yet in CMDB" />
        <Kpi label="Protocols" value={Object.keys(stats.protos).length} icon={Workflow} tone="default" sub={Object.keys(stats.protos).join(', ') || '—'} />
      </div>

      <Panel
        title="Topology Map" icon={Network}
        subtitle="Grey nodes are neighbors not yet inventoried"
      >
        {isLoading && <div className="loading">Loading topology…</div>}
        {error && <div className="error-msg">{(error as Error).message}</div>}
        {data && data.length === 0 && (
          <EmptyState icon={Network} title="No topology links yet" message="Discover switches to populate LLDP/CDP neighbors and build the map." />
        )}
        <div ref={ref} className="topology-wrap" style={{ display: hasData ? 'block' : 'none' }} />
      </Panel>
    </div>
  )
}
