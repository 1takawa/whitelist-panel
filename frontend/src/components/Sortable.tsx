import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { Icon } from './Icon'

// A list whose rows are dragged into a new order: take a row, move it, the others step aside, let go. A mouse takes the
// row anywhere but on its buttons; a finger or pen takes it by the grip (SortGrip) so the page still scrolls.
// Each row is wrapped in a div; the wrapper is what moves, so the row itself keeps its own hover and entrance effects.
// A row moved in the DOM would play its entrance animation again, so once it has played it is switched off (see index.css).

const EASE = 'cubic-bezier(0.2, 0.8, 0.2, 1)'
const reduced = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches
const INTERACTIVE = 'button, a, input, select, textarea, label, [role=switch], [role=menu], [data-nodrag]'

// The handle that shows a row can be dragged. It is also the only part a touch can pick up.
export function SortGrip({ className = '' }: { className?: string }) {
  return (
    <span data-grip className={`grid size-6 shrink-0 cursor-grab touch-none place-items-center rounded-md text-faint transition-colors hover:text-dim active:cursor-grabbing ${className}`} title="Перетащите, чтобы изменить порядок" aria-hidden>
      <Icon name="grip" className="size-4" />
    </span>
  )
}

type Drag = { id: string; pointerId: number; startX: number; startY: number; active: boolean; grab: number; lastY: number; from: string[] }

export function Sortable<T>({
  items, id, canDrag = () => true, onReorder, render, className = '',
}: {
  items: T[]
  id: (t: T) => string
  canDrag?: (t: T) => boolean
  onReorder: (ids: string[]) => void
  render: (t: T, dragging: boolean) => ReactNode
  className?: string
}) {
  const ids = items.map(id)
  const key = ids.join('\n')
  const [order, setOrder] = useState(ids)
  const [dragging, setDragging] = useState('')
  const box = useRef<HTMLDivElement>(null)
  const drag = useRef<Drag | null>(null)
  const orderRef = useRef(order)
  orderRef.current = order

  // The list the parent hands over wins whenever nobody is dragging.
  useEffect(() => {
    if (!drag.current?.active) setOrder(key === '' ? [] : key.split('\n'))
  }, [key])

  const wrapper = (sid: string) => box.current?.querySelector<HTMLElement>(`[data-sid="${CSS.escape(sid)}"]`) ?? null
  const wrappers = () => Array.from(box.current?.children ?? []) as HTMLElement[]

  // Moves the dragged row under the pointer and, when it passed the middle of a neighbour, swaps their places.
  const follow = useCallback(() => {
    const d = drag.current
    if (!d || !d.active || !box.current) return
    const el = wrapper(d.id)
    if (!el) return
    const top = d.lastY - box.current.getBoundingClientRect().top - d.grab
    const centre = top + el.offsetHeight / 2
    const cur = orderRef.current
    const others = cur.filter((x) => x !== d.id)
    let want = 0
    for (const o of others) {
      const w = wrapper(o)
      if (w && w.offsetTop + w.offsetHeight / 2 < centre) want++
    }
    if (want !== cur.indexOf(d.id)) {
      const before = new Map(wrappers().map((w) => [w, w.offsetTop]))
      const next = [...others.slice(0, want), d.id, ...others.slice(want)]
      flushSync(() => setOrder(next))
      if (!reduced()) {
        for (const w of wrappers()) {
          if (w === el) continue
          const delta = (before.get(w) ?? w.offsetTop) - w.offsetTop
          if (delta !== 0) w.animate([{ transform: `translateY(${delta}px)` }, { transform: 'translateY(0)' }], { duration: 240, easing: EASE })
        }
      }
    }
    el.style.transform = `translateY(${top - el.offsetTop}px)`
  }, [])

  // The handlers live in a ref and the listeners are made once, so the ones added at the press are the very ones removed at the release.
  const handlers = useRef({ move: (_: PointerEvent) => {}, up: (_: PointerEvent) => {}, cancel: (_: PointerEvent) => {}, scroll: () => {} })
  const [onMove, onUp, onCancel, onScroll] = useMemo(
    () => [(e: PointerEvent) => handlers.current.move(e), (e: PointerEvent) => handlers.current.up(e), (e: PointerEvent) => handlers.current.cancel(e), () => handlers.current.scroll()] as const,
    [],
  )
  const finish = useCallback((cancel: boolean) => {
    const d = drag.current
    drag.current = null
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', onCancel)
    window.removeEventListener('scroll', onScroll, true)
    if (!d?.active) return
    document.body.style.userSelect = ''
    document.body.style.cursor = ''
    const el = wrapper(d.id)
    if (el) {
      const from = new DOMMatrixReadOnly(getComputedStyle(el).transform).m42
      el.style.transform = ''
      el.style.zIndex = ''
      el.style.scale = ''
      el.style.filter = ''
      if (!reduced() && from !== 0) el.animate([{ transform: `translateY(${from}px)` }, { transform: 'translateY(0)' }], { duration: 220, easing: EASE })
    }
    setDragging('')
    if (cancel) {
      setOrder(d.from)
      return
    }
    const done = orderRef.current
    if (done.join('\n') !== d.from.join('\n')) onReorder(done)
  }, [onReorder, onMove, onUp, onCancel, onScroll])

  handlers.current = {
    move: (e) => {
      const d = drag.current
      if (!d || e.pointerId !== d.pointerId) return
      d.lastY = e.clientY
      if (!d.active) {
        if (Math.hypot(e.clientX - d.startX, e.clientY - d.startY) < 5) return
        const el = wrapper(d.id)
        if (!el) return
        d.active = true
        d.grab = d.startY - el.getBoundingClientRect().top // where on the row it was taken
        d.from = orderRef.current
        el.style.zIndex = '30'
        el.style.scale = '1.012'
        el.style.filter = 'drop-shadow(0 14px 22px rgb(0 0 0 / 0.16))'
        document.body.style.userSelect = 'none'
        document.body.style.cursor = 'grabbing'
        setDragging(d.id)
      }
      e.preventDefault()
      follow()
    },
    up: (e) => {
      if (drag.current && e.pointerId === drag.current.pointerId) finish(false)
    },
    cancel: (e) => {
      if (drag.current && e.pointerId === drag.current.pointerId) finish(true)
    },
    scroll: () => follow(),
  }

  useEffect(() => () => {
    document.body.style.userSelect = ''
    document.body.style.cursor = ''
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', onCancel)
    window.removeEventListener('scroll', onScroll, true)
  }, [onMove, onUp, onCancel, onScroll])

  function press(e: React.PointerEvent, sid: string, item: T) {
    if (drag.current || e.button !== 0 || !canDrag(item)) return
    const target = e.target as HTMLElement
    if (target.closest(INTERACTIVE)) return
    if (e.pointerType !== 'mouse' && !target.closest('[data-grip]')) return
    drag.current = { id: sid, pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, active: false, grab: 0, lastY: e.clientY, from: orderRef.current }
    window.addEventListener('pointermove', onMove, { passive: false })
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onCancel)
    window.addEventListener('scroll', onScroll, true)
  }

  // The entrance animation is over a moment after a row appears even if its end event never came (a page in a hidden tab).
  useEffect(() => {
    const t = setTimeout(() => wrappers().forEach((w) => { w.dataset.settled = '1' }), 1500)
    return () => clearTimeout(t)
  }, [key])

  const byID = new Map(items.map((t) => [id(t), t]))
  const shown = [...order.filter((x) => byID.has(x)), ...ids.filter((x) => !order.includes(x))]
  return (
    <div ref={box} className={`relative grid gap-2.5 ${className}`}>
      {shown.map((sid) => {
        const item = byID.get(sid) as T
        const style: CSSProperties = canDrag(item) ? { cursor: dragging === sid ? 'grabbing' : 'grab' } : {}
        return (
          <div key={sid} data-sid={sid} style={style} onPointerDown={(e) => press(e, sid, item)} onAnimationEnd={(e) => { if (e.target === e.currentTarget.firstElementChild) e.currentTarget.dataset.settled = '1' }} className="touch-pan-y">
            {render(item, dragging === sid)}
          </div>
        )
      })}
    </div>
  )
}
