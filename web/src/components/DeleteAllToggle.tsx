import { Trash2 } from 'lucide-react'
import { useDeleteAllArmed } from '../lib/dangerMode'

// Shared "Delete all" control for the Inventory page and every device-category
// list. The destructive button is GATED behind a single browser-wide switch in
// System Settings → Destructive Actions: when that's off this renders nothing,
// so the button can't be hit by accident; when it's on the button appears on
// every list at once. (The arm/disarm toggle used to live inline on each page;
// it now lives in Settings so one switch controls them all.)
//
// Confirmation gating lives here so every surface enforces the same guardrail:
//   - wiping the ENTIRE unfiltered inventory requires typing DELETE
//   - deleting a scoped/filtered subset (a category, a filter) asks a confirm
export function DeleteAllToggle({ ids, fullInventory, scope, onDelete, busy }: {
  ids: string[]            // every id in the current view (all pages), to delete
  fullInventory: boolean   // true only when this is the whole, unfiltered inventory
  scope: string            // human label, e.g. "matching the current filter" / "switches"
  onDelete: (ids: string[]) => void
  busy?: boolean
}) {
  const armed = useDeleteAllArmed()
  if (!armed) return null // hidden unless enabled in Settings → Destructive Actions

  const run = () => {
    if (ids.length === 0) return
    if (fullInventory) {
      const typed = prompt(`This permanently deletes ALL ${ids.length} device(s) in the entire inventory, including their collected inventory. This cannot be undone.\n\nType DELETE to confirm:`)
      if (typed !== 'DELETE') return
    } else if (!confirm(`Delete all ${ids.length} device(s) (${scope})? This also removes their collected inventory and cannot be undone.`)) {
      return
    }
    onDelete(ids)
  }

  return (
    <button className="btn btn-danger btn-sm" disabled={ids.length === 0 || busy} onClick={run}
      title={`Delete every device in this view (${ids.length}), not just the visible page`}>
      <Trash2 size={14} /> Delete all{ids.length > 0 ? ` (${ids.length})` : ''}
    </button>
  )
}
