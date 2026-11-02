import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select } from '../components/ui'
import { ask } from '../components/confirm'
import { DomainPicker } from '../components/DomainPicker'
import CountryPicker from '../components/CountryPicker'
import { useLoad } from '../hooks'
import type { Cascade, DomainCheck, Inbound, Saved, ServerInfo } from '../types'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

const MAIN = '' // the main server has no id

type Mode = 'merge' | 'split'
type Form = { name: string; country: string; entry: string; exit: string; exitInbound: string; mode: Mode; domain: string }
const empty: Form = { name: '', country: '', entry: MAIN, exit: '', exitInbound: '', mode: 'split', domain: '' }

// A cascade chains two servers: clients connect to the entry server A and leave to the internet from the exit server B.
// Everything runs on port 443. Merge: A and B become one node (A's own connection is the cascade). Split: A stays as it is
// and the cascade is a second node on the same 443 under a domain of its own.
function CascadeForm({ servers, inbounds, onClose, onSaved }: { servers: ServerInfo[]; inbounds: Inbound[]; onClose: () => void; onSaved: (note: string) => void }) {
  const [f, setF] = useState<Form>(empty)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [check, setCheck] = useState<DomainCheck | null>(null)
  const [checking, setChecking] = useState(false)
  const [created, setCreated] = useState('') // a domain the panel has just made in reg.ru: DNS may not show it yet
  const set = (p: Partial<Form>) => setF((x) => ({ ...x, ...p }))
  const domain = f.domain.trim().toLowerCase()
  const dnsPending = created !== '' && created === domain

  const options = [{ id: MAIN, name: 'Основной сервер' }, ...servers.filter((s) => s.enable).map((s) => ({ id: s.id, name: `${s.flag ? `${s.flag} ` : ''}${s.name}` }))]
  // The exit connections: enabled VLESS connections of the chosen exit server that are not cascade nodes themselves.
  const exits = inbounds.filter((i) => (i.serverId || MAIN) === f.exit && i.enable && i.protocol === 'vless' && !i.cascadeId)
  // The connection a server got from the installer: REALITY behind nginx on 443. Merge turns exactly this one into the cascade.
  const own = inbounds.find((i) => (i.serverId || MAIN) === f.entry && !i.cascadeId && i.security === 'reality' && i.protocol === 'vless' && i.port === 8443 && i.listen === '127.0.0.1' && i.acceptProxyProtocol && i.public.port === 443)
  const entryName = options.find((o) => o.id === f.entry)?.name ?? ''

  // Defaults once the lists are there: the main server as entry, the first node as exit.
  useEffect(() => {
    setF((x) => (x.exit === '' && options.length > 1 && x.entry === MAIN ? { ...x, exit: options[1].id } : x))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [servers.length])

  async function checkDomain(name = domain, pending = dnsPending) {
    setChecking(true)
    setError('')
    try {
      setCheck(await api<DomainCheck>('/api/cascades/check-domain', { method: 'POST', body: { domain: name, entryServerId: f.entry, dnsPending: pending } }))
    } catch (err) {
      setCheck(null)
      setError(errText(err))
    } finally {
      setChecking(false)
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const res = await api<Saved<Cascade>>('/api/cascades', {
        method: 'POST',
        body: { name: f.name.trim(), country: f.country, entryServerId: f.entry, exitServerId: f.exit, exitInboundId: f.exitInbound, mode: f.mode, domain: f.mode === 'split' ? domain : '', dnsPending: f.mode === 'split' && dnsPending },
      })
      onSaved(res.warning ?? '')
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  const same = f.entry === f.exit
  const mergeBlocked = f.mode === 'merge' && !own
  const splitBlocked = f.mode === 'split' && (domain === '' || (check !== null && !check.ok))
  return (
    <Modal title="Новый каскад" onClose={onClose} wide persistent>
      <form onSubmit={submit} className="grid gap-4">
        <p className="text-sm text-dim">
          Клиент подключается к серверу A, а в интернет выходит с сервера B. Всё работает на порту 443. Каскад появится у всех клиентов как нода с выбранными названием и флагом.
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Название" hint="Так нода называется в приложении клиента.">
            <Input required value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="Германия через NL" autoFocus />
          </Field>
          <Field label="Флаг">
            <CountryPicker value={f.country} onChange={(country) => set({ country })} emptyLabel="Без флага" />
          </Field>
          <Field label="Сервер A — вход" hint="Сюда подключаются клиенты.">
            <Select value={f.entry} onChange={(e) => { set({ entry: e.target.value }); setCheck(null) }}>
              {options.map((o) => <option key={o.id || 'main'} value={o.id}>{o.name}</option>)}
            </Select>
          </Field>
          <Field label="Сервер B — выход" hint="С него трафик уходит в интернет.">
            <Select value={f.exit} onChange={(e) => set({ exit: e.target.value, exitInbound: '' })}>
              {options.map((o) => <option key={o.id || 'main'} value={o.id}>{o.name}</option>)}
            </Select>
          </Field>
        </div>
        {same && <Alert tone="warn">Вход и выход должны быть разными серверами.</Alert>}
        {!same && exits.length === 0 && <Alert tone="warn">На сервере B нет включённого подключения VLESS: сначала создайте его, каскад подключится к нему.</Alert>}

        <div className="grid gap-2 sm:grid-cols-2">
          {([
            ['split', 'Сплит', 'Сервер A остаётся обычной нодой, а каскад A → B — отдельная вторая нода на том же порту 443, со своим доменом. В подписке будут обе.'],
            ['merge', 'Слияние', 'Сервер A и сервер B становятся одной нодой C. Подключение A на 443 превращается в каскад, а прямого выхода с A у клиентов не остаётся. Одна нода вместо двух.'],
          ] as const).map(([id, title, text]) => (
            <label key={id} className={`flex cursor-pointer items-start gap-2 rounded-lg border p-3 text-sm ${f.mode === id ? 'border-accent bg-surface-2' : 'border-line'}`}>
              <input type="radio" name="mode" className="mt-1" checked={f.mode === id} onChange={() => set({ mode: id })} />
              <span><b>{title}</b><br /><span className="text-dim">{text}</span></span>
            </label>
          ))}
        </div>

        {f.mode === 'split' && (
          <div className="rounded-lg border border-line p-3">
            <Field
              label={`Домен новой ноды (A-запись на адрес: ${entryName})`}
              hint="Нужен отдельный домен: по нему сервер A на порту 443 отличает каскад от своей обычной ноды. Панель сама добавит его в nginx. Проверка смотрит, что домен свободен и указывает на сервер A."
            >
              <div className="grid gap-2">
                <DomainPicker value={f.domain} onChange={(v) => { set({ domain: v }); setCheck(null) }} placeholder="nl2.example.com" target={{ serverId: f.entry }} onCreated={(d) => { const n = d.trim().toLowerCase(); setCreated(n); void checkDomain(n, true) }} />
                <div><Button disabled={checking || domain === ''} onClick={() => void checkDomain()}>{checking ? 'Проверяем…' : 'Проверить домен'}</Button></div>
              </div>
            </Field>
            {check && check.ok && <Alert tone={check.pending ? 'warn' : 'ok'}>{check.message}</Alert>}
            {check && !check.ok && <Alert tone="warn">{check.problem}</Alert>}
            {check && (check.addresses ?? []).length > 0 && (
              <div className="mt-2 text-xs text-dim">DNS домена: {(check.addresses ?? []).join(', ')}{(check.main ?? []).length > 0 && <> · сервер A: {(check.main ?? []).join(', ')}</>}</div>
            )}
          </div>
        )}

        {f.mode === 'merge' && (
          own ? (
            <Alert tone="warn">
              Станет каскадом подключение «{own.remark}» ({entryName}, порт 443, домен {own.reality.serverNames[0]}). Его клиенты после этого выходят в интернет с сервера B. При удалении каскада подключение вернётся как было.
            </Alert>
          ) : (
            <Alert tone="warn">У сервера «{entryName}» нет подключения REALITY на порту 443 (за nginx), которое можно превратить в каскад. Создайте его или выберите «Сплит».</Alert>
          )
        )}

        {exits.length > 1 && (
          <details className="rounded-lg border border-line p-3">
            <summary className="cursor-pointer text-sm font-medium">Дополнительно</summary>
            <div className="mt-3">
              <Field label="Подключение на сервере B" hint="Пусто — первое подходящее (REALITY предпочтительнее).">
                <Select value={f.exitInbound} onChange={(e) => set({ exitInbound: e.target.value })}>
                  <option value="">Выбрать автоматически</option>
                  {exits.map((i) => <option key={i.id} value={i.id}>{i.remark} — {i.network} · {i.security}</option>)}
                </Select>
              </Field>
            </div>
          </details>
        )}

        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="primary" disabled={busy || same || f.name.trim() === '' || f.exit === '' || mergeBlocked || splitBlocked}>{busy ? 'Создаём…' : 'Создать каскад'}</Button>
        </div>
      </form>
    </Modal>
  )
}

function RenameForm({ c, onClose, onSaved }: { c: Cascade; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(c.name)
  const [country, setCountry] = useState(c.country)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api(`/api/cascades/${c.id}`, { method: 'PUT', body: { name: name.trim(), country } })
      onSaved()
    } catch (err) {
      setError(errText(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={`Каскад: ${c.name}`} onClose={onClose} persistent>
      <form onSubmit={submit} className="grid gap-3">
        <Field label="Название"><Input required value={name} onChange={(e) => setName(e.target.value)} autoFocus /></Field>
        <Field label="Флаг">
          <CountryPicker value={country} onChange={setCountry} emptyLabel="Без флага" />
        </Field>
        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="primary" disabled={busy || name.trim() === ''}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
        </div>
      </form>
    </Modal>
  )
}

export default function Cascades() {
  const list = useLoad<Cascade[]>('/api/cascades')
  const servers = useLoad<ServerInfo[]>('/api/servers')
  const inbounds = useLoad<Inbound[]>('/api/inbounds')
  const [creating, setCreating] = useState(false)
  const [renaming, setRenaming] = useState<Cascade | null>(null)
  const [note, setNote] = useState<{ tone: 'ok' | 'warn' | 'bad'; text: string } | null>(null)
  const [busy, setBusy] = useState('')

  async function remove(c: Cascade) {
    if (!(await ask(`Удалить каскад «${c.name}»? Нода пропадёт из подписок всех клиентов.`, { confirmLabel: 'Удалить' }))) return
    setBusy(c.id)
    setNote(null)
    try {
      const r = await api<{ warning?: string }>(`/api/cascades/${c.id}`, { method: 'DELETE' })
      if (r.warning) setNote({ tone: 'warn', text: r.warning })
      await list.reload()
    } catch (e) {
      setNote({ tone: 'bad', text: errText(e) })
    } finally {
      setBusy('')
    }
  }

  const rows = list.data ?? []
  return (
    <section className="max-w-5xl">
      <PageHeader title="Каскады" hint="Клиент подключается к серверу A (вход), а в интернет выходит с сервера B (выход). Для клиентов это отдельная нода, она сразу попадает в подписки всех клиентов.">
        <Button variant="primary" onClick={() => setCreating(true)}>Создать каскад</Button>
      </PageHeader>
      <div className="mb-4 grid gap-2">
        {list.error && <Alert>{list.error}</Alert>}
        {note && <Alert tone={note.tone === 'bad' ? 'bad' : note.tone}>{note.text}</Alert>}
      </div>

      {list.data && rows.length === 0 && <Card className="p-6 text-sm text-dim">Каскадов пока нет. Нажмите «Создать каскад»: нужны два сервера и на втором подключение VLESS.</Card>}

      <div className="grid gap-3">
        {rows.map((c) => (
          <Card key={c.id} className="p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-base font-semibold">{c.flag} {c.name}</span>
                  {c.state === 'running' && <Badge tone="ok">Работает</Badge>}
                  {c.state === 'off' && <Badge>Выключен</Badge>}
                  {c.state === 'broken' && <Badge tone="bad">Не работает</Badge>}
                </div>
                <div className="mt-1 text-sm">
                  <b>{c.entryName}</b> <span className="text-dim">→</span> <b>{c.exitName}</b>
                  {c.exitRemark && <span className="text-dim"> (подключение «{c.exitRemark}»)</span>}
                </div>
                <div className="mt-1 text-xs text-dim">
                  Клиенты подключаются к {c.entryAddress || '—'}:{c.port || '—'}
                  {c.mode === 'merge' ? <> · слияние: вход — подключение самого сервера</> : <> · сплит</>}
                </div>
                {c.problem && <div className="mt-2 text-sm text-danger">{c.problem}</div>}
              </div>
              <div className="flex gap-1">
                <Button variant="ghost" onClick={() => setRenaming(c)}>Изменить</Button>
                <Button variant="danger" disabled={busy === c.id} onClick={() => void remove(c)}>Удалить</Button>
              </div>
            </div>
          </Card>
        ))}
      </div>

      {creating && servers.data && inbounds.data && (
        <CascadeForm
          servers={servers.data}
          inbounds={inbounds.data}
          onClose={() => setCreating(false)}
          onSaved={(w) => {
            setCreating(false)
            setNote(w ? { tone: 'warn', text: w } : { tone: 'ok', text: 'Каскад создан и уже в подписках клиентов.' })
            void list.reload()
          }}
        />
      )}
      {renaming && <RenameForm c={renaming} onClose={() => setRenaming(null)} onSaved={() => { setRenaming(null); void list.reload() }} />}
    </section>
  )
}
