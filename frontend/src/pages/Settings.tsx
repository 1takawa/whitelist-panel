import { useEffect, useState, type ReactNode } from 'react'
import { api, apiUpload, ApiError } from '../api'
import { useAuth } from '../auth'
import { Alert, Badge, Button, Card, CopyButton, Field, Hint, Input, PageHeader, Select, Textarea } from '../components/ui'
import { ask } from '../components/confirm'
import { useLoad } from '../hooks'
import { formatDate } from '../lib'
import type { ImportReport, RegRuCertificate, RegRuStatus, TelegramStatus, UpdateInfo } from '../types'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

type Note = { ok: boolean; text: string } | null

const icons: Record<string, ReactNode> = {
  globe: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18" /></>,
  key: <><circle cx="8" cy="15" r="4" /><path d="m11 12 9-9M16 7l3 3" /></>,
  bot: <><rect x="4" y="8" width="16" height="11" rx="3" /><path d="M12 4v4M9 13v1M15 13v1" /></>,
  refresh: <path d="M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5" />,
  chevron: <path d="m6 9 6 6 6-6" />,
  db: <><ellipse cx="12" cy="6" rx="8" ry="3" /><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3" /></>,
}

// One settings block: icon, title, short description, optional control on the right, then the content. With a key the block
// can be folded, and the choice is remembered in this browser; until it is made the block is open when defaultOpen says so.
function Section({ icon, title, hint, aside, foldKey, defaultOpen = true, children }: { icon: string; title: string; hint: string; aside?: ReactNode; foldKey?: string; defaultOpen?: boolean; children: ReactNode }) {
  const [chosen, setChosen] = useState<boolean | null>(() => {
    try {
      const v = foldKey ? localStorage.getItem(`ap-fold-${foldKey}`) : null
      return v === null ? null : v === '1'
    } catch {
      return null
    }
  })
  const open = foldKey ? (chosen ?? defaultOpen) : true
  const toggle = () => {
    setChosen(!open)
    try {
      localStorage.setItem(`ap-fold-${foldKey}`, open ? '0' : '1')
    } catch {
      // the choice just is not remembered
    }
  }
  const svg = (
    <svg viewBox="0 0 24 24" className="size-4 shrink-0 text-dim" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {icons[icon]}
    </svg>
  )
  return (
    <Card>
      <div className="flex items-center justify-between gap-3 px-5 py-4">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            {foldKey ? (
              <button type="button" onClick={toggle} aria-expanded={open} className="flex items-center gap-2 text-left">
                {svg}
                {title}
              </button>
            ) : (
              <>
                {svg}
                {title}
              </>
            )}
            <Hint text={hint} />
          </h2>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {aside}
          {foldKey && (
            <button type="button" onClick={toggle} aria-label={open ? 'Свернуть' : 'Развернуть'} aria-expanded={open} className="grid size-7 place-items-center rounded-md text-dim transition-colors hover:bg-surface-2 hover:text-ink">
              <svg viewBox="0 0 24 24" className={`size-4 transition-transform ${open ? 'rotate-180' : ''}`} fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                {icons.chevron}
              </svg>
            </button>
          )}
        </div>
      </div>
      {open && <div className="border-t border-line p-5">{children}</div>}
    </Card>
  )
}

function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={`relative h-6 w-11 shrink-0 rounded-full transition-colors ${checked ? 'bg-accent' : 'bg-surface-2 border border-line'}`}
    >
      <span className={`absolute top-0.5 size-5 rounded-full bg-white shadow transition-all ${checked ? 'left-[22px]' : 'left-0.5'}`} />
    </button>
  )
}

function Message({ note }: { note: Note }) {
  return note ? <div className="mt-3"><Alert tone={note.ok ? 'ok' : 'bad'}>{note.text}</Alert></div> : null
}

// ---- administrator account ----

function Account() {
  const { user, logout } = useAuth()
  const [username, setUsername] = useState(user ?? '')
  const [password, setPassword] = useState('')
  const [note, setNote] = useState<Note>(null)
  const [busy, setBusy] = useState(false)

  async function save() {
    if (!(await ask('После изменения все сеансы будут завершены, потребуется войти заново. Продолжить?', { danger: false }))) return
    setBusy(true)
    setNote(null)
    try {
      await api('/api/account', { method: 'PUT', body: { username: username.trim(), password } })
      await logout() // the server already ended every session; this sends the user to the login page
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
      setBusy(false)
    }
  }

  const unchanged = username.trim() === user && password === ''
  return (
    <Section icon="key" title="Учётная запись" hint="Изменение завершит текущую сессию.">
      <form onSubmit={(e) => { e.preventDefault(); void save() }} className="grid gap-4">
        <Field label="Логин администратора">
          <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        </Field>
        <Field label="Новый пароль" hint="Минимум 10 символов">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" placeholder="Не менять" />
        </Field>
        <Message note={note} />
        <Button type="submit" variant="primary" disabled={busy || unchanged}>{busy ? 'Сохраняем…' : 'Обновить данные'}</Button>
      </form>
    </Section>
  )
}

// ---- administrators' Telegram bot ----

function Bot() {
  const status = useLoad<TelegramStatus>('/api/telegram')
  const [enable, setEnable] = useState(false)
  const [token, setToken] = useState('')
  const [ids, setIds] = useState('')
  const [note, setNote] = useState<Note>(null)
  const [busy, setBusy] = useState(false)
  const st = status.data

  useEffect(() => {
    if (st) {
      setEnable(st.enable)
      setIds(st.adminIds.join(', '))
    }
  }, [st])

  const parsed = ids.split(/[\s,;]+/).filter(Boolean)
  const bad = parsed.some((x) => !/^\d+$/.test(x))

  async function save() {
    setBusy(true)
    setNote(null)
    try {
      const next = await api<TelegramStatus>('/api/telegram', { method: 'PUT', body: { enable, adminIds: parsed.map(Number), token: token.trim() } })
      setToken('')
      setEnable(next.enable)
      setIds(next.adminIds.join(', '))
      await status.reload()
      setNote({ ok: true, text: 'Сохранено.' })
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  async function test() {
    setNote(null)
    try {
      await api('/api/telegram/test', { method: 'POST' })
      setNote({ ok: true, text: 'Сообщение отправлено администраторам.' })
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    }
  }

  return (
    <Section
      icon="bot"
      title="Бот для администраторов"
      hint="Управление пользователями и ежедневная отправка бэкапа БД."
      aside={<Switch checked={enable} onChange={setEnable} label="Включить бота" />}
    >
      <div className="mb-4 flex flex-wrap items-center gap-2 text-sm">
        <span className={`size-2 rounded-full ${st?.running ? 'bg-emerald-500' : 'bg-dim/50'}`} />
        {st?.username ? <span className="font-medium">@{st.username}</span> : <span className="text-dim">{st?.tokenSet ? 'Токен задан' : 'Токен не задан'}</span>}
        <Badge tone={st?.running ? 'ok' : 'neutral'}>{st?.running ? 'подключён' : 'остановлен'}</Badge>
        {st?.error && <span className="text-xs text-danger">{st.error}</span>}
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Токен от BotFather" hint="Токен хранится только на сервере.">
          <Input type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder={st?.tokenSet ? 'Токен сохранён — оставьте пустым' : '123456789:AA…'} />
        </Field>
        <Field label="Telegram ID администраторов" hint="Через запятую или пробел, максимум 20.">
          <Input value={ids} inputMode="numeric" onChange={(e) => setIds(e.target.value)} placeholder="123456789, 987654321" />
        </Field>
      </div>
      <div className="mt-4 border-l-2 border-accent/60 bg-surface-2 px-4 py-3 text-xs text-dim">
        <b className="text-ink">Перед проверкой.</b> Каждый администратор должен открыть бота и отправить <code>/start</code>. Копия базы приходит каждый день в 00:00 по Москве.
        Команды: <code>/help</code>.
      </div>
      <Message note={note} />
      {bad && <div className="mt-3"><Alert tone="warn">ID состоит только из цифр.</Alert></div>}
      <div className="mt-4 flex justify-end gap-2 border-t border-line pt-4">
        <Button onClick={test} disabled={!st?.running}>Проверить</Button>
        <Button variant="primary" onClick={save} disabled={busy || bad}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
      </div>
    </Section>
  )
}

// ---- REG.RU: the DNS of the panel's domain ----

// With the account connected, the node installer and cascades can create a node's domain (an A record to its address)
// by themselves. The panel runs on a server, so it is this server's address that must be allowed in the REG.RU account.
function RegRu() {
  const status = useLoad<RegRuStatus>('/api/regru')
  const [username, setUsername] = useState('')
  const [zone, setZone] = useState('')
  const [extra, setExtra] = useState('')
  const [password, setPassword] = useState('')
  const [cert, setCert] = useState('')
  const [key, setKey] = useState('')
  const [note, setNote] = useState<Note>(null)
  const [busy, setBusy] = useState(false)
  const [made, setMade] = useState(false) // a key pair was just made here and is waiting to be saved
  const st = status.data

  useEffect(() => {
    if (st) {
      setUsername(st.username)
      setZone(st.zone)
      setExtra((st.extraZones ?? []).join(', '))
    }
  }, [st?.username, st?.zone, (st?.extraZones ?? []).join(',')]) // eslint-disable-line react-hooks/exhaustive-deps

  const certHalf = (cert.trim() === '') !== (key.trim() === '')

  async function send(body: Record<string, unknown>, okText: string) {
    setBusy(true)
    setNote(null)
    try {
      const next = await api<RegRuStatus>('/api/regru', { method: 'PUT', body: { username: username.trim(), zone: zone.trim(), extraZones: extra.split(/[\s,;]+/).filter(Boolean), password, cert, key, ...body } })
      setPassword('')
      setCert('')
      setKey('')
      setMade(false)
      await status.reload()
      setNote(next.connected ? { ok: true, text: okText } : { ok: false, text: 'Сохранено, но подключиться к reg.ru не удалось: причина показана выше.' }) // the cause is already in the red block at the top
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  async function generate() {
    if (st?.certSet && !(await ask('Заменить сохранённый сертификат новым? Новый my.crt придётся заново загрузить в настройках API reg.ru, а до этого панель не сможет работать с DNS.', { confirmLabel: 'Заменить' }))) return
    setBusy(true)
    setNote(null)
    try {
      const c = await api<RegRuCertificate>('/api/regru/certificate', { method: 'POST' })
      setCert(c.certificate)
      setKey(c.key)
      setMade(true)
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  async function recheck() {
    setBusy(true)
    setNote(null)
    try {
      await api<RegRuStatus>('/api/regru?refresh=1') // a real test against reg.ru; the page itself uses the last one
    } catch {
      // the status below shows what went wrong
    }
    await status.reload()
    setBusy(false)
  }

  async function cleanup() {
    setBusy(true)
    setNote(null)
    try {
      const r = await api<{ removed: string[]; problems: string[] }>('/api/regru/cleanup', { method: 'POST' })
      await status.reload()
      setNote({ ok: r.problems.length === 0, text: [r.removed.length > 0 && `Удалено записей: ${r.removed.length} (${r.removed.join(', ')}).`, ...r.problems].filter(Boolean).join(' ') || 'Удалять было нечего.' })
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  async function disconnect() {
    if (!(await ask('Отключить reg.ru? Логин, пароль и сертификат будут удалены из панели. Уже созданные записи в DNS останутся.', { confirmLabel: 'Отключить' }))) return
    setBusy(true)
    try {
      await api('/api/regru', { method: 'DELETE' })
      setPassword('')
      setCert('')
      setKey('')
      setUsername('')
      setZone('')
      setExtra('')
      await status.reload()
      setNote({ ok: true, text: 'Отключено.' })
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  const label = !st?.configured ? 'не подключён' : st.connected ? 'подключён' : 'ошибка'
  const tone = !st?.configured ? 'neutral' : st.connected ? 'ok' : 'bad'

  return (
    <Section
      icon="globe"
      title="DNS в reg.ru"
      hint="Домен куплен в reg.ru: при добавлении сервера панель сама создаёт поддомен с A-записью на его адрес или берёт уже созданный."
      foldKey="regru"
      defaultOpen={!!st?.configured}
      aside={<Badge tone={tone}>{label}</Badge>}
    >
      {st?.configured && !st.connected && st.error && <div className="mb-4"><Alert>{st.error}</Alert></div>}
      {st?.serverIp && (
        <div className="mb-4">
          <Alert tone="warn">
            Адрес этого сервера для списка разрешённых IP в настройках API reg.ru: <code className="font-mono">{st.serverIp}</code> <CopyButton text={st.serverIp} />
          </Alert>
        </div>
      )}
      {st?.connected && st.unused.length > 0 && (
        <div className="mb-4">
          <Alert tone="warn">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span>Созданные панелью домены, которыми уже никто не пользуется: <code className="font-mono">{st.unused.join(', ')}</code></span>
              <Button disabled={busy} onClick={() => void cleanup()}>Удалить записи ({st.unused.length})</Button>
            </div>
          </Alert>
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="Логин reg.ru">
          <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label="Пароль API" hint="Альтернативный пароль из настроек API. Хранится только на сервере.">
          <Input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={st?.passwordSet ? 'Сохранён — оставьте пустым' : ''} />
        </Field>
        <Field label="Домен в reg.ru" hint="Зона, где создаются поддомены.">
          <Input value={zone} onChange={(e) => setZone(e.target.value)} placeholder="example.com" spellCheck={false} autoCapitalize="none" />
        </Field>
      </div>
      <div className="mt-4">
        <Field label="Другие домены этого аккаунта" hint="Необязательно, через запятую. В мастере установки можно будет выбрать, в каком из доменов создать поддомен; первый домен выше — по умолчанию.">
          <Input value={extra} onChange={(e) => setExtra(e.target.value)} placeholder="example.org, example.net" spellCheck={false} autoCapitalize="none" />
        </Field>
      </div>
      {st?.connected && (st.zoneProblems ?? []).length > 0 && (
        <div className="mt-4"><Alert tone="warn">Эти домены reg.ru не отдаёт панели: {st.zoneProblems.join('; ')}</Alert></div>
      )}
      <details className="mt-4 rounded-lg border border-line px-4 py-3" open={st?.certSet || made || undefined}>
        <summary className="cursor-pointer text-sm font-medium">
          Сертификат для API {st?.certSet && <Badge tone="ok">загружен</Badge>}
        </summary>
        <p className="mt-2 text-xs text-dim">
          Нужен, только если в настройках API reg.ru загружен сертификат: тогда reg.ru требует его в каждом запросе. Вставьте сертификат и ключ в формате PEM
          (то, что начинается с <code>-----BEGIN</code>). Ключ без пароля. Хранятся только на сервере.
        </p>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button disabled={busy} onClick={() => void generate()}>Сгенерировать сертификат на сервере</Button>
          <span className="text-xs text-dim">Панель сама создаст пару на 10 лет: ключ останется здесь, а сертификат нужно загрузить в reg.ru.</span>
        </div>
        {made && cert && (
          <div className="mt-3">
            <Alert tone="ok">
              <div className="grid gap-2">
                <span>
                  Сертификат создан. <b>1.</b> Скопируйте или скачайте его и загрузите в reg.ru: Личный кабинет → Настройки → API → сертификат.
                  <b> 2.</b> Нажмите «Сохранить» ниже: пара запишется на сервере.
                </span>
                <span className="flex flex-wrap gap-2">
                  <CopyButton text={cert} label="Копировать сертификат" />
                  <Button onClick={() => downloadText('my.crt', cert)}>Скачать my.crt</Button>
                </span>
              </div>
            </Alert>
          </div>
        )}
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <Field label="Сертификат (my.crt)"><Textarea value={cert} onChange={(e) => setCert(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} autoComplete="off" /></Field>
          <Field label="Приватный ключ (my.key)"><Textarea value={key} onChange={(e) => setKey(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" spellCheck={false} autoComplete="off" /></Field>
        </div>
        {certHalf && <div className="mt-3"><Alert tone="warn">Сертификат и ключ нужны вместе.</Alert></div>}
        {st?.certSet && (
          <div className="mt-3">
            <Button variant="danger" disabled={busy || username.trim() === '' || zone.trim() === ''} onClick={() => void send({ clearCert: true, cert: '', key: '' }, 'Сертификат удалён.')}>Удалить сертификат</Button>
          </div>
        )}
      </details>
      <div className="mt-4 border-l-2 border-accent/60 bg-surface-2 px-4 py-3 text-xs text-dim">
        <b className="text-ink">Что настроить в reg.ru.</b> Личный кабинет → Настройки → API: включите доступ, задайте альтернативный пароль для API и добавьте в разрешённые
        IP адрес сервера, где работает панель (при отказе панель покажет его выше). Панель использует только функции DNS-зоны: список записей, добавление и удаление A-записи. Удаляет она только те домены, которые создала сама, и вместе с нодой или каскадом, для которых они сделаны.
      </div>
      <Message note={note} />
      <div className="mt-4 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
        {st?.configured && <Button variant="danger" onClick={() => void disconnect()} disabled={busy}>Отключить</Button>}
        <Button onClick={() => void recheck()} disabled={busy || !st?.configured}>Проверить</Button>
        <Button variant="primary" onClick={() => void send({}, 'Подключено: reg.ru принял доступ.')} disabled={busy || certHalf || username.trim() === '' || zone.trim() === '' || (password === '' && !st?.passwordSet)}>
          {busy ? 'Проверяем…' : 'Сохранить'}
        </Button>
      </div>
    </Section>
  )
}

// ---- updates of the panel itself ----

function downloadText(name: string, text: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type: 'application/x-pem-file' }))
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 1000)
}

// Looks for a newer release of the panel and installs it: the panel downloads it, checks it and restarts itself; the page
// waits for the new version to answer and then reloads.
function Updates() {
  const [info, setInfo] = useState<UpdateInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<Note>(null)
  const [target, setTarget] = useState('') // the version being installed
  const [step, setStep] = useState('')

  async function check(manual: boolean) {
    setBusy(true)
    if (manual) setNote(null)
    try {
      setInfo(await api<UpdateInfo>('/api/update' + (manual ? '?refresh=1' : '')))
    } catch (e) {
      if (manual) setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  useEffect(() => { void check(false) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function install() {
    if (!info) return
    if (!(await ask(`Установить версию ${info.latest}? Панель скачает релиз, проверит контрольную сумму и перезапустится (около 15–30 секунд). Клиенты на нодах продолжат работать.`, { confirmLabel: 'Обновить' }))) return
    setNote(null)
    setBusy(true)
    try {
      await api('/api/update/apply', { method: 'POST', body: { version: info.latest } })
      setTarget(info.latest)
      setStep('Скачиваю релиз…')
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  // While an update runs: ask how far it is; when the panel answers with the new version, reload the page.
  useEffect(() => {
    if (!target) return
    const started = Date.now()
    let stop = false
    const tick = async () => {
      if (stop) return
      try {
        const st = await api<{ state: string; message: string; log: string }>('/api/update/status')
        if (st.state === 'failed') {
          setTarget('')
          setNote({ ok: false, text: `Обновление не удалось: ${st.message}${st.log ? `\n${st.log}` : ''}` })
          return
        }
        if (st.message) setStep(st.message)
        const now = await api<UpdateInfo>('/api/update')
        if (now.current === target) {
          window.location.reload()
          return
        }
      } catch {
        setStep('Панель перезапускается…') // it does not answer for a few seconds while it restarts
      }
      if (Date.now() - started > 4 * 60 * 1000) {
        setTarget('')
        setNote({ ok: false, text: 'Панель не ответила за 4 минуты. Проверьте её на сервере: journalctl -u anypanel -n 50. Если версия не поднялась, установщик сам вернёт прежнюю.' })
        return
      }
      setTimeout(() => void tick(), 3000)
    }
    const t = setTimeout(() => void tick(), 3000)
    return () => {
      stop = true
      clearTimeout(t)
    }
  }, [target])

  return (
    <Section
      icon="refresh"
      title="Обновление панели"
      hint="Панель берёт новые версии из релизов на GitHub. Перед установкой проверяется контрольная сумма; если новая версия не запустится, установщик возвращает прежнюю."
      aside={info ? <Badge>версия {info.current}</Badge> : undefined}
    >
      {target ? (
        <Alert tone="warn">Обновляю до {target}: {step} Страница обновится сама.</Alert>
      ) : info?.newer && info.canApply ? (
        <div className="grid gap-3">
          <Alert tone="warn">
            Доступна версия <b>{info.latest}</b>{info.published ? ` от ${formatDate(info.published)}` : ''}. Сейчас установлена {info.current}.
            {info.url && <> <a className="underline" href={info.url} target="_blank" rel="noreferrer">Что нового</a></>}
          </Alert>
          {info.notes && <pre className="max-h-40 overflow-y-auto whitespace-pre-wrap rounded-lg bg-surface-2 p-3 text-xs text-dim">{info.notes}</pre>}
        </div>
      ) : info ? (
        info.reason ? <Alert tone="warn">{info.reason}</Alert> : <p className="text-sm text-dim">Установлена последняя версия: {info.current}.</p>
      ) : (
        <p className="text-sm text-dim">{busy ? 'Проверяю…' : 'Нажмите «Проверить», чтобы узнать, вышла ли новая версия.'}</p>
      )}
      <Message note={note} />
      <div className="mt-4 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
        <Button onClick={() => void check(true)} disabled={busy || !!target}>{busy && !target ? 'Проверяю…' : 'Проверить наличие обновлений'}</Button>
        {info?.newer && info.canApply && !target && <Button variant="primary" onClick={() => void install()} disabled={busy}>Обновить до {info.latest}</Button>}
      </div>
    </Section>
  )
}

// ---- database export and import ----

const kindName = { full: 'Полная копия панели', users: 'Только пользователи', legacy: 'Пользователи прежней панели (users.json)' }

function Database() {
  const [file, setFile] = useState<File | null>(null)
  const [report, setReport] = useState<ImportReport | null>(null)
  const [expiryFrom, setExpiryFrom] = useState('creation')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function run(f: File, dry: boolean, from = expiryFrom) {
    setBusy(true)
    setError('')
    try {
      const r = await apiUpload<ImportReport>(`/api/db/import?dryRun=${dry ? 1 : 0}&expiryFrom=${from}`, f)
      if (!dry && r.kind === 'full') {
        alert('База заменена. Войдите с логином и паролем из файла.')
        window.location.assign('/login')
        return
      }
      setReport(r)
    } catch (e) {
      setReport(null)
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  function pick(f: File | null) {
    setFile(f)
    setReport(null)
    setError('')
    if (f) void run(f, true)
  }

  async function apply() {
    if (!file || !report) return
    const msg = report.kind === 'full'
      ? 'Полная загрузка ЗАМЕНИТ всё в панели: логин и пароль, настройки, подключения, серверы и клиентов. Текущие данные пропадут. Продолжить?'
      : `Добавить клиентов: ${report.created}?`
    if (await ask(msg, { danger: report.kind === 'full', confirmLabel: report.kind === 'full' ? 'Заменить всё' : 'Добавить' })) void run(file, false)
  }

  const skipped = report?.items.filter((i) => i.action === 'skip') ?? []
  const canApply = !!report && report.dryRun && (report.kind === 'full' || report.created > 0)

  return (
    <Section icon="db" title="База данных" hint="Выгрузка и загрузка. Полная копия содержит пароли и ключи — храните её как секрет.">
      <div className="grid gap-2 sm:grid-cols-2">
        <a href="/api/db/export?kind=full" download className="rounded-lg border border-line bg-surface px-3 py-2 text-center text-sm font-medium hover:bg-surface-2">Выгрузить FULL</a>
        <a href="/api/db/export?kind=users" download className="rounded-lg border border-line bg-surface px-3 py-2 text-center text-sm font-medium hover:bg-surface-2">Выгрузить Users only</a>
      </div>
      <p className="mt-2 text-xs text-dim">FULL — вся панель целиком, включая логин и пароль. Users only — только пользователи.</p>

      <div className="mt-5">
        <Field label="Загрузить файл" hint="Подходит FULL, Users only и users.json прежней панели.">
          <input
            type="file"
            accept=".json,.jsonl,application/json,text/plain"
            onChange={(e) => pick(e.target.files?.[0] ?? null)}
            className="block w-full text-sm file:mr-3 file:rounded-lg file:border file:border-line file:bg-surface file:px-3 file:py-1.5 file:text-sm"
          />
        </Field>
      </div>

      {error && <div className="mt-3"><Alert>{error}</Alert></div>}
      {report && (
        <div className="mt-3 rounded-lg border border-line bg-surface-2 p-3 text-sm">
          <div className="font-medium">{kindName[report.kind]}{report.exportedAt ? ` · ${formatDate(report.exportedAt)}` : ''}</div>
          {report.kind === 'full' && report.counts ? (
            <p className="mt-1 text-dim">В файле: клиентов {report.counts.clients}, подключений {report.counts.inbounds}, серверов {report.counts.servers}, внешних нод {report.counts.externalNodes}. Всё текущее будет заменено.</p>
          ) : (
            <p className="mt-1 text-dim">{report.dryRun ? 'Будет добавлено' : 'Добавлено'}: <b className="text-ink">{report.created}</b> · пропущено: <b className="text-ink">{report.skipped}</b></p>
          )}
          {Object.entries(report.notMigrated ?? {}).map(([k, n]) => <p key={k} className="mt-1 text-xs text-dim">Не переносится: {k} — {n}</p>)}
          {skipped.length > 0 && (
            <ul className="mt-2 max-h-32 overflow-y-auto text-xs text-dim">
              {skipped.slice(0, 50).map((i) => <li key={i.name}>{i.name}: {i.reason}</li>)}
              {skipped.length > 50 && <li>… и ещё {skipped.length - 50}</li>}
            </ul>
          )}
          {report.kind === 'legacy' && (
            <div className="mt-3 max-w-xs">
              <Field label="Срок действия" hint="В прежней панели срок хранится числом дней.">
                <Select value={expiryFrom} onChange={(e) => { setExpiryFrom(e.target.value); if (file) void run(file, true, e.target.value) }}>
                  <option value="creation">Считать от даты создания</option>
                  <option value="now">Считать от сегодняшнего дня</option>
                </Select>
              </Field>
            </div>
          )}
          {report.warning && <div className="mt-2"><Alert tone="warn">{report.warning}</Alert></div>}
          {!report.dryRun && <div className="mt-2"><Alert tone="ok">Готово: добавлено клиентов {report.created}.</Alert></div>}
        </div>
      )}
      <div className="mt-4 flex justify-end">
        <Button variant="primary" onClick={apply} disabled={busy || !canApply}>{busy ? 'Работаем…' : 'Загрузить'}</Button>
      </div>
    </Section>
  )
}

export default function Settings() {
  return (
    <section className="max-w-4xl">
      <PageHeader title="Настройки" />
      <div className="grid gap-5">
        <Updates />
        <RegRu />
        <div className="grid items-start gap-5 lg:grid-cols-2">
          <Account />
          <Database />
        </div>
        <Bot />
      </div>
    </section>
  )
}
