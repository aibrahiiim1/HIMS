import type { Device } from '../api'

// Client-side CSV export for the inventory/device-list pages. We export exactly
// what the operator is looking at (the already-filtered rows), so the file
// honours the active search / category / status filters with no backend call.

type Cell = string | number | null | undefined

// RFC-4180 escaping: quote a field only when it contains a comma, quote, or
// newline, doubling any embedded quotes.
function esc(v: Cell): string {
  const s = v == null ? '' : String(v)
  return /[",\r\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s
}

// exportToCsv builds a CSV (UTF-8 with BOM so Excel reads accented vendor names
// correctly) and triggers a browser download named "<filename>-YYYY-MM-DD.csv".
export function exportToCsv(filename: string, headers: string[], rows: Cell[][]): void {
  const body = [headers, ...rows].map((r) => r.map(esc).join(',')).join('\r\n')
  const blob = new Blob(['﻿' + body], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${filename}-${new Date().toISOString().slice(0, 10)}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// Standard device column set — shared by every inventory page so exports are
// consistent regardless of which list they came from.
const DEVICE_HEADERS = [
  'Name', 'IP', 'Category', 'Vendor', 'Model', 'OS Version', 'Class', 'VLAN',
  'Location', 'Reachability', 'Management', 'Managed By', 'Status', 'Hostname',
  'Serial', 'Driver', 'Confidence %', 'Virtual',
]

export function deviceCsv(
  devices: Device[],
  opts?: { locName?: (id?: string | null) => string },
): { headers: string[]; rows: Cell[][] } {
  const rows = devices.map((d): Cell[] => {
    let loc = opts?.locName ? opts.locName(d.location_id) : (d.location_id ?? '')
    if (loc === '—') loc = '' // the UI shows an em-dash for "no location"; keep CSV blank
    return [
      d.name, d.primary_ip ?? '', d.category, d.vendor ?? '', d.model ?? '', d.os_version ?? '',
      d.device_class ?? '', d.vlan ?? '', loc, d.reachability ?? '', d.management ?? '',
      (d.managed_by ?? []).join('|'), d.status ?? '', d.hostname ?? '', d.serial ?? '',
      d.driver ?? '', typeof d.confidence_score === 'number' ? d.confidence_score : '',
      d.is_virtual ? 'yes' : '',
    ]
  })
  return { headers: DEVICE_HEADERS, rows }
}
