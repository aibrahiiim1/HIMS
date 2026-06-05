import { useParams } from 'react-router-dom'
import { Laptop } from 'lucide-react'
import { DeviceHeader } from '../components/DeviceHeader'
import { ClassificationCard } from '../components/ClassificationCard'
import { DeepOSInventory } from '../components/DeepOSInventory'
import { DeviceOps } from '../components/DeviceOps'
import { DeviceCredentialHealth } from '../components/DeviceCredentialHealth'

// EndpointDetail is the template for user computers / workstations (category
// "endpoint"). Unlike the switch template it shows NO ports/VLANs/MAC tables —
// a workstation's relevant data is its deep OS inventory (OS, hardware, disks,
// services, software, roles) plus classification and operations.
export function EndpointDetail() {
  const { id } = useParams<{ id: string }>()
  const deviceId = id ?? ''
  return (
    <div>
      <DeviceHeader deviceId={deviceId} icon={Laptop} />
      <div style={{ marginBottom: 16 }}><ClassificationCard deviceId={deviceId} /></div>
      <DeepOSInventory deviceId={deviceId} alwaysShow />
      <DeviceCredentialHealth deviceId={deviceId} category="endpoint" />
      <DeviceOps deviceId={deviceId} />
    </div>
  )
}
