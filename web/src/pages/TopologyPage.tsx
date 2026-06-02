import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import cytoscape from 'cytoscape'
import { api, type TopologyLink } from '../api'

// Builds a Cytoscape graph from topology_links. Each device is a node;
// each link is an edge. Remote endpoints not yet in the CMDB render as
// "stub" nodes keyed by their sys-name / IP.
export function TopologyPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['topology-links'],
    queryFn: () => api.get<TopologyLink[]>('/topology/links'),
  })
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!ref.current || !data) return
    const nodes = new Map<string, { id: string; label: string }>()
    const edges: { source: string; target: string; label: string }[] = []

    for (const l of data) {
      const localID = l.local_device_id
      nodes.set(localID, { id: localID, label: l.local_device_name })
      const remoteID = l.remote_device_id ?? `stub:${l.remote_sys_name ?? l.remote_ip ?? 'unknown'}`
      if (!nodes.has(remoteID)) {
        nodes.set(remoteID, { id: remoteID, label: l.remote_device_name ?? l.remote_sys_name ?? l.remote_ip ?? '?' })
      }
      edges.push({ source: localID, target: remoteID, label: l.local_if_name ?? '' })
    }

    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...[...nodes.values()].map((n) => ({ data: { id: n.id, label: n.label } })),
        ...edges.map((e, i) => ({ data: { id: `e${i}`, source: e.source, target: e.target, label: e.label } })),
      ],
      style: [
        { selector: 'node', style: {
          'background-color': '#1565c0', label: 'data(label)', color: '#fff',
          'font-size': 10, 'text-valign': 'center', 'text-halign': 'center',
          width: 60, height: 60, 'text-wrap': 'wrap', 'text-max-width': '55px',
        } },
        { selector: 'node[id ^= "stub:"]', style: { 'background-color': '#90a4ae' } },
        { selector: 'edge', style: {
          width: 2, 'line-color': '#b0bec5', 'curve-style': 'bezier',
          label: 'data(label)', 'font-size': 8, color: '#777',
          'target-arrow-shape': 'none',
        } },
      ],
      layout: { name: 'cose', animate: false, padding: 30 },
    })
    return () => cy.destroy()
  }, [data])

  return (
    <div>
      <div className="card">
        <h2>Topology</h2>
        <p className="muted">
          Links from LLDP/CDP neighbors + MAC/ARP correlation. Grey nodes are neighbors
          not yet in the inventory.
        </p>
      </div>
      <div className="card">
        {isLoading && <div className="loading">Loading topology…</div>}
        {error && <div className="error-msg">{(error as Error).message}</div>}
        {data && data.length === 0 && (
          <div className="muted">No topology links yet. Discover switches to populate LLDP/CDP neighbors.</div>
        )}
        <div ref={ref} className="topology-wrap" style={{ display: data && data.length ? 'block' : 'none' }} />
      </div>
    </div>
  )
}
