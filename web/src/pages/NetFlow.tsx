import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Waypoints, ArrowDownUp, Network, Radio, Server } from 'lucide-react'
import { api, type FlowOverview, type FlowEntry } from '../api'
import { PageHeader, Panel, Kpi, EmptyState, BarList, colorFor } from '../components/ui'

const WINDOWS = [
  { label: '15 min', v: 15 },
  { label: '1 hour', v: 60 },
  { label: '6 hours', v: 360 },
  { label: '24 hours', v: 1440 },
]

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${u[i]}`
}

export function NetFlow() {
  const [win, setWin] = useState(60)
  const q = `?window=${win}`
  const overview = useQuery({ queryKey: ['flow-overview', win], queryFn: () => api.get<FlowOverview>('/netflow/overview' + q), refetchInterval: 30_000 })
  const talkers = useQuery({ queryKey: ['flow-talkers', win], queryFn: () => api.get<FlowEntry[]>('/netflow/top-talkers' + q), refetchInterval: 30_000 })
  const protos = useQuery({ queryKey: ['flow-protocols', win], queryFn: () => api.get<FlowEntry[]>('/netflow/protocols' + q), refetchInterval: 30_000 })
  const convos = useQuery({ queryKey: ['flow-convos', win], queryFn: () => api.get<FlowEntry[]>('/netflow/conversations' + q), refetchInterval: 30_000 })

  const ov = overview.data
  const tk = talkers.data ?? []
  const pr = protos.data ?? []
  const cv = convos.data ?? []
  const hasData = (ov?.bytes ?? 0) > 0

  return (
    <div>
      <PageHeader title="NetFlow Analytics" icon={Waypoints}
        subtitle="Top talkers, protocol mix and bandwidth from NetFlow v5 exports"
        actions={
          <div className="seg">
            {WINDOWS.map((wn) => <button key={wn.v} className={'seg-chip' + (win === wn.v ? ' active' : '')} onClick={() => setWin(wn.v)}>{wn.label}</button>)}
          </div>} />

      {ov && !ov.listening && (
        <div className="enc-banner warn" style={{ marginBottom: 12 }}>The NetFlow collector failed to bind a UDP port — flow analytics are unavailable. Set HIMS_NETFLOW_ADDR and restart.</div>
      )}
      {ov && ov.listening && (
        <div className="enc-banner info" style={{ marginBottom: 12 }}>
          Collector listening on <code>{ov.listen_addr}</code> (NetFlow v5). {ov.packets_received} export packet(s) received since start. Point your switches/routers/firewalls to export NetFlow v5 to this address.
        </div>
      )}

      <div className="kpi-grid">
        <Kpi label="Total Traffic" value={fmtBytes(ov?.bytes ?? 0)} icon={ArrowDownUp} tone="info" sub={`${WINDOWS.find((w) => w.v === win)?.label} window`} />
        <Kpi label="Packets" value={(ov?.packets ?? 0).toLocaleString()} icon={Network} />
        <Kpi label="Active Hosts" value={ov?.talkers ?? 0} icon={Server} />
        <Kpi label="Exports Received" value={ov?.packets_received ?? 0} icon={Radio} tone={(ov?.packets_received ?? 0) > 0 ? 'ok' : 'default'} />
      </div>

      {!hasData && (overview.data && talkers.data) && (
        <EmptyState icon={Waypoints} title="No flow data in this window"
          message="HIMS is listening for NetFlow v5. Configure devices to export flows to the collector address shown above; top talkers, protocols and bandwidth will appear here within a minute of the first export." />
      )}

      {hasData && (
        <>
          <div className="grid-2">
            <Panel title="Top Talkers" icon={Server} subtitle={`${tk.length} hosts`}>
              <BarList rows={tk.map((t) => ({ label: `${t.label} · ${fmtBytes(t.bytes)}`, value: t.bytes, color: colorFor(t.label) }))} />
            </Panel>
            <Panel title="Protocol Mix" icon={Network} subtitle={`${pr.length}`}>
              <BarList rows={pr.map((p) => ({ label: `${p.label} · ${fmtBytes(p.bytes)}`, value: p.bytes, color: colorFor(p.label) }))} />
            </Panel>
          </div>
          <Panel title="Top Conversations" icon={ArrowDownUp} subtitle={`${cv.length}`} pad={false}>
            <table className="data-table">
              <thead><tr><th>Conversation (src → dst)</th><th>Traffic</th><th>Packets</th></tr></thead>
              <tbody>
                {cv.map((c, i) => (
                  <tr key={i}>
                    <td className="mono">{c.label}</td>
                    <td>{fmtBytes(c.bytes)}</td>
                    <td>{c.packets.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        </>
      )}
    </div>
  )
}
