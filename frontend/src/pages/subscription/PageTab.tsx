import { useEffect, useRef, useState } from 'react'
import { api } from '../../api'
import { Alert, Button, Card, Segmented, Textarea, Toggle } from '../../components/ui'
import type { PageSettings } from '../../types'

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

// The subscriber's page written by the administrator: an HTML document with {{…}} places for the subscription's values.
export default function PageTab({ value: p, onChange }: { value: PageSettings; onChange: (patch: Partial<PageSettings>) => void }) {
  const [file, setFile] = useState<File>('html')
  const [view, setView] = useState<{ id: string } | { error: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const seq = useRef(0)

  // The preview follows the code with a pause after typing; the server checks the page and renders it with made-up data.
  useEffect(() => {
    if (p.html.trim() === '') {
      setView(null)
      return
    }
    const mine = ++seq.current
    const t = setTimeout(async () => {
      try {
        const r = await api<{ id: string }>('/api/settings/page-preview', { method: 'POST', body: { html: p.html, css: p.css, js: p.js } })
        if (mine === seq.current) setView({ id: r.id })
      } catch (e) {
        if (mine === seq.current) setView({ error: e instanceof Error ? e.message : String(e) })
      }
    }, 600)
    return () => clearTimeout(t)
  }, [p.html, p.css, p.js])

  async function insertExample() {
    if ((p.html + p.css + p.js).trim() !== '' && !window.confirm('Заменить текущий код (HTML, CSS и JavaScript) примером?')) return
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
    <div className="grid gap-5">
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Своя страница подписки</h2>
        <p className="text-sm text-dim">
          Страница, которую клиент видит в браузере по своей ссылке. Можно написать её целиком: вставьте HTML, стили (CSS) и скрипт (JavaScript), а значения подписки подставляются в HTML в места вида <code className="font-mono">{'{{.name}}'}</code>. В скрипте те же значения лежат в объекте <code className="font-mono">sub</code>.
          Приложения (Happ, INCY и другие) получают саму подписку, как и раньше.
        </p>
        <Toggle label="Показывать мою страницу вместо стандартной" checked={p.enable} onChange={(v) => onChange({ enable: v })} />
        {p.enable && p.html.trim() === '' && <Alert tone="warn">HTML пуст: вставьте свой или нажмите «Вставить пример».</Alert>}
      </Card>

      <Card className="grid gap-3 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Segmented
            label="Файл"
            value={file}
            onChange={setFile}
            small
            items={files.map((f) => ({ id: f.id, label: f.label, title: p[f.id].trim() === '' ? 'пусто' : `${p[f.id].length} символов` }))}
          />
          <div className="flex gap-2">
            <Button onClick={() => void insertExample()} disabled={busy}>Вставить пример</Button>
            <Button onClick={() => onChange({ [file]: '' } as Partial<PageSettings>)} disabled={p[file] === ''}>Очистить {files.find((f) => f.id === file)?.label}</Button>
          </div>
        </div>
        <Textarea key={file} rows={22} value={p[file]} spellCheck={false} placeholder={holders[file]} onChange={(e) => onChange({ [file]: e.target.value } as Partial<PageSettings>)} className="font-mono text-xs" />
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
              Страница — шаблон Go html/template.
            </p>
          </div>
        </details>
      </Card>
    </div>
  )
}
