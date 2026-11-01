import { useEffect, useMemo, useRef, useState } from 'react'
import { countries, flagOf } from '../countries'
import { Icon } from './Icon'

// A country field you can type into: the list narrows as you type (by name or code), Enter takes the first match.
// An empty value means "not set"; the caller says what that means through emptyLabel.
export default function CountryPicker({ value, onChange, emptyLabel = 'Не указана' }: { value: string; onChange: (code: string) => void; emptyLabel?: string }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [at, setAt] = useState<{ x: number; y: number; w: number } | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)

  const current = countries.find(([c]) => c === value)
  const list = useMemo(() => {
    const s = q.trim().toLowerCase()
    return s ? countries.filter(([c, n]) => c.toLowerCase().startsWith(s) || n.toLowerCase().includes(s)) : countries
  }, [q])

  function show() {
    const r = box.current?.getBoundingClientRect()
    if (r) setAt({ x: r.left, y: r.bottom + 4, w: r.width })
    setOpen(true)
  }
  function pick(code: string) {
    onChange(code)
    setOpen(false)
    setQ('')
  }

  useEffect(() => {
    if (!open) return
    const close = (e: Event) => {
      if (e.target instanceof Node && box.current?.contains(e.target)) return
      if (e.target instanceof Element && e.target.closest('[data-country-list]')) return
      setOpen(false)
      setQ('')
    }
    addEventListener('pointerdown', close)
    return () => removeEventListener('pointerdown', close)
  }, [open])

  return (
    <div ref={box} className="relative">
      <Icon name="search" className="pointer-events-none absolute left-3 top-[11px] size-4 text-faint" />
      <input
        ref={input}
        value={open ? q : current ? `${flagOf(current[0])} ${current[1]}` : ''}
        placeholder={open ? 'Название или код' : emptyLabel}
        onFocus={show}
        onClick={show}
        onChange={(e) => {
          setQ(e.target.value)
          if (!open) show()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            if (list[0]) pick(list[0][0])
          } else if (e.key === 'Escape' && open) {
            e.stopPropagation()
            setOpen(false)
            setQ('')
          }
        }}
        autoComplete="off"
        spellCheck={false}
        aria-label="Страна"
        className="w-full rounded-[10px] border border-line-strong bg-surface py-2 pl-9 pr-8 text-sm transition-[border-color,box-shadow] placeholder:text-faint hover:border-faint focus:border-brand focus:shadow-[0_0_0_3px_var(--brand-soft)] focus:outline-none"
      />
      {value && !open && (
        <button type="button" aria-label="Сбросить страну" onClick={() => onChange('')} className="absolute right-2 top-[7px] grid size-[26px] place-items-center rounded-md text-faint hover:bg-surface-2 hover:text-ink">
          <Icon name="x" className="size-3.5" />
        </button>
      )}
      {open && at && (
        <ul data-country-list role="listbox" className="ap-pop glass fixed z-[70] max-h-60 overflow-y-auto rounded-[14px] border border-line-strong p-1.5 shadow-pop" style={{ left: at.x, top: at.y, width: at.w, transformOrigin: 'top left' }}>
          {!q && (
            <li>
              <button type="button" role="option" aria-selected={value === ''} onClick={() => pick('')} className="flex h-9 w-full items-center rounded-lg px-2.5 text-left text-dim hover:bg-surface-2">{emptyLabel}</button>
            </li>
          )}
          {list.map(([code, name]) => (
            <li key={code}>
              <button type="button" role="option" aria-selected={value === code} onClick={() => pick(code)} className={`flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left hover:bg-surface-2 ${value === code ? 'font-semibold' : ''}`}>
                <span className="w-6 text-lg leading-none">{flagOf(code)}</span>
                <span className="flex-1 truncate">{name}</span>
                <span className="text-xs text-faint">{code}</span>
              </button>
            </li>
          ))}
          {list.length === 0 && <li className="px-2.5 py-3 text-sm text-faint">Ничего не нашлось</li>}
        </ul>
      )}
    </div>
  )
}
