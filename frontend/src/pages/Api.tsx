import { useState } from 'react'
import { api, ApiError } from '../api'
import { Alert, Badge, Button, Card, CopyButton, PageHeader } from '../components/ui'
import { ask } from '../components/confirm'
import { useLoad } from '../hooks'
import { formatDate } from '../lib'
import type { ApiTokenInfo } from '../types'
import { docGroups, type DocField, type Endpoint, type Method } from './apiDocs'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

const code = 'rounded bg-surface-2 px-1 font-mono text-[0.85em]'

function KeyCard() {
  const info = useLoad<ApiTokenInfo>('/api/api-token')
  const [fresh, setFresh] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const exists = !!info.data?.exists

  async function generate() {
    if (exists && !(await ask('Прежний ключ перестанет работать. Заменить его новым?', { confirmLabel: 'Заменить' }))) return
    setBusy(true)
    setError('')
    try {
      const r = await api<{ token: string }>('/api/api-token', { method: 'POST' })
      setFresh(r.token)
      await info.reload()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  async function revoke() {
    if (!(await ask('Отозвать ключ? Все интеграции с ним перестанут работать.', { confirmLabel: 'Отозвать' }))) return
    setError('')
    try {
      await api('/api/api-token', { method: 'DELETE' })
      setFresh('')
      await info.reload()
    } catch (e) {
      setError(errText(e))
    }
  }

  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3 p-5">
        <div className="min-w-0 max-w-2xl">
          <h2 className="text-base font-semibold">Ключ доступа</h2>
          <p className="mt-1 text-sm text-dim">
            Один Bearer-токен для серверных интеграций: продаж, ботов, скриптов. Полное значение показывается только после создания,
            на сервере хранится лишь его хэш. Этим ключом нельзя управлять самим ключом, ботом и установкой серверов.
          </p>
        </div>
        <div className="flex gap-2">
          <Button onClick={generate} disabled={busy || info.loading}>
            {busy ? 'Подождите…' : exists ? 'Заменить' : 'Создать ключ'}
          </Button>
          {exists && <Button variant="danger" onClick={revoke} aria-label="Отозвать ключ">Отозвать</Button>}
        </div>
      </div>

      {(error || info.error || fresh) && (
        <div className="grid gap-2 px-5 pb-4">
          {(error || info.error) && <Alert>{error || info.error}</Alert>}
          {fresh && (
            <Alert tone="ok">
              <div className="font-medium">Скопируйте ключ сейчас — потом его не увидеть:</div>
              <div className="mt-1 flex flex-wrap items-center gap-2">
                <code className="break-all rounded bg-surface-2 px-2 py-1 text-xs text-ink">{fresh}</code>
                <CopyButton text={fresh} />
              </div>
            </Alert>
          )}
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-5 py-4">
        {exists ? (
          <>
            <div className="min-w-0">
              <div className="font-mono text-sm">{info.data?.prefix}••••••••</div>
              <div className="text-xs text-dim">{info.data?.createdAt ? `Создан ${formatDate(info.data.createdAt)}` : ''}</div>
            </div>
            <Badge tone="ok">Активен</Badge>
          </>
        ) : (
          <>
            <span className="text-sm text-dim">Ключ ещё не создан. API без него недоступно.</span>
            <Badge>Не создан</Badge>
          </>
        )}
      </div>
    </Card>
  )
}

const methodTone: Record<Method, string> = {
  GET: 'bg-surface-2 text-dim',
  POST: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
  PUT: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  PATCH: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  DELETE: 'bg-red-500/15 text-danger',
}

function MethodBadge({ method }: { method: Method }) {
  return (
    <span className={`inline-block w-16 shrink-0 rounded-md px-2 py-1 text-center font-mono text-xs font-semibold ${methodTone[method]}`}>
      {method}
    </span>
  )
}

function Fields({ title, rows }: { title: string; rows: DocField[] }) {
  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">{title}</h4>
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full min-w-[32rem] text-left text-sm">
          <thead className="bg-surface-2 text-xs text-dim">
            <tr>
              <th className="px-3 py-2 font-medium">Поле</th>
              <th className="px-3 py-2 font-medium">Тип</th>
              <th className="px-3 py-2 font-medium">Обязательное</th>
              <th className="px-3 py-2 font-medium">Описание</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((f) => (
              <tr key={f.name} className="align-top">
                <td className="px-3 py-2 font-mono text-xs font-semibold">{f.name}</td>
                <td className="px-3 py-2 font-mono text-xs text-dim">{f.type}</td>
                <td className="px-3 py-2">{f.required ? <Badge tone="warn">Да</Badge> : <span className="text-dim">Нет</span>}</td>
                <td className="px-3 py-2 text-dim">{f.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function Sample({ title, text, badge }: { title: string; text: string; badge?: string }) {
  return (
    <div className="min-w-0">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h4 className="text-xs font-semibold uppercase tracking-wide text-dim">{title}</h4>
        <div className="flex items-center gap-1">
          {badge && <Badge tone="ok">{badge}</Badge>}
          <CopyButton text={text} />
        </div>
      </div>
      <pre className="max-h-80 overflow-auto rounded-lg border border-line bg-surface-2 p-3 text-xs leading-relaxed">{text}</pre>
    </div>
  )
}

function curl(e: Endpoint, origin: string): string {
  const parts = [`curl -X ${e.method} "${origin}${e.path}"`]
  if (!e.public) parts.push(`-H "Authorization: Bearer ТОКЕН"`)
  if (e.request) {
    parts.push(`-H "Content-Type: application/json"`)
    parts.push(`-d '${e.request.replace(/'/g, `'\\''`)}'`)
  }
  return parts.join(' \\\n  ')
}

function EndpointRow({ e, open, toggle, origin }: { e: Endpoint; open: boolean; toggle: () => void; origin: string }) {
  const panelId = `ep-${e.method}-${e.path.replace(/[^a-z0-9]+/gi, '-')}`
  return (
    <div className={open ? 'bg-surface-2/40' : ''}>
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={panelId}
        className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-surface-2/60"
      >
        <MethodBadge method={e.method} />
        <span className="min-w-0 flex-1">
          <span className="block break-all font-mono text-sm font-medium">{e.path}</span>
          <span className="block text-sm text-dim">{e.title}</span>
        </span>
        {e.public && <Badge>Без ключа</Badge>}
        <svg
          viewBox="0 0 20 20"
          className={`size-4 shrink-0 text-dim transition-transform ${open ? 'rotate-180' : ''}`}
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden
        >
          <path d="m5 8 5 5 5-5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && (
        <div id={panelId} className="grid gap-5 border-t border-line px-4 py-5">
          <p className="text-sm text-dim">{e.desc}</p>
          {e.params && <Fields title="Параметры" rows={e.params} />}
          {e.body && <Fields title="JSON-тело запроса" rows={e.body} />}
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="grid min-w-0 content-start gap-4">
              <Sample title="Пример вызова" text={curl(e, origin)} />
              {e.request && <Sample title="Пример тела запроса" text={e.request} />}
            </div>
            <Sample title="Пример ответа" text={e.response} badge={e.status ?? '200 OK'} />
          </div>
          {e.note && <p className="rounded-lg border border-line bg-surface px-3 py-2 text-sm text-dim">{e.note}</p>}
        </div>
      )}
    </div>
  )
}

function Intro() {
  return (
    <Card className="p-5">
      <h2 className="text-base font-semibold">Как пользоваться</h2>
      <ul className="mt-3 grid gap-2 text-sm text-dim">
        <li>
          Передавайте ключ в заголовке <code className={code}>Authorization: Bearer ТОКЕН</code>. Куки и заголовок CSRF для запросов с ключом не нужны.
        </li>
        <li>
          Тела запросов и ответы — JSON в UTF-8. Для запросов с телом добавляйте <code className={code}>Content-Type: application/json</code>.
        </li>
        <li>
          Даты — строки RFC 3339 в UTC, трафик — в байтах. Идентификаторы клиентов и подключений — строки из поля <code className={code}>id</code>.
        </li>
        <li>
          Ошибки приходят с кодом 4xx/5xx и телом <code className={code}>{'{"error": "описание"}'}</code>: 400 — неверные данные, 401 — неверный ключ, 404 — не найдено,
          409 — имя, порт или идентификатор подписки уже заняты, 429 — больше 600 запросов в минуту с одного адреса.
        </li>
        <li>
          Изменения клиентов и подключений сразу применяются к Xray. Если данные сохранены, а Xray конфигурацию не принял, ответ содержит поле{' '}
          <code className={code}>warning</code> с причиной.
        </li>
      </ul>
    </Card>
  )
}

function Docs() {
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  const all = docGroups.flatMap((g) => g.endpoints.map((e) => `${e.method} ${e.path}`))
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (k: string) =>
    setOpen((s) => {
      const n = new Set(s)
      if (!n.delete(k)) n.add(k)
      return n
    })

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Документация</h2>
        <Button variant="ghost" onClick={() => setOpen(open.size ? new Set() : new Set(all))}>
          {open.size ? 'Свернуть все' : 'Развернуть все'}
        </Button>
      </div>
      <Intro />
      {docGroups.map((g) => (
        <section key={g.id} aria-labelledby={`grp-${g.id}`}>
          <h3 id={`grp-${g.id}`} className="text-sm font-semibold">{g.title}</h3>
          {g.intro && <p className="mt-1 text-sm text-dim">{g.intro}</p>}
          <Card className="mt-2 divide-y divide-line overflow-hidden">
            {g.endpoints.map((e) => {
              const k = `${e.method} ${e.path}`
              return <EndpointRow key={k} e={e} open={open.has(k)} toggle={() => toggle(k)} origin={origin} />
            })}
          </Card>
        </section>
      ))}
    </div>
  )
}

export default function Api() {
  return (
    <section className="max-w-5xl">
      <PageHeader title="API" hint="Интеграции для продаж и автоматической выдачи подписок." />
      <div className="grid gap-8">
        <KeyCard />
        <Docs />
      </div>
    </section>
  )
}
