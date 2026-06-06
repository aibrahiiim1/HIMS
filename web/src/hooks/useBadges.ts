import { useQuery } from '@tanstack/react-query'
import { api, type DiscoveryJob, type Device } from '../api'
import type { BadgeKey } from '../nav'
import { needsClassification } from '../lib/classify'

interface DashboardData {
  headline?: {
    open_work_orders?: number
    open_alerts?: number
  }
}
interface AccessCoverage { unmanaged_devices?: number }

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
  // Unmanaged-devices count for the Inventory → Unmanaged Devices badge. Reuses the
  // proven-only Management Access Coverage figure (unmanaged = total − proven-managed),
  // which matches the Unmanaged Devices page's `/devices?management=not_managed` list.
  const coverage = useQuery({
    queryKey: ['access-coverage'],
    queryFn: () => api.get<AccessCoverage>('/dashboard/access-coverage'),
    refetchInterval: 60_000,
    retry: 0,
  })
  // Missing-Classification count. Uses the SAME devices query + needsClassification
  // predicate as the Missing Classification page so the badge always matches the
  // page (category unknown OR vendor missing OR low confidence — NOT category-only).
  const devices = useQuery({
    queryKey: ['devices', 'all'],
    queryFn: () => api.get<Device[]>('/devices?category=all'),
    refetchInterval: 60_000,
    retry: 0,
  })

  const headline = dash.data?.headline ?? {}
  const unknown = (devices.data ?? []).filter((d) => needsClassification(d)).length
  const failed = (jobs.data ?? []).filter((j) => {
    const s = (j.status || '').toLowerCase()
    return s === 'failed' || s === 'error'
  }).length

  return {
    alerts: headline.open_alerts,
    work_orders: headline.open_work_orders,
    unknown: unknown || undefined,
    unmanaged: coverage.data?.unmanaged_devices || undefined,
    failed_scans: failed || undefined,
  }
}
