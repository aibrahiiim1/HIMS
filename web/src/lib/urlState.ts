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

// useSetParams returns a setter that updates SEVERAL query params in ONE
// setSearchParams call. This is required whenever a single UI action changes more
// than one param — e.g. editing the search box also resets the page. Calling
// useQueryParam's setter twice in a row does NOT work: react-router's functional
// updater reads the current render's params each time, so the second call clobbers
// the first (the classic symptom: the search box "won't type" because q is dropped
// by the page-reset call). Pass '' | null | undefined to delete a param.
export function useSetParams(): (updates: Record<string, string | number | null | undefined>) => void {
  const [, setSp] = useSearchParams()
  return useCallback(
    (updates) => {
      setSp(
        (prev) => {
          const next = new URLSearchParams(prev)
          for (const [k, v] of Object.entries(updates)) {
            if (v === null || v === undefined || v === '') next.delete(k)
            else next.set(k, String(v))
          }
          return next
        },
        { replace: true },
      )
    },
    [setSp],
  )
}
