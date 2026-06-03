import { useParams, Link } from 'react-router-dom'
import { Construction } from 'lucide-react'

function humanize(slug: string): string {
  return slug
    .split('-')
    .map((w) => (w.length <= 4 && w === w.toLowerCase() && ['noc', 'mib', 'vlan', 'lldp', 'cdp', 'pbx', 'ups', 'nvr'].includes(w) ? w.toUpperCase() : w.charAt(0).toUpperCase() + w.slice(1)))
    .join(' ')
}

export function ComingSoon() {
  const { slug = '' } = useParams()
  const title = humanize(slug)
  return (
    <div className="coming-soon">
      <div className="cs-icon"><Construction size={34} /></div>
      <span className="cs-pill">Coming soon</span>
      <h1>{title}</h1>
      <p className="muted" style={{ maxWidth: 460 }}>
        This module is part of the NIMS roadmap and isn’t available yet. The navigation entry is
        reserved so the structure stays stable as the feature lands.
      </p>
      <Link to="/dashboard" className="badge badge-lldp" style={{ textDecoration: 'none' }}>← Back to Dashboard</Link>
    </div>
  )
}
