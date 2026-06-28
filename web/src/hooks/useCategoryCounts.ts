import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

// useCategoryCounts fetches the {category: count} map (GET /devices/category-counts) used
// to render sidebar inventory counts that reconcile with the data-driven pages. Refetches
// on the same cadence as the badge counts so the sidebar stays consistent after scans.
export function useCategoryCounts() {
  return useQuery({
    queryKey: ['device-category-counts'],
    queryFn: () => api.get<Record<string, number>>('/devices/category-counts'),
    refetchInterval: 60_000,
    staleTime: 30_000,
  })
}
