import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { ChevronDown, ChevronRight, Boxes } from 'lucide-react'
import { NAV, type NavItem, type NavLeaf, type BadgeKey } from '../nav'
import type { BadgeCounts } from '../hooks/useBadges'

const BADGE_TONE: Record<BadgeKey, string> = {
  alerts: '',            // default = crit/red
  failed_scans: 'tone-warn',
  unknown: 'tone-neutral',
  unmanaged: 'tone-warn',
  work_orders: 'tone-info',
}

function Badge({ k, counts }: { k?: BadgeKey; counts: BadgeCounts }) {
  if (!k) return null
  const n = counts[k]
  if (!n || n <= 0) return null
  return <span className={`nav-badge ${BADGE_TONE[k]}`}>{n > 99 ? '99+' : n}</span>
}

function matches(pathname: string, to: string): boolean {
  if (to === '/') return pathname === '/'
  return pathname === to || pathname.startsWith(to + '/')
}

function Leaf({ leaf, counts, onNavigate }: { leaf: NavLeaf; counts: BadgeCounts; onNavigate: () => void }) {
  const Icon = leaf.icon
  return (
    <NavLink
      to={leaf.to}
      end={leaf.to === '/'}
      onClick={onNavigate}
      className={({ isActive }) => 'nav-child' + (isActive ? ' active' : '')}
    >
      {Icon && <span className="nav-ico"><Icon size={15} /></span>}
      <span className="nav-label">{leaf.label}</span>
      <Badge k={leaf.badge} counts={counts} />
    </NavLink>
  )
}

function ParentItem({ item, counts, onNavigate }: { item: NavItem; counts: BadgeCounts; onNavigate: () => void }) {
  const loc = useLocation()
  const Icon = item.icon
  const children = item.children ?? []
  const childActive = children.some((c) => matches(loc.pathname, c.to))
  const [open, setOpen] = useState(childActive)

  return (
    <div>
      <button
        type="button"
        className={'nav-item' + (open ? ' open' : '') + (childActive ? ' parent-active' : '')}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <span className="nav-ico"><Icon size={18} /></span>
        <span className="nav-label">{item.label}</span>
        <Badge k={item.badge} counts={counts} />
        <ChevronRight size={15} className="chev" />
      </button>
      {open && (
        <div className="nav-children">
          {children.map((c) => (
            <Leaf key={c.to + c.label} leaf={c} counts={counts} onNavigate={onNavigate} />
          ))}
        </div>
      )}
    </div>
  )
}

function Item({ item, counts, onNavigate }: { item: NavItem; counts: BadgeCounts; onNavigate: () => void }) {
  const Icon = item.icon

  if (item.children && item.children.length) {
    return <ParentItem item={item} counts={counts} onNavigate={onNavigate} />
  }

  return (
    <NavLink
      to={item.to ?? '#'}
      end={item.to === '/'}
      onClick={onNavigate}
      className={({ isActive }) => 'nav-item' + (isActive ? ' active' : '')}
    >
      <span className="nav-ico"><Icon size={18} /></span>
      <span className="nav-label">{item.label}</span>
      <Badge k={item.badge} counts={counts} />
    </NavLink>
  )
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  const [collapsed, setCollapsed] = useState(false)
  return (
    <div className={'nav-group' + (collapsed ? ' collapsed' : '')}>
      <button type="button" className="nav-group-header" onClick={() => setCollapsed((v) => !v)}>
        <span>{title}</span>
        <ChevronDown size={13} className="chev" />
      </button>
      {!collapsed && children}
    </div>
  )
}

export function Sidebar({ counts, onNavigate }: { counts: BadgeCounts; onNavigate: () => void }) {
  return (
    <aside className="rail">
      <div className="rail-brand">
        <span className="rail-brand-mark"><Boxes size={18} /></span>
        <span className="rail-brand-text">
          <b>NIMS</b>
          <span>Network Inventory</span>
        </span>
      </div>
      <nav className="rail-scroll">
        {NAV.map((g) => (
          <Group key={g.title} title={g.title}>
            {g.items.map((it) => (
              <Item key={it.label} item={it} counts={counts} onNavigate={onNavigate} />
            ))}
          </Group>
        ))}
      </nav>
    </aside>
  )
}
