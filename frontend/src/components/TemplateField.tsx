import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { TemplateToken } from '../types'
import { Input } from './ui'

type Kind = 'remark' | 'meta' | 'url'

const META_TOKENS = new Set(['EMAIL', 'ID', 'SHORT_ID', 'SUB_ID'])

// A text field for {{TOKEN}} templates: token chips insert at the cursor, and the result is previewed by the server
// (the same engine that builds real links), so what you see here is what subscribers get.
export default function TemplateField({
  label, value, onChange, kind, tokens, timeZone, hint, placeholder, maxLength = 256,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  kind: Kind
  tokens: TemplateToken[]
  timeZone?: string
  hint?: string
  placeholder?: string
  maxLength?: number
}) {
  const ref = useRef<HTMLInputElement>(null)
  const [preview, setPreview] = useState('')
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!value.includes('{{')) {
      setPreview('')
      setFailed(false)
      return
    }
    let live = true
    const t = setTimeout(() => {
      api<{ result: string }>('/api/settings/preview', { method: 'POST', body: { kind, template: value, timeZone: timeZone ?? '' } })
        .then((r) => { if (live) { setPreview(r.result); setFailed(false) } })
        .catch(() => { if (live) setFailed(true) })
    }, 300)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [value, kind, timeZone])

  function insert(name: string) {
    const el = ref.current
    const token = `{{${name}}}`
    if (!el) {
      onChange(value + token)
      return
    }
    const start = el.selectionStart ?? value.length
    const end = el.selectionEnd ?? value.length
    onChange(value.slice(0, start) + token + value.slice(end))
    requestAnimationFrame(() => {
      el.focus()
      const pos = start + token.length
      el.setSelectionRange(pos, pos)
    })
  }

  const shown = kind === 'remark' ? tokens : tokens.filter((t) => META_TOKENS.has(t.name))

  return (
    <div>
      <label className="block">
        <span className="mb-1 block text-sm font-medium">{label}</span>
        <Input ref={ref} value={value} maxLength={maxLength} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      </label>
      <details className="mt-1.5">
        <summary className="cursor-pointer select-none text-xs text-dim hover:text-ink">Подстановки ({shown.length}): нажмите, чтобы вставить в позицию курсора</summary>
        <div className="mt-1.5 flex flex-wrap gap-1">
          {shown.map((t) => (
            <button
              key={t.name}
              type="button"
              title={t.help}
              onClick={() => insert(t.name)}
              className="rounded-md border border-line bg-surface px-1.5 py-0.5 font-mono text-[11px] text-dim hover:bg-surface-2 hover:text-ink"
            >
              {t.name}
            </button>
          ))}
        </div>
      </details>
      {(preview || failed) && (
        <p className="mt-1.5 text-xs" aria-live="polite">
          <span className="text-dim">Предпросмотр: </span>
          {failed ? <span className="text-danger">не удалось построить</span> : <span className="break-all font-medium">{preview || '—'}</span>}
        </p>
      )}
      {hint && <p className="mt-1 text-xs text-dim">{hint}</p>}
    </div>
  )
}
