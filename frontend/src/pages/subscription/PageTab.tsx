import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { api } from '../../api'
import { ask } from '../../components/confirm'
import { Icon } from '../../components/Icon'
import { Alert, Badge, Button, Card, Field, Input, Modal, Segmented, Textarea } from '../../components/ui'
import { useLoad } from '../../hooks'
import type { PagePreset, PageSettings, PageTheme } from '../../types'

// What a page can use. `{{.name}}` puts the value in, escaped for the place it stands in (text, an attribute, a script).
const fields: Array<[string, string]> = [
  ['{{.name}}', 'имя подписки (e-mail клиента)'],
  ['{{.title}}', 'название сервиса из настроек подписки'],
  ['{{.statusLabel}} · {{.status}} · {{.statusTone}}', 'состояние: «Активна»…, active / disabled / expired / exhausted, ok / neutral / warn'],
  ['{{.usedText}} · {{.totalText}} · {{.leftText}}', 'трафик текстом: «12.0 ГБ», «40.0 ГБ» или «∞», остаток'],
  ['{{.used}} · {{.total}} · {{.up}} · {{.down}} · {{.percent}}', 'трафик числами (байты) и процент использования'],
  ['{{.unlimited}} · {{.hasExpiry}} · {{.hasUrl}}', 'условия для {{if …}}: без лимита, есть срок, есть ссылка'],
  ['{{.expiryText}} · {{.expiryAt}} · {{.daysLeft}}', 'срок: «10.01.2030» или «Бессрочно», дата ISO, дней осталось (−1 без срока)'],
  ['{{.url}}', 'ссылка подписки'],
  ['{{.qrSvg}}', 'QR-код ссылки готовой SVG-картинкой, размер задаётся CSS'],
  ['{{.supportUrl}} · {{.profileUrl}} · {{.announce}}', 'поддержка, сайт и объявление из настроек'],
  ['{{range .links}}{{.remark}} {{.protocol}} {{.network}} {{.security}} {{.link}}{{end}}', 'подключения клиента (имя, протокол, транспорт, защита, ссылка)'],
  ['{{range .apps}}{{.name}} {{.platforms}} {{.openUrl}} {{.copyOnly}}{{end}}', 'приложения: ссылка для добавления в один тап (у INCY её нет, copyOnly)'],
  ['{{.json}}', 'все значения одним объектом для скрипта в самом HTML: <script>const s = {{.json}}</script>'],
]

type File = 'html' | 'css' | 'js'
const files: { id: File; label: string }[] = [
  { id: 'html', label: 'HTML' },
  { id: 'css', label: 'CSS' },
  { id: 'js', label: 'JavaScript' },
]
const holders: Record<File, string> = {
  html: '<!doctype html>\n<html>…</html>',
  css: '/* Стили добавятся в страницу перед </head> */',
  js: '// Скрипт добавится в конец страницы. Значения подписки лежат в объекте sub:\n// sub.name, sub.url, sub.usedText, sub.links …',
}

const DEFAULT = ''
const newId = () => `t${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`

const PRESET = 'preset:'

type Option = { id: string; name: string; note?: string; tag?: string; icon: ReactNode }

// The list of pages as a drop-down: a picture, the name and, for a page from a sponsor, a tag. A native <select> cannot show
// that. It works from the keyboard: arrows move, Enter takes, Esc closes.
function PageSelect({ options, value, onChange }: { options: Option[]; value: string; onChange: (id: string) => void }) {
  const [open, setOpen] = useState(false)
  const [at, setAt] = useState(0)
  const box = useRef<HTMLDivElement>(null)
  const current = options.find((o) => o.id === value) ?? options[0]

  useEffect(() => {
    if (!open) return
    const close = (e: PointerEvent) => {
      if (e.target instanceof Node && !box.current?.contains(e.target)) setOpen(false)
    }
    addEventListener('pointerdown', close)
    return () => removeEventListener('pointerdown', close)
  }, [open])

  function show() {
    setAt(Math.max(0, options.findIndex((o) => o.id === value)))
    setOpen(true)
  }
  function pick(id: string) {
    onChange(id)
    setOpen(false)
  }
  function onKey(e: React.KeyboardEvent) {
    if (!open) {
      if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        show()
      }
      return
    }
    if (e.key === 'Escape') setOpen(false)
    else if (e.key === 'ArrowDown') setAt((i) => (i + 1) % options.length)
    else if (e.key === 'ArrowUp') setAt((i) => (i - 1 + options.length) % options.length)
    else if (e.key === 'Enter' || e.key === ' ') pick(options[at].id)
    else return
    e.preventDefault()
  }

  const row = (o: Option) => (
    <>
      <span className="grid size-9 shrink-0 place-items-center overflow-hidden rounded-[10px] bg-surface-2 text-dim">{o.icon}</span>
      <span className="min-w-0 flex-1 truncate text-left font-medium">{o.name}{o.note && <span className="ml-2 font-normal text-faint">{o.note}</span>}</span>
      {o.tag && <Badge>{o.tag}</Badge>}
    </>
  )

  return (
    <div ref={box} className="relative" onKeyDown={onKey}>
      <button type="button" aria-haspopup="listbox" aria-expanded={open} onClick={() => (open ? setOpen(false) : show())} className="flex min-h-12 w-full items-center gap-3 rounded-[10px] border border-line-strong bg-surface px-3 py-1.5 text-sm hover:border-ink/30">
        {row(current)}
        <Icon name="down" className={`shrink-0 text-dim transition-transform duration-300 ease-[var(--ease)] ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <ul role="listbox" className="ap-enter absolute left-0 right-0 z-30 mt-1.5 grid gap-0.5 overflow-hidden rounded-xl border border-line bg-surface p-1.5 shadow-pop">
          {options.map((o, i) => (
            <li key={o.id} role="option" aria-selected={o.id === value}>
              <button type="button" onClick={() => pick(o.id)} onPointerEnter={() => setAt(i)} className={`flex min-h-12 w-full items-center gap-3 rounded-lg px-2 py-1.5 text-sm ${i === at ? 'bg-surface-2' : ''}`}>
                {row(o)}
                {o.id === value && <Icon name="check" className="shrink-0 text-ink" />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

const PANEL_ICON = <span className="text-sm font-bold uppercase text-ink">W</span>
const CODE_ICON = <span className="font-mono text-xs text-dim">{'</>'}</span>

// The subscriber's page: the standard one, a built-in page of the panel (a sponsor's), or one of the saved themes written by
// the administrator. A theme is an HTML document with {{…}} places for the subscription's values, its styles and its script.
export default function PageTab({ value: p, onChange }: { value: PageSettings; onChange: (patch: Partial<PageSettings>) => void }) {
  const [dialog, setDialog] = useState<'create' | 'rename' | null>(null)
  const presets = useLoad<PagePreset[]>('/api/settings/page-presets')
  const theme = p.themes.find((t) => t.id === p.selected)
  const preset = (presets.data ?? []).find((x) => PRESET + x.id === p.selected)

  const setTheme = (patch: Partial<PageTheme>) => onChange({ themes: p.themes.map((t) => (t.id === p.selected ? { ...t, ...patch } : t)) })

  async function create(name: string) {
    // A new theme starts as a copy of the example page, so there is something working to change.
    const ex = await api<{ html: string; css: string; js: string }>('/api/settings/page-example')
    const t: PageTheme = { id: newId(), name, html: ex.html, css: ex.css, js: ex.js }
    onChange({ themes: [...p.themes, t], selected: t.id })
  }

  async function remove() {
    if (!theme || !(await ask(`Удалить тему «${theme.name}»? Это видно после кнопки «Сохранить».`, { confirmLabel: 'Удалить' }))) return
    onChange({ themes: p.themes.filter((t) => t.id !== theme.id), selected: DEFAULT })
  }

  const options: Option[] = [
    { id: DEFAULT, name: 'Whitelist Panel', note: 'по умолчанию', icon: PANEL_ICON },
    ...(presets.data ?? []).map((x) => ({ id: PRESET + x.id, name: x.name, tag: x.tag, icon: <img src={x.icon} alt="" className="size-7 object-contain" /> })),
    ...p.themes.map((t) => ({ id: t.id, name: t.name, icon: CODE_ICON })),
  ]

  return (
    <div className="grid gap-5">
      <Card className="grid gap-4 p-5">
        <div>
          <h2 className="text-sm font-semibold">Страница подписки</h2>
          <p className="mt-1 text-sm text-dim">Что клиент видит в браузере по своей ссылке. Приложения (Happ, INCY и другие) получают саму подписку, как и раньше.</p>
        </div>
        <Field label="Тема">
          <PageSelect options={options} value={p.selected} onChange={(id) => onChange({ selected: id })} />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setDialog('create')} disabled={p.themes.length >= 20}>Создать</Button>
          {theme && <Button onClick={() => setDialog('rename')}>Переименовать</Button>}
          {theme && <Button variant="danger" onClick={() => void remove()}>Удалить</Button>}
        </div>
      </Card>

      {preset && <PresetPanel key={preset.id} preset={preset} saved={p.presets?.[preset.id] ?? {}} onChange={(vals) => onChange({ presets: { ...(p.presets ?? {}), [preset.id]: vals } })} />}
      {theme && <Editor key={theme.id} theme={theme} onChange={setTheme} />}

      {dialog && createPortal(
        // Outside the subscription form: a form inside a form is not valid HTML and its submit would reach the outer one.
        <NameDialog
          title={dialog === 'create' ? 'Новая тема' : 'Название темы'}
          initial={dialog === 'create' ? '' : theme?.name ?? ''}
          taken={p.themes.filter((t) => dialog === 'create' || t.id !== theme?.id).map((t) => t.name.toLowerCase())}
          onClose={() => setDialog(null)}
          onSave={async (name) => {
            if (dialog === 'create') await create(name)
            else setTheme({ name })
            setDialog(null)
          }}
        />,
        document.body,
      )}
    </div>
  )
}

// A built-in page: only the values it puts in can be changed, and how it looks is shown next to them.
function PresetPanel({ preset, saved, onChange }: { preset: PagePreset; saved: Record<string, string>; onChange: (vals: Record<string, string>) => void }) {
  const values: Record<string, string> = {}
  for (const f of preset.fields) values[f.key] = saved[f.key] ?? f.default
  const [view, setView] = useState<{ id: string } | { error: string } | null>(null)
  const seq = useRef(0)

  // The preview follows the typing with a pause; a link that is not valid yet is reported instead of drawn.
  const key = JSON.stringify(values)
  useEffect(() => {
    const mine = ++seq.current
    const t = setTimeout(async () => {
      try {
        const r = await api<{ id: string }>('/api/settings/page-preview', { method: 'POST', body: { preset: preset.id, fields: values } })
        if (mine === seq.current) setView({ id: r.id })
      } catch (e) {
        if (mine === seq.current) setView({ error: e instanceof Error ? e.message : String(e) })
      }
    }, 500)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, preset.id])

  return (
    <>
      <Card className="grid gap-4 p-5">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">{preset.name}<Badge>{preset.tag}</Badge></h2>
          <p className="mt-1 text-sm text-dim">Готовая страница. Подставьте свои ссылки: пустое поле убирает ссылку со страницы. Telegram можно ввести как @имя.</p>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          {preset.fields.map((f) => (
            <Field key={f.key} label={f.label}>
              <Input value={values[f.key]} placeholder={f.placeholder} spellCheck={false} onChange={(e) => onChange({ ...values, [f.key]: e.target.value })} />
            </Field>
          ))}
        </div>
      </Card>

      <Card className="grid gap-3 p-5">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold">Предпросмотр</h2>
          {view && 'id' in view && (
            <a href={`/api/settings/page-preview/${view.id}`} target="_blank" rel="noopener noreferrer" className="text-xs text-dim underline-offset-4 hover:text-ink hover:underline">Открыть в новой вкладке</a>
          )}
        </div>
        {view === null && <p className="text-sm text-dim">Загружается…</p>}
        {view && 'error' in view && <Alert>{view.error}</Alert>}
        {view && 'id' in view && (
          <iframe title="Предпросмотр страницы" src={`/api/settings/page-preview/${view.id}`} sandbox="allow-scripts allow-popups" className="h-[640px] w-full rounded-lg border border-line bg-white" />
        )}
      </Card>
    </>
  )
}

// The window that asks for a theme's name.
function NameDialog({ title, initial, taken, onSave, onClose }: { title: string; initial: string; taken: string[]; onSave: (name: string) => Promise<void>; onClose: () => void }) {
  const [name, setName] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const clean = name.trim()
  const duplicate = taken.includes(clean.toLowerCase())

  async function submit(e: FormEvent) {
    e.preventDefault()
    e.stopPropagation() // the settings form around the tab must not take this submit for its own
    if (clean === '' || duplicate) return
    setBusy(true)
    setError('')
    try {
      await onSave(clean)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose}>
      <form onSubmit={submit} className="grid gap-4">
        <Field label="Название">
          <Input autoFocus required maxLength={60} value={name} onChange={(e) => setName(e.target.value)} placeholder="Hidden Pool" />
        </Field>
        {duplicate && <Alert tone="warn">Тема с таким названием уже есть.</Alert>}
        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="primary" disabled={busy || clean === '' || duplicate}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
        </div>
      </form>
    </Modal>
  )
}

// The code of the selected theme and how it looks. Changes are kept with the page's «Сохранить» button.
function Editor({ theme, onChange }: { theme: PageTheme; onChange: (patch: Partial<PageTheme>) => void }) {
  const [file, setFile] = useState<File>('html')
  const [view, setView] = useState<{ id: string } | { error: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const seq = useRef(0)

  // The preview follows the code with a pause after typing; the server checks the page and renders it with made-up data.
  useEffect(() => {
    if (theme.html.trim() === '') {
      setView(null)
      return
    }
    const mine = ++seq.current
    const t = setTimeout(async () => {
      try {
        const r = await api<{ id: string }>('/api/settings/page-preview', { method: 'POST', body: { html: theme.html, css: theme.css, js: theme.js } })
        if (mine === seq.current) setView({ id: r.id })
      } catch (e) {
        if (mine === seq.current) setView({ error: e instanceof Error ? e.message : String(e) })
      }
    }, 600)
    return () => clearTimeout(t)
  }, [theme.html, theme.css, theme.js])

  async function insertExample() {
    if ((theme.html + theme.css + theme.js).trim() !== '' && !(await ask('Заменить код этой темы (HTML, CSS и JavaScript) примером?', { confirmLabel: 'Заменить' }))) return
    setBusy(true)
    setNote('')
    try {
      const r = await api<{ html: string; css: string; js: string }>('/api/settings/page-example')
      onChange({ html: r.html, css: r.css, js: r.js })
    } catch (e) {
      setNote(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Card className="grid gap-3 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Segmented
            label="Файл"
            value={file}
            onChange={setFile}
            small
            items={files.map((f) => ({ id: f.id, label: f.label, title: theme[f.id].trim() === '' ? 'пусто' : `${theme[f.id].length} символов` }))}
          />
          <div className="flex gap-2">
            <Button onClick={() => void insertExample()} disabled={busy}>Вставить пример</Button>
            <Button onClick={() => onChange({ [file]: '' } as Partial<PageTheme>)} disabled={theme[file] === ''}>Очистить {files.find((f) => f.id === file)?.label}</Button>
          </div>
        </div>
        <Textarea key={file} rows={22} value={theme[file]} spellCheck={false} placeholder={holders[file]} onChange={(e) => onChange({ [file]: e.target.value } as Partial<PageTheme>)} className="font-mono text-xs" />
        {theme.html.trim() === '' && <Alert tone="warn">HTML пуст: такую тему нельзя выбрать для клиентов.</Alert>}
        {note && <Alert>{note}</Alert>}
        <p className="text-xs text-dim">
          Страница открывается в песочнице: скрипты и стили работают, шрифты и картинки можно подключать с любых адресов, но доступа к панели у неё нет.
          Шаблон HTML проверяется при сохранении: страница с ошибкой не сохранится. CSS и JavaScript вставляются как есть.
        </p>
      </Card>

      <Card className="grid gap-3 p-5">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold">Предпросмотр</h2>
          {view && 'id' in view && (
            <a href={`/api/settings/page-preview/${view.id}`} target="_blank" rel="noopener noreferrer" className="text-xs text-dim underline-offset-4 hover:text-ink hover:underline">Открыть в новой вкладке</a>
          )}
        </div>
        {view === null && <p className="text-sm text-dim">Появится, когда в коде что-то есть. Подставляются придуманные данные.</p>}
        {view && 'error' in view && <Alert>{view.error}</Alert>}
        {view && 'id' in view && (
          <iframe title="Предпросмотр страницы" src={`/api/settings/page-preview/${view.id}`} sandbox="allow-scripts allow-popups" className="h-[640px] w-full rounded-lg border border-line bg-white" />
        )}
      </Card>

      <Card className="grid gap-3 p-5">
        <details>
          <summary className="cursor-pointer select-none text-sm font-semibold">Что можно подставить</summary>
          <div className="mt-3 grid gap-2.5">
            {fields.map(([code, text]) => (
              <div key={code} className="grid gap-1 border-t border-line pt-2.5 first:border-t-0 first:pt-0 sm:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] sm:gap-4">
                <code className="break-words font-mono text-xs">{code}</code>
                <span className="text-xs text-dim">{text}</span>
              </div>
            ))}
            <p className="text-xs text-dim">
              Условия и циклы: <code className="font-mono">{'{{if .hasUrl}}…{{end}}'}</code>, <code className="font-mono">{'{{range .links}}…{{end}}'}</code>; внутри цикла к общим значениям обращаются через <code className="font-mono">{'$.url'}</code>.
              Страница — шаблон Go html/template. В скрипте те же значения лежат в объекте <code className="font-mono">sub</code>.
            </p>
          </div>
        </details>
      </Card>
    </>
  )
}
