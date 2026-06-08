import { useState } from 'react'
import { Plus } from 'lucide-react'
import { DeviceList } from './DeviceList'
import { AddWirelessController } from '../components/AddWirelessController'

// WirelessControllers is the Inventory → Wireless screen: the wireless-controller
// device list plus the one-step "Add controller" action (REST/XML primary).
export function WirelessControllers() {
  const [adding, setAdding] = useState(false)
  return (
    <>
      <DeviceList
        category="wireless_controller"
        title="Wireless Controllers"
        detailBase="/wlan"
        headerExtra={
          <button className="btn btn-primary btn-sm" onClick={() => setAdding(true)}>
            <Plus size={14} /> Add controller
          </button>
        }
      />
      {adding && <AddWirelessController onClose={() => setAdding(false)} />}
    </>
  )
}
