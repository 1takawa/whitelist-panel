import { createContext, useContext, useEffect, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Field, Hint, Input, Select, Textarea, Toggle } from '../../components/ui'
import type { Inbound } from '../../types'
import { getPath } from './doc'

// The inbound form edits one Inbound object; every field of the tabs is bound to a dotted path inside it.
export type FormState = {
  d: Inbound
  set: (path: string, v: unknown) => void
  patch: (fn: (d: Inbound) => Inbound) => void
  editing: boolean
  others: Inbound[] // the other inbounds, for the fallback helper
}

export const FormCtx = createContext<FormState>(null as unknown as FormState)
export const useForm = () => useContext(FormCtx)

export function useVal<T = unknown>(path: string): [T | undefined, (v: unknown) => void] {
  const { d, set } = useForm()
  return [getPath(d, path) as T | undefined, (v) => set(path, v)]
}

type Base = { path: string; label: string; hint?: string }

export function BText({ path, label, hint, placeholder, mono, readOnly }: Base & { placeholder?: string; mono?: boolean; readOnly?: boolean }) {
  const [v, set] = useVal<string>(path)
  return (
    <Field label={label} hint={hint}>
      <Input value={v ?? ''} readOnly={readOnly} placeholder={placeholder} spellCheck={false} className={mono ? 'font-mono text-xs' : ''} onChange={(e) => set(e.target.value === '' ? undefined : e.target.value)} />
    </Field>
  )
}

export function BNum({ path, label, hint, placeholder, min, max }: Base & { placeholder?: string; min?: number; max?: number }) {
  const [v, set] = useVal<number>(path)
  return (
    <Field label={label} hint={hint}>
      <Input type="number" value={v ?? ''} min={min} max={max} placeholder={placeholder} onChange={(e) => set(e.target.value === '' ? undefined : Number(e.target.value))} />
    </Field>
  )
}

export function BBool({ path, label }: { path: string; label: string }) {
  const [v, set] = useVal<boolean>(path)
  return <Toggle label={label} checked={v === true} onChange={(c) => set(c ? true : undefined)} />
}

export function BSel({ path, label, hint, options, empty }: Base & { options: Array<[string, string]>; empty?: string }) {
  const [v, set] = useVal<string>(path)
  return (
    <Field label={label} hint={hint}>
      <Select value={v ?? ''} onChange={(e) => set(e.target.value === '' ? undefined : e.target.value)}>
        {empty !== undefined && <option value="">{empty}</option>}
        {options.map(([val, text]) => <option key={val} value={val}>{text}</option>)}
      </Select>
    </Field>
  )
}

// Chips: type a value and press Enter or comma; Backspace on an empty box removes the last one.
export function Tags({ value, onChange, placeholder, suggestions }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string; suggestions?: string[] }) {
  const [text, setText] = useState('')
  const add = (raw: string) => {
    const parts = raw.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean)
    if (parts.length) onChange([...value, ...parts.filter((p) => !value.includes(p))])
    setText('')
  }
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      add(text)
    } else if (e.key === 'Backspace' && text === '' && value.length) {
      onChange(value.slice(0, -1))
    }
  }
  const free = (suggestions ?? []).filter((s) => !value.includes(s))
  return (
    <div>
      <div className="flex min-h-[2.5rem] flex-wrap items-center gap-1.5 rounded-[10px] border border-line-strong bg-surface px-2 py-1.5 focus-within:border-brand focus-within:shadow-[0_0_0_3px_var(--brand-soft)]">
        {value.map((t) => (
          <span key={t} className="inline-flex items-center gap-1 rounded-md bg-surface-2 px-2 py-0.5 font-mono text-xs">
            {t}
            <button type="button" aria-label={`Убрать ${t}`} className="text-dim hover:text-danger" onClick={() => onChange(value.filter((x) => x !== t))}>×</button>
          </span>
        ))}
        <input
          value={text}
          placeholder={value.length ? '' : placeholder}
          spellCheck={false}
          className="min-w-[8rem] flex-1 bg-transparent px-1 py-0.5 text-sm outline-none"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKey}
          onBlur={() => text && add(text)}
        />
      </div>
      {free.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-1">
          {free.map((s) => (
            <button key={s} type="button" className="rounded-md border border-line px-1.5 py-0.5 text-xs text-dim hover:bg-surface-2" onClick={() => onChange([...value, s])}>+ {s}</button>
          ))}
        </div>
      )}
    </div>
  )
}

export function BTags({ path, label, hint, placeholder, suggestions }: Base & { placeholder?: string; suggestions?: string[] }) {
  const [v, set] = useVal<string[]>(path)
  return (
    <div>
      <span className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">{label}{hint && <Hint text={hint} />}</span>
      <Tags value={v ?? []} placeholder={placeholder} suggestions={suggestions} onChange={(x) => set(x.length ? x : undefined)} />
    </div>
  )
}

// Key-order independent form of a JSON value, for comparing "the same document".
function canon(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canon).join(',')}]`
  if (v !== null && typeof v === 'object') {
    return `{${Object.keys(v).sort().map((k) => JSON.stringify(k) + ':' + canon((v as Record<string, unknown>)[k])).join(',')}}`
  }
  return JSON.stringify(v) ?? 'undefined'
}

// A JSON value edited as text: it is stored as soon as it parses, and the text is kept while it does not.
export function JsonText({ value, onChange, rows = 8, label, hint }: { value: unknown; onChange: (v: unknown) => void; rows?: number; label?: string; hint?: string }) {
  const [text, setText] = useState(() => (value === undefined ? '' : JSON.stringify(value, null, 2)))
  const [err, setErr] = useState('')
  useEffect(() => {
    // An outside change (another tab, a generator) replaces the text unless it says the same thing; text that does
    // not parse is the user's work in progress and is left alone.
    if (text.trim() === '') {
      if (value === undefined) return
    } else {
      try {
        if (canon(JSON.parse(text)) === canon(value)) return
      } catch {
        return
      }
    }
    setText(value === undefined ? '' : JSON.stringify(value, null, 2))
    setErr('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const body = (
    <>
      <Textarea
        rows={rows}
        value={text}
        spellCheck={false}
        aria-invalid={err !== ''}
        className={err ? 'border-danger' : ''}
        onChange={(e) => {
          const t = e.target.value
          setText(t)
          if (t.trim() === '') {
            setErr('')
            onChange(undefined)
            return
          }
          try {
            onChange(JSON.parse(t))
            setErr('')
          } catch (x) {
            setErr(x instanceof Error ? x.message : 'Некорректный JSON')
          }
        }}
      />
      {err && <span className="mt-1 block text-xs text-danger">{err}</span>}
    </>
  )
  return label ? <Field label={label} hint={hint}>{body}</Field> : <div>{body}</div>
}

export function BJson({ path, label, hint, rows }: Base & { rows?: number }) {
  const [v, set] = useVal<unknown>(path)
  return <JsonText value={v} onChange={set} label={label} hint={hint} rows={rows} />
}

// Header lists: name → one value or several (comma separated).
export function BHeaders({ path, label }: { path: string; label: string }) {
  const [v, set] = useVal<Record<string, string | string[]>>(path)
  const rows = Object.entries(v ?? {})
  const write = (next: Array<[string, string]>) => {
    const o: Record<string, string | string[]> = {}
    for (const [k, val] of next) {
      const parts = val.split(',').map((x) => x.trim())
      o[k] = parts.length > 1 ? parts : val
    }
    set(next.length ? o : undefined)
  }
  const text = (val: string | string[]) => (Array.isArray(val) ? val.join(', ') : val)
  return (
    <div className="sm:col-span-2">
      <span className="mb-1 block text-sm font-medium">{label}</span>
      <div className="grid gap-2">
        {rows.map(([k, val], i) => (
          <div key={i} className="flex gap-2">
            <Input value={k} placeholder="Имя" onChange={(e) => write(rows.map(([a, b], j) => [j === i ? e.target.value : a, text(b)]))} />
            <Input value={text(val)} placeholder="Значение (несколько — через запятую)" onChange={(e) => write(rows.map(([a, b], j) => [a, j === i ? e.target.value : text(b)]))} />
            <button type="button" aria-label="Удалить заголовок" className="px-2 text-dim hover:text-danger" onClick={() => write(rows.filter((_, j) => j !== i).map(([a, b]) => [a, text(b)]))}>×</button>
          </div>
        ))}
        <div>
          <button type="button" className="rounded-md border border-line px-2 py-1 text-xs hover:bg-surface-2" onClick={() => write([...rows.map(([a, b]): [string, string] => [a, text(b)]), ['', '']])}>+ Добавить заголовок</button>
        </div>
      </div>
    </div>
  )
}

export function Section({ title, children, hint }: { title: string; children: ReactNode; hint?: string }) {
  return (
    <fieldset className="mt-4 rounded-xl border border-line p-4">
      <legend className="flex items-center gap-1.5 px-1 text-sm font-semibold">{title}{hint && <Hint text={hint} />}</legend>
      <div className="grid gap-3 sm:grid-cols-2">{children}</div>
    </fieldset>
  )
}

export function Grid({ children }: { children: ReactNode }) {
  return <div className="grid gap-3 sm:grid-cols-2">{children}</div>
}

export function Full({ children }: { children: ReactNode }) {
  return <div className="sm:col-span-2">{children}</div>
}
