import { useParams } from 'react-router-dom'
import { HelpCircle } from 'lucide-react'
import { DeviceHeader } from '../components/DeviceHeader'
import { ClassificationCard } from '../components/ClassificationCard'
import { DeepOSInventory } from '../components/DeepOSInventory'
import { DeviceOps } from '../components/DeviceOps'

// GenericDeviceDetail is the fallback template for devices that have no
// type-specific page — primarily "unknown" (discovered/pingable but not yet
// classified), plus other categories without a dedicated detail view. It shows
// NO switch-specific data (ports / VLANs / MAC tables / neighbors): an
// unclassified or non-switch device has none, so rendering the switch template
// for it was misleading. Instead it shows the device header, its current
// classification (with the Reclassify action so the operator can correct/assign
// it), any deep OS inventory if it happens to be a Windows/Linux host, and the
// standard device operations.
export function GenericDeviceDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={HelpCircle} />
      <div style={{ marginBottom: 16 }}><ClassificationCard deviceId={deviceId} /></div>
      {/* No alwaysShow: the OS panel surfaces only when the device actually is a
          Windows/Linux host or already has inventory — it stays hidden for a
          bare pingable device. */}
      <DeepOSInventory deviceId={deviceId} />
      <DeviceOps deviceId={deviceId} />
    </div>
  )
}
