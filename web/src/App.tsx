import { useEffect, useState } from 'react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Navigate } from 'react-router-dom'
import { api, setUnauthorizedHandler, type AuthMe } from './api'
import { Login } from './pages/Login'
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
import { Mibs } from './pages/Mibs'
import { Settings } from './pages/Settings'
import { Inventory } from './pages/Inventory'
import { Locations } from './pages/Locations'
import { Reports } from './pages/Reports'
import { DeviceIntelligence } from './pages/DeviceIntelligence'
import { AccessControl } from './pages/AccessControl'
import { DeviceTemplates } from './pages/DeviceTemplates'
import { VendorFingerprints } from './pages/VendorFingerprints'
import { AuditLog } from './pages/AuditLog'
import { Encryption } from './pages/Encryption'
import { SystemHealth } from './pages/SystemHealth'
import { NocWallboard } from './pages/NocWallboard'
import { PathFinder } from './pages/PathFinder'
import { DataQuality } from './pages/DataQuality'
import { Notifications } from './pages/Notifications'
import { ConfigBackups } from './pages/ConfigBackups'
import { NetFlow } from './pages/NetFlow'
import { AssetLifecycle } from './pages/AssetLifecycle'
import { MultiSite } from './pages/MultiSite'
import { BackupRestore } from './pages/BackupRestore'
import { ApiDocs } from './pages/ApiDocs'
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

function Shell({ me, onLogout }: { me?: AuthMe; onLogout: () => void }) {
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
        username={me?.username}
        onToggleCollapse={() => setCollapsed((v) => !v)}
        onToggleDrawer={() => setDrawerOpen((v) => !v)}
        onToggleTheme={toggleTheme}
        onLogout={onLogout}
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
            <Route path="/workstations" element={<DeviceList category="endpoint" title="Workstations" detailBase="/devices" />} />
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
            <Route path="/device-intelligence" element={<DeviceIntelligence />} />
            <Route path="/access-control" element={<AccessControl />} />
            <Route path="/access-control/:tab" element={<AccessControl />} />
            <Route path="/device-templates" element={<DeviceTemplates />} />
            <Route path="/vendor-fingerprints" element={<VendorFingerprints />} />
            <Route path="/audit-log" element={<AuditLog />} />
            <Route path="/security/encryption" element={<Encryption />} />
            <Route path="/system-health" element={<SystemHealth />} />
            <Route path="/noc" element={<NocWallboard />} />
            <Route path="/path-finder" element={<PathFinder />} />
            <Route path="/data-quality" element={<DataQuality />} />
            <Route path="/notifications" element={<Notifications />} />
            <Route path="/config-backups" element={<ConfigBackups />} />
            <Route path="/netflow" element={<NetFlow />} />
            <Route path="/assets" element={<AssetLifecycle />} />
            <Route path="/sites" element={<MultiSite />} />
            <Route path="/backup-restore" element={<BackupRestore />} />
            <Route path="/api-docs" element={<ApiDocs />} />
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
            <Route path="/reports" element={<Reports />} />
            <Route path="/reports/:view" element={<Reports />} />
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}

// AuthGate resolves the current session before rendering the app. While auth is
// inactive (fresh install, open mode) it renders the app directly; once auth is
// active it requires a valid session and otherwise shows the login screen.
function AuthGate() {
  const me = useQuery({ queryKey: ['me'], queryFn: () => api.get<AuthMe>('/auth/me'), retry: false })
  useEffect(() => {
    setUnauthorizedHandler(() => qc.setQueryData(['me'], { authenticated: false, auth_active: true } as AuthMe))
  }, [])

  if (me.isLoading) {
    return <div className="login-screen"><div className="loading">Loading…</div></div>
  }
  const data = me.data
  const needsLogin = me.isError || (data ? data.auth_active && !data.authenticated : true)
  if (needsLogin) {
    return <Login onSuccess={() => qc.invalidateQueries({ queryKey: ['me'] })} />
  }
  const logout = async () => {
    try { await api.post('/auth/logout', {}) } catch { /* ignore */ }
    qc.setQueryData(['me'], { authenticated: false, auth_active: true } as AuthMe)
  }
  return <Shell me={data} onLogout={logout} />
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <AuthGate />
      </BrowserRouter>
    </QueryClientProvider>
  )
}
