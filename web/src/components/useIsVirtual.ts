import { useQuery } from '@tanstack/react-query'
import { api, type Device } from '../api'

// useIsVirtual reads the shared ['devices','all'] cache (the same key DeviceHeader
// uses, so react-query dedupes — no extra request) to tell a category detail page
// whether the device is a manually-modeled virtual device. Detail pages use this to
// suppress collection-remediation prompts ("bind a credential", "re-scan", "collect
// via REST/XML") that make no sense for manual records, and to show a short note
// instead. Real discovered devices (is_virtual=false) keep their normal banners.
export function useIsVirtual(id?: string): boolean {
  const q = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })
  return !!(q.data ?? []).find((d) => d.id === id)?.is_virtual
}
