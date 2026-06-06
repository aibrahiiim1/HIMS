import type { Device } from '../api'

// Missing Classification = HIMS does not yet KNOW WHAT the device is (category /
// vendor / model / weak-low-confidence evidence). This predicate is the single
// source of truth, shared by the Missing Classification page AND the sidebar
// badge so the count always matches (a device with a known category but no vendor,
// or low confidence, still counts — which is why the badge must not key off
// category='unknown' alone).
export const LOW_CONFIDENCE = 50

export function needsClassification(d: Device): { why: string[] } | null {
  const why: string[] = []
  if (!d.category || d.category === 'unknown') why.push('category unknown')
  if (!d.vendor || !d.vendor.trim()) why.push('vendor missing')
  if (typeof d.confidence_score === 'number' && d.confidence_score > 0 && d.confidence_score < LOW_CONFIDENCE && !d.classification_locked) {
    why.push(`low confidence ${d.confidence_score}%`)
  }
  return why.length ? { why } : null
}
