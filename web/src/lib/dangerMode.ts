import { useSyncExternalStore } from 'react'

// Global "armed" flag for the destructive bulk "Delete all" buttons. It used to
// be a per-page checkbox next to each Delete-all control; it's now a single
// browser-wide toggle set in System Settings → Destructive Actions. When on,
// every Delete-all button (Inventory + all device-category lists) is shown; when
// off they're all hidden. Stored in localStorage (per browser/machine) and made
// reactive via a custom event so toggling it in Settings updates every mounted
// page immediately, plus the native `storage` event for cross-tab sync.
//
// Key is unchanged from the old per-page control, so an already-armed browser
// stays armed after this move.
const LS_KEY = 'hims.deleteAll.armed'
const EVT = 'hims:deleteAllArmed'

function read(): boolean {
  try { return localStorage.getItem(LS_KEY) === '1' } catch { return false }
}

// setDeleteAllArmed persists the flag and notifies all subscribers in this tab
// (the `storage` event only fires in OTHER tabs, so we dispatch our own).
export function setDeleteAllArmed(on: boolean) {
  try { localStorage.setItem(LS_KEY, on ? '1' : '0') } catch { /* private mode */ }
  window.dispatchEvent(new Event(EVT))
}

function subscribe(cb: () => void): () => void {
  window.addEventListener(EVT, cb)
  window.addEventListener('storage', cb)
  return () => {
    window.removeEventListener(EVT, cb)
    window.removeEventListener('storage', cb)
  }
}

// useDeleteAllArmed reactively reads the global flag.
export function useDeleteAllArmed(): boolean {
  return useSyncExternalStore(subscribe, read, () => false)
}
