import { Download } from 'lucide-react'
import type { Device } from '../api'
import { deviceCsv, exportToCsv } from '../lib/exportCsv'

// ExportDevicesButton — a toolbar button for every inventory / device-list page.
// It exports the rows it is handed (the page's already-filtered set), so the CSV
// reflects exactly what the operator sees. Disabled when there is nothing to export.
export function ExportDevicesButton({
  devices,
  filename,
  locName,
  label = 'Export CSV',
}: {
  devices: Device[]
  filename: string
  locName?: (id?: string | null) => string
  label?: string
}) {
  const onClick = () => {
    const { headers, rows } = deviceCsv(devices, { locName })
    exportToCsv(filename, headers, rows)
  }
  return (
    <button
      className="btn btn-sm"
      disabled={devices.length === 0}
      title={devices.length ? `Export ${devices.length} device(s) to CSV` : 'Nothing to export'}
      onClick={onClick}
    >
      <Download size={14} /> {label}
    </button>
  )
}
