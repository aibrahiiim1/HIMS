import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Menu, PanelLeftClose, PanelLeft, Search, Bell, Sun, Moon } from 'lucide-react'
import type { BadgeCounts } from '../hooks/useBadges'

interface TopbarProps {
  collapsed: boolean
  theme: 'light' | 'dark'
  counts: BadgeCounts
  onToggleCollapse: () => void
  onToggleDrawer: () => void
  onToggleTheme: () => void
}

export function Topbar({ collapsed, theme, counts, onToggleCollapse, onToggleDrawer, onToggleTheme }: TopbarProps) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const alerts = counts.alerts ?? 0

  function submitSearch(e: React.FormEvent) {
    e.preventDefault()
    navigate('/search' + (q.trim() ? `?q=${encodeURIComponent(q.trim())}` : ''))
  }

  return (
    <header className="topbar">
      <button type="button" className="icon-btn topbar-menu-btn" onClick={onToggleDrawer} aria-label="Open menu">
        <Menu size={20} />
      </button>
      <button type="button" className="icon-btn" onClick={onToggleCollapse} aria-label="Collapse sidebar">
        {collapsed ? <PanelLeft size={20} /> : <PanelLeftClose size={20} />}
      </button>

      <form className="topbar-search" onSubmit={submitSearch}>
        <Search size={16} />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search devices, IPs, serials…"
          aria-label="Search"
        />
      </form>

      <div className="topbar-spacer" />

      <button type="button" className="icon-btn" onClick={onToggleTheme} aria-label="Toggle theme">
        {theme === 'dark' ? <Sun size={19} /> : <Moon size={19} />}
      </button>

      <button type="button" className="icon-btn" onClick={() => navigate('/alerts')} aria-label="Alerts">
        <Bell size={19} />
        {alerts > 0 && <span className="dot" />}
      </button>

      <div className="user-chip">
        <span className="user-avatar">AI</span>
        <span className="user-name">Admin</span>
      </div>
    </header>
  )
}
