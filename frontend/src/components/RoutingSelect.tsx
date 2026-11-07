import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Icon } from './Icon'

// The routing a connection can ask the client app for. "" follows the subscription.
export const ROUTING_CHOICES: { id: string; short: string; name: string; hint: string }[] = [
  { id: '', short: 'как в подписке', name: 'Как в подписке', hint: 'Общий профиль подписки' },
  { id: 'none', short: 'без правил', name: 'Без правил', hint: 'Весь трафик идёт через сервер' },
  { id: 'default', short: 'DEFAULT', name: 'DEFAULT', hint: 'Россия и Беларусь напрямую' },
  { id: 'whitelist', short: 'WHITELIST', name: 'WHITELIST', hint: 'Напрямую только белые списки РФ' },
  { id: 'whitelist_ru', short: 'whitelist_RU', name: 'whitelist_RU', hint: 'Россия напрямую, с доменами Яндекса и VK' },
]

const WIDTH = 300

// A small drop-down for a row of a list. The list is drawn on the page itself (not inside the row) so no card clips it, and
// it opens upwards when there is no room below. Arrows move, Enter takes, Esc closes.
export default function RoutingSelect({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const [open, setOpen] = useState(false)
  const [at, setAt] = useState(0)
  const [pos, setPos] = useState<{ x: number; y: number; up: boolean } | null>(null)
  const btn = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLUListElement>(null)
  const current = ROUTING_CHOICES.find((c) => c.id === value) ?? ROUTING_CHOICES[0]

  useLayoutEffect(() => {
    if (!open || !btn.current) return
    const r = btn.current.getBoundingClientRect()
    const height = ROUTING_CHOICES.length * 52 + 14
    const up = innerHeight - r.bottom < height + 12 && r.top > height
    const x = Math.max(8, Math.min(r.right - WIDTH, innerWidth - WIDTH - 8))
    setPos({ x, y: up ? r.top - 6 : r.bottom + 6, up })
  }, [open])

  useEffect(() => {
    if (!open) return
    const close = (e: Event) => {
      if (e.target instanceof Node && (btn.current?.contains(e.target) || list.current?.contains(e.target))) return
      setOpen(false)
    }
    const away = () => setOpen(false)
    addEventListener('pointerdown', close)
    addEventListener('scroll', away, true)
    addEventListener('resize', away)
    return () => {
      removeEventListener('pointerdown', close)
      removeEventListener('scroll', away, true)
      removeEventListener('resize', away)
    }
  }, [open])

  function show() {
    setAt(Math.max(0, ROUTING_CHOICES.findIndex((c) => c.id === value)))
    setOpen(true)
  }
  function pick(id: string) {
    setOpen(false)
    btn.current?.focus()
    if (id !== value) onChange(id)
  }
  function onKey(e: React.KeyboardEvent) {
    if (!open) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        show()
      }
      return
    }
    if (e.key === 'Escape') setOpen(false)
    else if (e.key === 'ArrowDown') setAt((i) => (i + 1) % ROUTING_CHOICES.length)
    else if (e.key === 'ArrowUp') setAt((i) => (i - 1 + ROUTING_CHOICES.length) % ROUTING_CHOICES.length)
    else if (e.key === 'Enter' || e.key === ' ') pick(ROUTING_CHOICES[at].id)
    else if (e.key === 'Tab') setOpen(false)
    else return
    e.preventDefault()
  }

  return (
    <>
      <button
        ref={btn}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Маршрутизация на клиенте: ${current.name}`}
        title="Маршрутизация на клиенте: правила приложения для этого подключения (формат Xray JSON; Happ и INCY получают его сами)"
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={onKey}
        className={`flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors ${value ? 'border-line-strong bg-surface-2 text-ink' : 'border-line text-dim hover:border-line-strong hover:text-ink'}`}
      >
        <span className="text-faint">Маршрут:</span>
        <span className="font-medium">{current.short}</span>
        <Icon name="down" className={`size-3.5 transition-transform duration-300 ease-[var(--ease)] ${open ? 'rotate-180' : ''}`} />
      </button>
      {open &&
        pos &&
        createPortal(
          <ul
            ref={list}
            role="listbox"
            onKeyDown={onKey}
            style={{ position: 'fixed', left: pos.x, width: WIDTH, ...(pos.up ? { bottom: innerHeight - pos.y } : { top: pos.y }), zIndex: 60 }}
            className="ap-enter grid gap-0.5 rounded-xl border border-line bg-surface p-1.5 shadow-pop"
          >
            {ROUTING_CHOICES.map((c, i) => (
              <li key={c.id} role="option" aria-selected={c.id === value}>
                <button
                  type="button"
                  onClick={() => pick(c.id)}
                  onPointerEnter={() => setAt(i)}
                  className={`flex min-h-[3.1rem] w-full items-center gap-3 rounded-lg px-2.5 py-1.5 text-left ${i === at ? 'bg-surface-2' : ''}`}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">{c.name}</span>
                    <span className="block truncate text-xs text-dim">{c.hint}</span>
                  </span>
                  {c.id === value && <Icon name="check" className="shrink-0 text-ink" />}
                </button>
              </li>
            ))}
          </ul>,
          document.body,
        )}
    </>
  )
}
