import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router-dom'
import { api, type DeviceFact, type PrinterSupply } from '../api'

const barColor = (pct?: number | null) =>
  pct == null ? '#666' : pct <= 10 ? '#ef5350' : pct <= 25 ? '#ffa726' : '#66bb6a'

// Printer template — marker supplies (toner/ink/drum) with level bars + the
// lifetime page count. Collected via SNMP (Printer-MIB).
export function PrinterDetail() {
  const { id } = useParams<{ id: string }>()
  const supplies = useQuery({ queryKey: ['printer-supplies', id], queryFn: () => api.get<PrinterSupply[]>(`/devices/${id}/printer-supplies`) })
  const facts = useQuery({ queryKey: ['facts', id], queryFn: () => api.get<DeviceFact[]>(`/devices/${id}/facts`) })
  const pages = (facts.data ?? []).find((f) => f.key === 'printer.page_count')?.value

  return (
    <div>
      <div className="card">
        <h2>Printer</h2>
        <dl className="kv">
          <div><dt>Lifetime pages</dt><dd>{pages ? Number(pages).toLocaleString() : '—'}</dd></div>
        </dl>
      </div>

      <div className="card">
        <h2>Supplies</h2>
        {supplies.data && supplies.data.length === 0 && (
          <div className="muted">No supplies collected. Bind an SNMP credential and collect (Printer-MIB).</div>
        )}
        {supplies.data && supplies.data.length > 0 && (
          <table>
            <thead><tr><th>Supply</th><th>Level</th><th style={{ width: 220 }}></th></tr></thead>
            <tbody>
              {supplies.data.map((s) => (
                <tr key={s.id}>
                  <td>{s.description ?? `#${s.supply_index}`}</td>
                  <td>{s.pct != null ? `${s.pct}%` : (s.level != null && s.level < 0 ? 'unknown' : '—')}</td>
                  <td>
                    <div style={{ background: '#222', borderRadius: 4, overflow: 'hidden', height: 14 }}>
                      <div style={{ width: `${s.pct ?? 0}%`, background: barColor(s.pct), height: 14 }} />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
