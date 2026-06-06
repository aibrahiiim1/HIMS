import { useState } from 'react'
import { Trash2 } from 'lucide-react'

// Shared, opt-in "Delete all" control. The destructive button is HIDDEN by
// default behind an on/off switch so it can't be hit by accident; the armed
// state persists (localStorage) so an operator who turns it on keeps it on for
// the session/machine. Used by the Inventory page and every category list page.
//
// Confirmation gating lives here so every surface enforces the same guardrail:
//   - wiping the ENTIRE unfiltered inventory requires typing DELETE
//   - deleting a scoped/filtered subset (a category, a filter) asks a confirm

const LS_KEY = 'hims.deleteAll.armed'

export function DeleteAllToggle({ ids, fullInventory, scope, onDelete, busy }: {
  ids: string[]            // every id in the current view (all pages), to delete
  fullInventory: boolean   // true only when this is the whole, unfiltered inventory
  scope: string            // human label, e.g. "matching the current filter" / "switches"
  onDelete: (ids: string[]) => void
  busy?: boolean
}) {
  const [armed, setArmed] = useState(() => {
    try { return localStorage.getItem(LS_KEY) === '1' } catch { return false }
  })
  const setArmedPersist = (v: boolean) => {
    setArmed(v)
    try { localStorage.setItem(LS_KEY, v ? '1' : '0') } catch { /* private mode */ }
  }

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
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
      <label title="Show or hide the destructive Delete-all button"
        style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12, color: 'var(--text-muted)', cursor: 'pointer', userSelect: 'none' }}>
        <input type="checkbox" checked={armed} onChange={(e) => setArmedPersist(e.target.checked)} />
        Delete-all
      </label>
      {armed && (
        <button className="btn btn-danger btn-sm" disabled={ids.length === 0 || busy} onClick={run}
          title={`Delete every device in this view (${ids.length}), not just the visible page`}>
          <Trash2 size={14} /> Delete all{ids.length > 0 ? ` (${ids.length})` : ''}
        </button>
      )}
    </span>
  )
}
