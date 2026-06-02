import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Link, useLocation } from 'react-router-dom'
import { DeviceList } from './pages/DeviceList'
import { SwitchDetail } from './pages/SwitchDetail'
import { ServerDetail } from './pages/ServerDetail'
import { FirewallDetail } from './pages/FirewallDetail'
import { TopologyPage } from './pages/TopologyPage'
import { SearchPage } from './pages/SearchPage'
import { WorkOrders } from './pages/WorkOrders'
import { Systems } from './pages/Systems'
import { Monitoring } from './pages/Monitoring'
import './App.css'

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } })

function Nav() {
  const loc = useLocation()
  const active = (path: string) => (loc.pathname === path ? { borderBottom: '2px solid #90caf9' } : {})
  return (
    <nav className="hims-nav">
      <span className="hims-logo">HIMS</span>
      <Link to="/" style={active('/')}>Switches</Link>
      <Link to="/servers" style={active('/servers')}>Servers</Link>
      <Link to="/firewalls" style={active('/firewalls')}>Firewalls</Link>
      <Link to="/topology" style={active('/topology')}>Topology</Link>
      <Link to="/monitoring" style={active('/monitoring')}>Monitoring</Link>
      <Link to="/search" style={active('/search')}>Search</Link>
      <Link to="/work-orders" style={active('/work-orders')}>Work Orders</Link>
      <Link to="/systems" style={active('/systems')}>Systems</Link>
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
            <Route path="/" element={<DeviceList category="switch" title="Switches" detailBase="/devices" />} />
            <Route path="/servers" element={<DeviceList category="server" title="Servers" detailBase="/servers" />} />
            <Route path="/firewalls" element={<DeviceList category="firewall" title="Firewalls" detailBase="/firewalls" />} />
            <Route path="/devices/:id" element={<SwitchDetail />} />
            <Route path="/servers/:id" element={<ServerDetail />} />
            <Route path="/firewalls/:id" element={<FirewallDetail />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/monitoring" element={<Monitoring />} />
            <Route path="/search" element={<SearchPage />} />
            <Route path="/work-orders" element={<WorkOrders />} />
            <Route path="/systems" element={<Systems />} />
          </Routes>
        </div>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
