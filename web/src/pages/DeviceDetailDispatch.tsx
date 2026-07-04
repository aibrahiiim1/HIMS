import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type Device } from '../api'
import { SwitchDetail } from './SwitchDetail'
import { ServerDetail } from './ServerDetail'
import { EndpointDetail } from './EndpointDetail'
import { FirewallDetail } from './FirewallDetail'
import { VirtualHostDetail } from './VirtualHostDetail'
import { CctvDetail } from './CctvDetail'
import { PrinterDetail } from './PrinterDetail'
import { UPSDetail } from './UPSDetail'
import { PbxDetail } from './PbxDetail'
import { WirelessDetail } from './WirelessDetail'
import { GenericDeviceDetail } from './GenericDeviceDetail'
import { NasDetail } from './NasDetail'
import { BmcDetail } from './BmcDetail'

// DeviceDetailDispatch is the single entry point for `/devices/:id`. It looks up
// the device's category and renders the matching template. Previously this route
// was hardwired to SwitchDetail, so EVERY device reached via `/devices/:id`
// (unknown devices, and links from Work Orders / Asset Lifecycle / Dashboard /
// Search) rendered the switch template — ports, VLANs, MACs, neighbors — even a
// bare pingable host with no classification. Dispatching by category means each
// device gets its correct template, and anything unclassified or without a
// dedicated page falls back to the neutral GenericDeviceDetail (no switch data).
export function DeviceDetailDispatch() {
  const { id } = useParams<{ id: string }>()
  // Shares the cache key DeviceHeader uses, so this is usually already resolved.
  const { data, isLoading } = useQuery({ queryKey: ['devices', 'all'], queryFn: () => api.get<Device[]>('/devices?category=all') })

  if (isLoading) return <div className="loading">Loading…</div>
  const dev = (data ?? []).find((d) => d.id === id)

  switch (dev?.category) {
    case 'server':
      return <ServerDetail />
    case 'bmc':
      // A BMC (iLO/iDRAC/Redfish) gets a DEDICATED hardware dashboard — not the generic
      // server template — presenting controller identity, SNMP-vs-Redfish health, and the
      // full authenticated Redfish inventory (CPU/memory/storage-RAID/drives/sensors).
      return <BmcDetail />
    case 'storage':
      // A NAS (QNAP/Synology/TrueNAS) gets a DEDICATED storage template — physical
      // disk bays, logical volumes, network interfaces, and live system health, all
      // collected over SNMP with an honest not-collected gate before the first pull.
      return <NasDetail />
    case 'endpoint':
      return <EndpointDetail />
    case 'firewall':
      return <FirewallDetail />
    case 'virtual_host':
      return <VirtualHostDetail />
    case 'camera':
    case 'nvr':
    case 'dvr':
      return <CctvDetail />
    case 'printer':
      return <PrinterDetail />
    case 'ups':
      return <UPSDetail />
    case 'pbx':
      return <PbxDetail />
    case 'wireless_controller':
      return <WirelessDetail />
    case 'switch':
    case 'router':
      return <SwitchDetail />
    default:
      // unknown / unclassified / any category without a dedicated page
      return <GenericDeviceDetail />
  }
}
