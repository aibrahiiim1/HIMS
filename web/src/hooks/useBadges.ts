import { useQuery } from '@tanstack/react-query'
import { api, type DiscoveryJob } from '../api'
import type { BadgeKey } from '../nav'

interface CountRow { category?: string; status?: string; count: number }
interface DashboardData {
  by_category?: CountRow[]
  headline?: {
    open_work_orders?: number
    open_alerts?: number
  }
}

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

  const headline = dash.data?.headline ?? {}
  const unknown = (dash.data?.by_category ?? []).find((r) => r.category === 'unknown')?.count
  const failed = (jobs.data ?? []).filter((j) => {
    const s = (j.status || '').toLowerCase()
    return s === 'failed' || s === 'error'
  }).length

  return {
    alerts: headline.open_alerts,
    work_orders: headline.open_work_orders,
    unknown,
    failed_scans: failed || undefined,
  }
}
