import { Link } from 'react-router-dom'
import { Ghost } from 'lucide-react'

// AddVirtualButton drops an operator into the category-aware Virtual Device form
// with the type pre-selected (no type prompt) — used on each device category page.
export function AddVirtualButton({ type, label, size = 'sm' }: { type: string; label: string; size?: 'sm' | 'xs' }) {
  return (
    <Link className={`btn btn-ghost btn-${size}`} to={`/devices/virtual/new?type=${type}`} title={`Add a virtual (manually-entered) ${label.toLowerCase()}`}>
      <Ghost size={14} /> Add Virtual {label}
    </Link>
  )
}
