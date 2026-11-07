import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { NavLink, Navigate, Outlet, useLocation } from 'react-router'
import { useAuth } from '../auth'
import { Icon, type IconName } from '../components/Icon'
import { IconButton } from '../components/ui'
import { useTheme } from '../theme'

type Item = { to: string; label: string; icon: IconName; end?: boolean }

const main: Item[] = [
  { to: '/', label: 'Обзор', icon: 'grid', end: true },
  { to: '/clients', label: 'Клиенты', icon: 'users' },
  { to: '/inbounds', label: 'Подключения', icon: 'plug' },
  { to: '/servers', label: 'Серверы', icon: 'server' },
  { to: '/cascades', label: 'Каскады', icon: 'branch' },
  { to: '/nodes', label: 'Внешние ноды', icon: 'globe' },
]
const config: Item[] = [
  { to: '/subscription', label: 'Подписка', icon: 'rss' },
  { to: '/api', label: 'API', icon: 'code' },
  { to: '/settings', label: 'Настройки', icon: 'sliders' },
]

// Pages with a wide table use more of the screen than the others.
const WIDE_PAGES = ['/clients']

export default function Shell() {
  const { user, loading, logout } = useAuth()
  const { theme, toggle } = useTheme()
  const loc = useLocation()
  const nav = useRef<HTMLElement>(null)
  const [pill, setPill] = useState<{ x: number; y: number; w: number; h: number } | null>(null)

  // The highlight under the current item slides to the next one instead of jumping.
  useLayoutEffect(() => {
    const place = () => {
      const el = nav.current?.querySelector<HTMLElement>('[aria-current="page"]')
      if (el) setPill({ x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight })
      else setPill(null)
    }
    place()
    const ro = new ResizeObserver(place)
    if (nav.current) ro.observe(nav.current)
    return () => ro.disconnect()
  }, [loc.pathname, loading, user])

  useEffect(() => {
    document.getElementById('main')?.scrollTo({ top: 0 })
  }, [loc.pathname])

  if (loading) return null
  if (!user) return <Navigate to="/login" replace />

  const link = (n: Item) => (
    <NavLink
      key={n.to}
      to={n.to}
      end={n.end}
      className={({ isActive }) =>
        `relative z-[1] flex h-9 shrink-0 items-center gap-3 rounded-[10px] px-2.5 text-sm max-[900px]:w-11 max-[900px]:justify-center max-[900px]:px-0 max-[640px]:h-11 ${isActive ? 'font-medium text-ink' : 'text-dim hover:text-ink'}`
      }
      title={n.label}
    >
      <Icon name={n.icon} />
      <span className="max-[900px]:hidden">{n.label}</span>
    </NavLink>
  )

  return (
    <div className="grid h-full grid-cols-[232px_1fr] max-[900px]:grid-cols-[64px_1fr] max-[640px]:grid-cols-1 max-[640px]:grid-rows-[1fr_auto]">
      <aside className="flex min-h-0 flex-col border-r border-line px-3 py-4 max-[900px]:items-center max-[900px]:px-2 max-[640px]:order-2 max-[640px]:flex-row max-[640px]:border-r-0 max-[640px]:border-t max-[640px]:px-2 max-[640px]:py-1.5 max-[640px]:bg-surface/85 max-[640px]:backdrop-blur-xl">
        <div className="flex items-center gap-2.5 px-2.5 pb-5 pt-1 text-[15px] font-semibold tracking-tight max-[900px]:px-0 max-[640px]:hidden">
          <span className="grid size-[26px] place-items-center rounded-lg bg-accent text-xs font-bold text-accent-ink">W</span>
          <span className="max-[900px]:hidden">Whitelist Panel</span>
        </div>

        <nav ref={nav} aria-label="Разделы" className="relative flex min-h-0 flex-col gap-0.5 overflow-y-auto max-[640px]:min-w-0 max-[640px]:flex-1 max-[640px]:flex-row max-[640px]:overflow-x-auto max-[640px]:overflow-y-hidden max-[640px]:[scrollbar-width:none]">
          {pill && (
            <span
              aria-hidden="true"
              className="absolute left-0 top-0 rounded-[10px] bg-surface shadow-[0_1px_2px_rgba(17,18,20,.05),0_0_0_1px_var(--border)] transition-[transform,width,height] duration-[380ms] ease-[var(--ease)]"
              style={{ width: pill.w, height: pill.h, transform: `translate(${pill.x}px, ${pill.y}px)` }}
            />
          )}
          {main.map(link)}
          <div className="px-2.5 pb-1.5 pt-4 text-[11px] uppercase tracking-wider text-faint max-[900px]:hidden">Настройка</div>
          <div className="hidden h-px bg-line max-[900px]:my-2 max-[900px]:block max-[640px]:hidden" />
          {config.map(link)}
        </nav>

        <div className="mt-auto flex items-center gap-2 border-t border-line px-1.5 pt-3 max-[900px]:flex-col max-[900px]:border-0 max-[640px]:mt-0 max-[640px]:shrink-0 max-[640px]:flex-row max-[640px]:gap-0 max-[640px]:p-0">
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-surface-2 text-xs font-semibold text-dim max-[640px]:hidden">{user.slice(0, 1).toUpperCase()}</span>
          <span className="min-w-0 flex-1 truncate text-[13px] font-medium max-[900px]:hidden">{user}</span>
          <IconButton icon={theme === 'dark' ? 'sun' : 'moon'} label="Сменить тему" onClick={toggle} />
          <IconButton icon="out" label="Выйти" onClick={logout} />
        </div>
      </aside>

      <main id="main" className="min-w-0 overflow-y-auto">
        <div key={loc.pathname} className={`ap-enter mx-auto px-7 pb-16 pt-7 max-[640px]:px-4 max-[640px]:pb-10 max-[640px]:pt-5 ${WIDE_PAGES.some((p) => loc.pathname === p || loc.pathname.startsWith(p + '/')) ? 'max-w-[1700px]' : 'max-w-[1180px]'}`}>
          <Outlet />
        </div>
      </main>
    </div>
  )
}
