import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

// URL-backed UI state. Keeping list page/filter/sort/search in the query string
// means browser Back (react-router restores the previous URL) lands the operator
// exactly where they left off after visiting a device — and the view is shareable
// and survives a hard refresh. Writes use { replace: true } so tweaking a filter
// doesn't push a history entry (Back should return to the LIST, not step through
// each filter change); the push happens only when navigating to a device detail.

export function useQueryParam(name: string, dflt = ''): [string, (v: string) => void] {
  const [sp, setSp] = useSearchParams()
  const value = sp.get(name) ?? dflt
  const set = useCallback(
    (v: string) => {
      setSp(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (!v || v === dflt) next.delete(name)
          else next.set(name, v)
          return next
        },
        { replace: true },
      )
    },
    [name, dflt, setSp],
  )
  return [value, set]
}

// useQueryNum mirrors a numeric param (e.g. the 1-based page number).
export function useQueryNum(name: string, dflt = 0): [number, (v: number) => void] {
  const [s, setS] = useQueryParam(name, String(dflt))
  const n = Number.parseInt(s, 10)
  return [Number.isFinite(n) ? n : dflt, useCallback((v: number) => setS(String(v)), [setS])]
}
