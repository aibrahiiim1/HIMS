import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import cytoscape from 'cytoscape'
import type { SearchResult } from '../api'
import { roleColor, roleLabel, CONF_COLOR } from './topologyColors'

// PathGraph renders a traced path (from /search) as a left→right cytoscape chain:
// endpoint → access switch → uplinks → core, and for a Wi-Fi client it leads with
// client → AP → controller before the wired hops. Each edge is labelled with the
// port + the evidence source (LLDP/CDP/MAC/ARP/wireless) and coloured by it. Tapping
// a node that maps to a managed device opens its detail page.

// Evidence source → edge colour (mirrors link-confidence: topo > mac > arp).
function srcColor(s?: string | null): string {
  switch (s) {
    case 'lldp':
    case 'cdp':
      return CONF_COLOR.high
    case 'mac':
      return CONF_COLOR.medium
    case 'arp':
      return CONF_COLOR.low
    case 'wireless':
      return '#06b6d4'
    default:
      return '#94a3b8'
  }
}

export function PathGraph({ res, height = 340 }: { res: SearchResult; height?: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const navigate = useNavigate()

  useEffect(() => {
    if (!ref.current || res.path.length === 0) return
    const nodes = res.path.map((step, idx) => {
      const name =
        step.device_name ||
        step.ip ||
        (idx === 0 ? res.mac || res.query : roleLabel(step.role))
      return {
        data: {
          id: `n${idx}`,
          label: `${name}\n(${roleLabel(step.role)})`,
          color: roleColor(step.role),
          deviceId: step.device_id ?? '',
        },
        position: { x: idx * 200, y: 120 },
      }
    })
    const edges = res.path.slice(1).map((step, i) => {
      const via = step.if_name ? ` · ${step.if_name}` : ''
      const label = `${(step.source ?? '').toUpperCase()}${via}`
      return {
        data: {
          id: `e${i}`,
          source: `n${i}`,
          target: `n${i + 1}`,
          label,
          color: srcColor(step.source),
        },
      }
    })

    const cy = cytoscape({
      container: ref.current,
      elements: [...nodes, ...edges],
      style: [
        {
          selector: 'node',
          style: {
            'background-color': 'data(color)',
            label: 'data(label)',
            color: '#fff',
            'font-size': 10,
            'text-valign': 'center',
            'text-halign': 'center',
            'text-wrap': 'wrap',
            'text-max-width': '92px',
            'font-weight': 600,
            width: 64,
            height: 64,
            'border-width': 3,
            'border-color': '#0f172a',
            'border-opacity': 0.35,
          },
        },
        {
          selector: 'edge',
          style: {
            width: 3,
            'line-color': 'data(color)',
            'target-arrow-color': 'data(color)',
            'target-arrow-shape': 'triangle',
            'curve-style': 'bezier',
            label: 'data(label)',
            'font-size': 9,
            color: 'var(--text-muted, #64748b)',
            'text-background-color': '#0b1220',
            'text-background-opacity': 0.55,
            'text-background-padding': '2px',
          },
        },
        { selector: 'node:selected', style: { 'border-color': '#fff', 'border-opacity': 1, 'border-width': 4 } },
      ],
      layout: { name: 'preset', padding: 40, fit: true },
      userZoomingEnabled: true,
      autoungrabify: false,
    })
    cy.fit(undefined, 40)
    cy.on('tap', 'node', (ev) => {
      const id = ev.target.data('deviceId')
      if (id) navigate(`/devices/${id}`)
    })
    return () => cy.destroy()
  }, [res, navigate])

  return <div ref={ref} className="topology-wrap" style={{ height }} />
}
