import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

// useOfflineCounts fetches the {category: offline_count} map (GET /devices/offline-counts) of
// devices currently OFFLINE (reachability down). Drives the red "disconnected" badge on the
// sidebar inventory items + groups. Same cadence as the other sidebar counts so it stays fresh.
export function useOfflineCounts() {
  return useQuery({
    queryKey: ['device-offline-counts'],
    queryFn: () => api.get<Record<string, number>>('/devices/offline-counts'),
    refetchInterval: 60_000,
    staleTime: 30_000,
  })
}
