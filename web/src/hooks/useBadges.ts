import { useQuery } from '@tanstack/react-query'
import { api, type DiscoveryJob } from '../api'
import type { BadgeKey } from '../nav'

interface DashboardData {
  headline?: {
    open_work_orders?: number
    open_alerts?: number
  }
}
// Device-derived badge counts, computed server-side so the sidebar never has to
// download the full device list. missing_classification matches the Missing
// Classification page; unmanaged matches the Unmanaged Devices page (proven-only).
interface BadgeCountsData { missing_classification?: number; unmanaged?: number }

export type BadgeCounts = Partial<Record<BadgeKey, number>>

/**
 * Sidebar badge counts. Reuses the cached /dashboard query for alerts /
 * work-orders / unknown-devices, and pulls discovery jobs for failed scans.
 * Tolerant by design: a failed fetch simply yields no badge (count 0/undefined).
 */
export function useBadges(): BadgeCounts {
  const dash = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api.get<DashboardData>('/dashboard'),
    refetchInterval: 60_000,
    retry: 0,
  })
  const jobs = useQuery({
    queryKey: ['discovery-jobs', 'badge'],
    queryFn: () => api.get<DiscoveryJob[]>('/discovery/jobs'),
    refetchInterval: 60_000,
    retry: 0,
  })
  // Device-derived badge counts (Missing Classification + Unmanaged), computed
  // server-side in one lightweight call — no full device-list download in the
  // sidebar. Each matches its page by construction (same scoped device set +
  // predicate / proven-only access map on the backend).
  const counts = useQuery({
    queryKey: ['badge-counts'],
    queryFn: () => api.get<BadgeCountsData>('/dashboard/badge-counts'),
    refetchInterval: 60_000,
    retry: 0,
  })

  const headline = dash.data?.headline ?? {}
  const failed = (jobs.data ?? []).filter((j) => {
    const s = (j.status || '').toLowerCase()
    return s === 'failed' || s === 'error'
  }).length

  return {
    alerts: headline.open_alerts,
    work_orders: headline.open_work_orders,
    unknown: counts.data?.missing_classification || undefined,
    unmanaged: counts.data?.unmanaged || undefined,
    failed_scans: failed || undefined,
  }
}
