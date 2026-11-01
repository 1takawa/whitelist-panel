import { useEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { copyText } from '../lib'
import { Icon, type IconName } from './Icon'

const field =
  'w-full rounded-[10px] border border-line-strong bg-surface px-3 py-2 text-sm transition-[border-color,box-shadow] placeholder:text-faint hover:border-faint focus:border-brand focus:shadow-[0_0_0_3px_var(--brand-soft)] focus:outline-none disabled:opacity-60'

export function Button({
  variant = 'default',
  className = '',
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'primary' | 'danger' | 'ghost' | 'warn' }) {
  const styles = {
    default: 'border border-line-strong bg-surface hover:bg-surface-2',
    primary: 'border border-accent bg-accent text-accent-ink hover:opacity-85',
    danger: 'border border-line-strong bg-surface text-blocked hover:bg-surface-2',
    ghost: 'text-dim hover:bg-surface-2 hover:text-ink',
    warn: 'border border-expired bg-expired text-white hover:opacity-90',
  }[variant]
  return (
    <button
      type="button"
      {...rest}
      className={`inline-flex items-center justify-center gap-1.5 rounded-[10px] px-3.5 py-1.5 text-sm font-medium disabled:opacity-50 ${styles} ${className}`}
    />
  )
}

// A square icon button; the label is both the accessible name and the tooltip, so no text is needed next to it.
export function IconButton({ icon, label, className = '', tone = 'default', ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { icon: IconName; label: string; tone?: 'default' | 'danger' }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      {...rest}
      className={`grid size-8 place-items-center rounded-[9px] text-dim hover:bg-surface-2 disabled:opacity-40 ${tone === 'danger' ? 'hover:text-blocked' : 'hover:text-ink'} ${className}`}
    >
      <Icon name={icon} />
    </button>
  )
}

// The little (i) that shows an explanation on hover or focus, so a field does not need a paragraph under it.
export function Hint({ text }: { text: string }) {
  return (
    <span className="ap-hint relative inline-grid cursor-help place-items-center text-faint hover:text-dim focus-visible:text-dim" tabIndex={0} aria-label={text}>
      <Icon name="info" className="size-[15px]" />
      <span role="tooltip" className="ap-hint-pop absolute bottom-[calc(100%+8px)] left-1/2 z-[60] w-max max-w-60 origin-bottom rounded-[9px] bg-ink px-2.5 py-1.5 text-xs font-normal leading-snug text-accent-ink">
        {text}
      </span>
    </span>
  )
}

// The label wraps the control, so it is associated with it without ids.
export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
        {label}
        {hint && <Hint text={hint} />}
      </span>
      {children}
    </label>
  )
}

export function Input(props: React.ComponentProps<'input'>) {
  return <input {...props} className={`${field} ${props.className ?? ''}`} />
}

export function Textarea(props: React.ComponentProps<'textarea'>) {
  return <textarea rows={4} {...props} className={`${field} font-mono text-xs ${props.className ?? ''}`} />
}

export function Select({ children, className = '', ...rest }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select {...rest} className={`${field} cursor-pointer appearance-none bg-[length:12px] bg-[right_10px_center] bg-no-repeat pr-8 ${className}`} style={{ backgroundImage: chevron }}>
      {children}
    </select>
  )
}

const chevron = `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%238f939b' stroke-width='2.5' stroke-linecap='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E")`

// A real checkbox (keyboard, forms, screen readers) drawn as a switch.
export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <label className="relative inline-flex cursor-pointer items-center gap-2.5 text-sm">
      <input type="checkbox" role="switch" checked={checked} onChange={(e) => onChange(e.target.checked)} className="peer sr-only" />
      <span className="relative h-[22px] w-[38px] shrink-0 rounded-full bg-line-strong transition-colors duration-200 peer-checked:bg-online peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-brand [&::after]:absolute [&::after]:left-0.5 [&::after]:top-0.5 [&::after]:size-[18px] [&::after]:rounded-full [&::after]:bg-white [&::after]:shadow-[0_1px_3px_rgba(0,0,0,.3)] [&::after]:transition-transform [&::after]:duration-300 [&::after]:ease-[var(--ease)] [&::after]:content-[''] peer-checked:[&::after]:translate-x-4" />
      {label}
    </label>
  )
}

// A switch that is not wrapped in a label (a row of a list); the label is its accessible name.
export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative h-[22px] w-[38px] shrink-0 rounded-full transition-colors duration-200 ${checked ? 'bg-online' : 'bg-line-strong'} [&::after]:absolute [&::after]:left-0.5 [&::after]:top-0.5 [&::after]:size-[18px] [&::after]:rounded-full [&::after]:bg-white [&::after]:shadow-[0_1px_3px_rgba(0,0,0,.3)] [&::after]:transition-transform [&::after]:duration-300 [&::after]:ease-[var(--ease)] [&::after]:content-[''] ${checked ? '[&::after]:translate-x-4' : ''}`}
    />
  )
}

// Comma-separated list editor that keeps what the user typed while parsing it into an array.
export function ListInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [text, setText] = useState(value.join(', '))
  useEffect(() => {
    const parsed = parse(text)
    if (parsed.join('\u0000') !== value.join('\u0000')) setText(value.join(', '))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  return (
    <Input
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value)
        onChange(parse(e.target.value))
      }}
    />
  )
}

function parse(s: string): string[] {
  return s.split(',').map((x) => x.trim()).filter(Boolean)
}

const chip = 'bg-[color-mix(in_srgb,var(--c)_13%,transparent)] text-[color:var(--c)]'

export type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'trial' | 'info'
const toneVar: Record<Tone, string> = {
  neutral: '',
  ok: '[--c:var(--st-online)]',
  warn: '[--c:var(--st-expired)]',
  bad: '[--c:var(--st-blocked)]',
  trial: '[--c:var(--st-trial)]',
  info: '[--c:var(--st-active)]',
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  const t = tone === 'neutral' ? 'bg-surface-2 text-dim' : `${toneVar[tone]} ${chip}`
  return <span className={`inline-block whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-medium ${t}`}>{children}</span>
}

// A status pill with a leading dot; online ones pulse.
export function StatusChip({ tone, children, live }: { tone: Tone; children: ReactNode; live?: boolean }) {
  return (
    <span className={`inline-flex h-[22px] items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 text-xs font-medium ${toneVar[tone]} ${tone === 'neutral' ? 'bg-surface-2 text-dim [--c:var(--text-faint)]' : chip}`}>
      <i className={`size-1.5 rounded-full bg-[var(--c)] ${live ? 'ap-pulse' : ''}`} />
      {children}
    </span>
  )
}

export function Alert({ tone = 'bad', children }: { tone?: 'bad' | 'warn' | 'ok'; children: ReactNode }) {
  const t = { bad: '[--c:var(--st-blocked)]', warn: '[--c:var(--st-expired)]', ok: '[--c:var(--st-online)]' }[tone]
  return (
    <div role="alert" className={`${t} ap-enter rounded-xl border border-[color-mix(in_srgb,var(--c)_35%,transparent)] bg-[color-mix(in_srgb,var(--c)_8%,var(--surface))] px-3.5 py-2.5 text-sm text-[color:var(--c)]`}>
      {children}
    </div>
  )
}

// Native <dialog>: focus trap, Esc and backdrop come from the browser. It rises in and leaves faster than it came.
// persistent: a click on the backdrop does not close the dialog (for forms where a stray click would lose typed data).
export function Modal({ title, onClose, children, wide, xwide, persistent }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean; xwide?: boolean; persistent?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null)
  const leaving = useRef(false)
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal()
  }, [])
  function close() {
    const d = ref.current
    if (!d || leaving.current) return
    leaving.current = true
    d.classList.add('closing')
    window.setTimeout(() => d.close(), matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 190)
  }
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onCancel={(e) => {
        e.preventDefault()
        close()
      }}
      onClick={(e) => !persistent && e.target === ref.current && close()}
      className={`ap-dialog m-auto w-[calc(100%-2rem)] ${xwide ? 'max-w-5xl' : wide ? 'max-w-3xl' : 'max-w-lg'} overflow-clip rounded-[20px] border border-line bg-surface p-0 text-ink shadow-pop`}
    >
      <div className="flex items-center justify-between px-5 pb-1 pt-4">
        <h2 className="text-[17px] font-semibold">{title}</h2>
        <IconButton icon="x" label="Закрыть" onClick={close} />
      </div>
      <div className="max-h-[75vh] overflow-y-auto px-5 pb-5 pt-3">{children}</div>
    </dialog>
  )
}

export function CopyButton({ text, label = 'Копировать', variant = 'ghost', className = '' }: { text: string; label?: string; variant?: 'ghost' | 'primary' | 'default'; className?: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant={variant}
      className={className}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        }
      }}
    >
      <Icon name={done ? 'check' : 'copy'} className="size-4" />
      {done ? 'Скопировано' : label}
    </Button>
  )
}

// A copy button that is just an icon, and shows a check mark for a moment once the text is on the clipboard.
export function CopyIcon({ text, label = 'Копировать' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <IconButton
      icon={done ? 'check' : 'copy'}
      label={done ? 'Скопировано' : label}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        }
      }}
    />
  )
}

export function PageHeader({ title, hint, children }: { title: string; hint?: string; children?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
      <h1 className="flex items-center gap-2.5 text-[26px] font-bold leading-tight tracking-[-0.03em]">
        {title}
        {hint && <Hint text={hint} />}
      </h1>
      <div className="flex items-center gap-2">{children}</div>
    </div>
  )
}

export function Card({ children, className = '', style }: { children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return <div className={`rounded-2xl border border-line bg-surface shadow-card ${className}`} style={style}>{children}</div>
}

// A pill-shaped switcher with a thumb that slides to the selected option (used by the tab strips and filters).
export function Segmented<T extends string>({ items, value, onChange, label, className = '', panels, small }: { items: { id: T; label: ReactNode; count?: number; title?: string }[]; value: T; onChange: (id: T) => void; label: string; className?: string; panels?: boolean; small?: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  // Arrow keys move between the options, Home/End jump to the first/last (roving focus).
  function onKey(e: React.KeyboardEvent, index: number) {
    const last = items.length - 1
    const next = e.key === 'ArrowRight' ? (index + 1) % items.length : e.key === 'ArrowLeft' ? (index - 1 + items.length) % items.length : e.key === 'Home' ? 0 : e.key === 'End' ? last : -1
    if (next < 0) return
    e.preventDefault()
    onChange(items[next].id)
    requestAnimationFrame(() => box.current?.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus())
  }
  const [thumb, setThumb] = useState<{ left: number; width: number } | null>(null)
  useEffect(() => {
    const place = () => {
      const el = box.current?.querySelector<HTMLElement>('[aria-selected="true"]')
      if (el) setThumb({ left: el.offsetLeft, width: el.offsetWidth })
    }
    place()
    const ro = new ResizeObserver(place)
    if (box.current) ro.observe(box.current)
    return () => ro.disconnect()
  }, [value, items])
  return (
    <div ref={box} role="tablist" aria-label={label} className={`relative inline-flex max-w-full overflow-x-auto overflow-y-hidden rounded-xl bg-surface-2 p-[3px] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden ${className}`}>
      {thumb && (
        <span
          aria-hidden="true"
          className="absolute bottom-[3px] top-[3px] rounded-[9px] bg-surface shadow-[0_1px_3px_rgba(0,0,0,.1),0_0_0_1px_var(--border)] transition-[transform,width] duration-[380ms] ease-[var(--ease)]"
          style={{ width: thumb.width, transform: `translateX(${thumb.left}px)`, left: 0 }}
        />
      )}
      {items.map((it, i) => (
        <button
          key={it.id}
          type="button" // inside a <form>: without this a tab click would submit (and save) the form
          role="tab"
          id={panels ? `tab-${it.id}` : undefined}
          aria-controls={panels ? `panel-${it.id}` : undefined}
          aria-selected={value === it.id}
          tabIndex={value === it.id ? 0 : -1}
          onKeyDown={(e) => onKey(e, i)}
          onClick={() => onChange(it.id)}
          title={it.title}
          className={`relative z-[1] flex items-center gap-1.5 whitespace-nowrap font-medium ${small ? 'h-6 rounded-[7px] px-2.5 text-xs' : 'h-[30px] rounded-[9px] px-3.5 text-sm'} ${value === it.id ? 'text-ink' : 'text-dim hover:text-ink'}`}
        >
          {it.label}
          {it.count !== undefined && <span className={`min-w-3.5 text-[11px] font-semibold tabular-nums ${value === it.id ? 'text-dim' : 'text-faint'}`}>{it.count}</span>}
        </button>
      ))}
    </div>
  )
}

export type MenuItem = { icon: IconName; label: string; onClick: () => void; danger?: boolean; disabled?: boolean }

// The "⋯" button with a small popover of rarely used actions. The popover is fixed-positioned, so a table or card that
// clips its overflow cannot cut it off; it closes on any outside click, Esc or scrolling.
export function ActionMenu({ items, label = 'Ещё' }: { items: MenuItem[]; label?: string }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null)
  useEffect(() => {
    if (!at) return
    const close = () => setAt(null)
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    addEventListener('pointerdown', close)
    addEventListener('keydown', onKey)
    addEventListener('scroll', close, true)
    return () => {
      removeEventListener('pointerdown', close)
      removeEventListener('keydown', onKey)
      removeEventListener('scroll', close, true)
    }
  }, [at])
  return (
    <>
      <IconButton
        icon="more"
        label={label}
        aria-expanded={at !== null}
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          setAt(at ? null : { x: r.right, y: r.bottom + 4 })
        }}
      />
      {at && (
        <div role="menu" onPointerDown={(e) => e.stopPropagation()} className="ap-pop glass fixed z-40 min-w-56 rounded-[14px] border border-line-strong p-1.5 shadow-pop" style={{ top: at.y, left: Math.max(8, at.x - 224) }}>
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              disabled={it.disabled}
              className={`flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left hover:bg-surface-2 disabled:opacity-40 ${it.danger ? 'text-blocked' : ''}`}
              onClick={() => {
                setAt(null)
                it.onClick()
              }}
            >
              <Icon name={it.icon} />
              {it.label}
            </button>
          ))}
        </div>
      )}
    </>
  )
}
