import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Cable, ArrowUpRight } from 'lucide-react'
import { api, type DeviceConnectivity } from '../api'
import { Panel, EmptyState, timeAgo } from './ui'

// ConnectivityPanel — "which switch + port + VLAN is this device attached to?" Shown on every
// device detail page. Data comes from /devices/{id}/connectivity (topology engine: ARP→MAC→FDB).
// The best (edge) attachment is highlighted; further sightings are the same MAC seen on uplink
// trunks. Honest gap: if there is no switch evidence, it says so rather than inventing a link.
const CONF: Record<string, { label: string; cls: string }> = {
  high: { label: 'High — direct LLDP/CDP or single-port FDB', cls: 'badge-up' },
  medium: { label: 'Medium — FDB / ARP correlation', cls: 'badge-warn' },
  low: { label: 'Low — weak/ambiguous evidence', cls: 'badge-warn' },
  none: { label: 'No evidence', cls: 'badge-unknown' },
}

export function ConnectivityPanel({ deviceId }: { deviceId: string }) {
  const q = useQuery({ queryKey: ['connectivity', deviceId], queryFn: () => api.get<DeviceConnectivity>(`/devices/${deviceId}/connectivity`) })
  const d = q.data
  const conf = CONF[d?.confidence ?? 'none'] ?? CONF.none
  const ports = d?.switch_port ?? []
  const best = ports[0]

  return (
    <Panel title="Switch Connectivity" icon={Cable} subtitle={ports.length ? `${ports.length} sighting${ports.length > 1 ? 's' : ''}` : undefined}
      actions={d && <span className={'badge ' + conf.cls} title={(d.confidence_reasons ?? []).join(' · ')}>{conf.label}</span>}>
      {q.isLoading && <div className="loading">Resolving…</div>}
      {d && ports.length === 0 && (
        <EmptyState icon={Cable} title="No switch evidence" message={d.gap || 'This device has not been seen in any collected switch FDB/ARP table.'} />
      )}
      {d && best && (
        <>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 24, padding: '4px 2px 14px' }}>
            <Field label="Connected switch"><Link to={`/devices/${best.switch_id}`}>{best.switch_name}</Link>{best.switch_ip && <small className="muted mono"> ({best.switch_ip})</small>}</Field>
            <Field label="Port">{best.if_name || (best.if_index != null ? `#${best.if_index}` : '—')}{best.if_alias && <small className="muted"> · {best.if_alias}</small>}</Field>
            <Field label="VLAN">{best.untagged_vlan ?? best.vlan_id}{best.untagged_vlan_name && <small className="muted"> {best.untagged_vlan_name}</small>}{best.vlan_suspect && <small className="muted" title="FDB reported a bridge index, not a validated 802.1Q VLAN"> (unverified)</small>}</Field>
            <Field label="MAC used">{d.matched_mac ? <span className="mono">{d.matched_mac}</span> : '—'}</Field>
            <Field label="Port MAC count">{best.mac_count ?? '—'}{best.mac_count != null && best.mac_count <= 4 && <small className="muted"> (edge)</small>}</Field>
            <Field label="Last seen">{best.last_seen_at ? timeAgo(best.last_seen_at) : '—'}</Field>
            {d.arp_device_name && <Field label="ARP resolved by"><span className="muted">{d.arp_device_name}{d.arp_source && ` · ${d.arp_source}`}</span></Field>}
          </div>
          {ports.length > 1 && (
            <details>
              <summary className="muted" style={{ cursor: 'pointer', fontSize: 13 }}>Also seen on {ports.length - 1} uplink/trunk port{ports.length - 1 > 1 ? 's' : ''} (MAC propagated through the fabric)</summary>
              <table className="data-table" style={{ marginTop: 8 }}>
                <thead><tr><th>Switch</th><th>Port</th><th>VLAN</th><th>MACs on port</th><th>Last seen</th></tr></thead>
                <tbody>
                  {ports.slice(1).map((p, i) => (
                    <tr key={i}>
                      <td className="cell-name"><Link to={`/devices/${p.switch_id}`}>{p.switch_name}</Link></td>
                      <td>{p.if_name || (p.if_index != null ? `#${p.if_index}` : '—')}</td>
                      <td>{p.untagged_vlan ?? p.vlan_id}</td>
                      <td className="muted">{p.mac_count ?? '—'}</td>
                      <td className="muted">{p.last_seen_at ? timeAgo(p.last_seen_at) : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          )}
          <div style={{ marginTop: 10 }}>
            <Link to={`/path-finder?q=${encodeURIComponent(d.primary_ip || d.matched_mac || '')}`} className="btn btn-ghost btn-xs"><ArrowUpRight size={12} /> Trace full path</Link>
          </div>
        </>
      )}
    </Panel>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <div><div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.4 }}>{label}</div><div style={{ fontSize: 14, marginTop: 2 }}>{children}</div></div>
}
