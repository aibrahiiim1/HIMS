import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Link, useLocation } from 'react-router-dom'
import { DeviceList } from './pages/DeviceList'
import { SwitchDetail } from './pages/SwitchDetail'
import { TopologyPage } from './pages/TopologyPage'
import { SearchPage } from './pages/SearchPage'
import './App.css'

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } })

function Nav() {
  const loc = useLocation()
  const active = (path: string) => loc.pathname === path ? { borderBottom: '2px solid #90caf9' } : {}
  return (
    <nav className="hims-nav">
      <span className="hims-logo">HIMS</span>
      <Link to="/" style={active('/')}>Inventory</Link>
      <Link to="/topology" style={active('/topology')}>Topology</Link>
      <Link to="/search" style={active('/search')}>Search</Link>
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
            <Route path="/" element={<DeviceList />} />
            <Route path="/devices/:id" element={<SwitchDetail />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/search" element={<SearchPage />} />
          </Routes>
        </div>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
