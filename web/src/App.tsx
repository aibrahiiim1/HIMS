import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Link, useLocation } from 'react-router-dom'
import { DeviceList } from './pages/DeviceList'
import { Dashboard } from './pages/Dashboard'
import { Discovery } from './pages/Discovery'
import { SwitchDetail } from './pages/SwitchDetail'
import { ServerDetail } from './pages/ServerDetail'
import { FirewallDetail } from './pages/FirewallDetail'
import { VirtualHostDetail } from './pages/VirtualHostDetail'
import { CctvDetail } from './pages/CctvDetail'
import { PrinterDetail } from './pages/PrinterDetail'
import { UPSDetail } from './pages/UPSDetail'
import { WirelessDetail } from './pages/WirelessDetail'
import { TopologyPage } from './pages/TopologyPage'
import { SearchPage } from './pages/SearchPage'
import { WorkOrders } from './pages/WorkOrders'
import { Systems } from './pages/Systems'
import { Monitoring } from './pages/Monitoring'
import { Alerts } from './pages/Alerts'
import { SpareParts } from './pages/SpareParts'
import { Expenses } from './pages/Expenses'
import { Credentials } from './pages/Credentials'
import { Roles } from './pages/Roles'
import { Mibs } from './pages/Mibs'
import './App.css'

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } })

function Nav() {
  const loc = useLocation()
  const active = (path: string) => (loc.pathname === path ? { borderBottom: '2px solid #90caf9' } : {})
  return (
    <nav className="hims-nav">
      <span className="hims-logo">HIMS</span>
      <Link to="/dashboard" style={active('/dashboard')}>Dashboard</Link>
      <Link to="/discovery" style={active('/discovery')}>Discovery</Link>
      <Link to="/" style={active('/')}>Switches</Link>
      <Link to="/servers" style={active('/servers')}>Servers</Link>
      <Link to="/virtual-hosts" style={active('/virtual-hosts')}>Virtual Hosts</Link>
      <Link to="/firewalls" style={active('/firewalls')}>Firewalls</Link>
      <Link to="/cameras" style={active('/cameras')}>Cameras</Link>
      <Link to="/nvrs" style={active('/nvrs')}>NVRs</Link>
      <Link to="/wlan" style={active('/wlan')}>Wireless</Link>
      <Link to="/printers" style={active('/printers')}>Printers</Link>
      <Link to="/ups" style={active('/ups')}>UPS</Link>
      <Link to="/topology" style={active('/topology')}>Topology</Link>
      <Link to="/monitoring" style={active('/monitoring')}>Monitoring</Link>
      <Link to="/alerts" style={active('/alerts')}>Alerts</Link>
      <Link to="/roles" style={active('/roles')}>Roles</Link>
      <Link to="/search" style={active('/search')}>Search</Link>
      <Link to="/work-orders" style={active('/work-orders')}>Work Orders</Link>
      <Link to="/systems" style={active('/systems')}>Systems</Link>
      <Link to="/spare-parts" style={active('/spare-parts')}>Parts</Link>
      <Link to="/expenses" style={active('/expenses')}>Expenses</Link>
      <Link to="/credentials" style={active('/credentials')}>Credentials</Link>
      <Link to="/mibs" style={active('/mibs')}>MIBs</Link>
    </nav>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Nav />
        <div className="hims-content">
          <Routes>
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/discovery" element={<Discovery />} />
            <Route path="/" element={<DeviceList category="switch" title="Switches" detailBase="/devices" />} />
            <Route path="/servers" element={<DeviceList category="server" title="Servers" detailBase="/servers" />} />
            <Route path="/firewalls" element={<DeviceList category="firewall" title="Firewalls" detailBase="/firewalls" />} />
            <Route path="/devices/:id" element={<SwitchDetail />} />
            <Route path="/servers/:id" element={<ServerDetail />} />
            <Route path="/virtual-hosts" element={<DeviceList category="virtual_host" title="Virtual Hosts" detailBase="/virtual-hosts" />} />
            <Route path="/virtual-hosts/:id" element={<VirtualHostDetail />} />
            <Route path="/firewalls/:id" element={<FirewallDetail />} />
            <Route path="/cameras" element={<DeviceList category="camera" title="Cameras" detailBase="/cctv" />} />
            <Route path="/nvrs" element={<DeviceList category="nvr" title="NVR / DVR" detailBase="/cctv" />} />
            <Route path="/cctv/:id" element={<CctvDetail />} />
            <Route path="/wlan" element={<DeviceList category="wireless_controller" title="Wireless Controllers" detailBase="/wlan" />} />
            <Route path="/wlan/:id" element={<WirelessDetail />} />
            <Route path="/printers" element={<DeviceList category="printer" title="Printers" detailBase="/printers" />} />
            <Route path="/printers/:id" element={<PrinterDetail />} />
            <Route path="/ups" element={<DeviceList category="ups" title="UPS Units" detailBase="/ups" />} />
            <Route path="/ups/:id" element={<UPSDetail />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/monitoring" element={<Monitoring />} />
            <Route path="/alerts" element={<Alerts />} />
            <Route path="/roles" element={<Roles />} />
            <Route path="/search" element={<SearchPage />} />
            <Route path="/work-orders" element={<WorkOrders />} />
            <Route path="/systems" element={<Systems />} />
            <Route path="/spare-parts" element={<SpareParts />} />
            <Route path="/expenses" element={<Expenses />} />
            <Route path="/credentials" element={<Credentials />} />
            <Route path="/mibs" element={<Mibs />} />
          </Routes>
        </div>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
