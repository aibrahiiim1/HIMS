import { useEffect, useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Navigate } from 'react-router-dom'
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
import { PbxDetail } from './pages/PbxDetail'
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
import { Settings } from './pages/Settings'
import { Inventory } from './pages/Inventory'
import { Locations } from './pages/Locations'
import { ComingSoon } from './pages/ComingSoon'
import { Sidebar } from './components/Sidebar'
import { Topbar } from './components/Topbar'
import { useBadges } from './hooks/useBadges'
import './App.css'

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } })

type Theme = 'light' | 'dark'

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem('nims-theme') as Theme) || 'light')
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem('nims-theme', theme)
  }, [theme])
  return [theme, () => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))]
}

function Shell() {
  const [theme, toggleTheme] = useTheme()
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('nims-rail-collapsed') === '1')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const counts = useBadges()

  useEffect(() => {
    localStorage.setItem('nims-rail-collapsed', collapsed ? '1' : '0')
  }, [collapsed])

  const shellClass =
    'app-shell' + (collapsed ? ' is-collapsed' : '') + (drawerOpen ? ' drawer-open' : '')

  return (
    <div className={shellClass}>
      <Sidebar counts={counts} onNavigate={() => setDrawerOpen(false)} />
      <div className="rail-scrim" onClick={() => setDrawerOpen(false)} />
      <Topbar
        collapsed={collapsed}
        theme={theme}
        counts={counts}
        onToggleCollapse={() => setCollapsed((v) => !v)}
        onToggleDrawer={() => setDrawerOpen((v) => !v)}
        onToggleTheme={toggleTheme}
      />
      <main className="app-main">
        <div className="app-main-inner">
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
            <Route path="/pbx" element={<DeviceList category="pbx" title="Call Managers / PBX" detailBase="/pbx" />} />
            <Route path="/pbx/:id" element={<PbxDetail />} />
            <Route path="/unknown" element={<DeviceList category="unknown" title="Unknown Devices" detailBase="/devices" />} />
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
            <Route path="/settings" element={<Settings />} />
            <Route path="/inventory" element={<Inventory />} />
            <Route path="/locations" element={<Locations />} />
            <Route path="/soon/:slug" element={<ComingSoon />} />
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Shell />
      </BrowserRouter>
    </QueryClientProvider>
  )
}
