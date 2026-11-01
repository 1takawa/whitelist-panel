import { useEffect, useState, type ReactNode } from 'react'
import { api, ApiError } from '../api'
import { Icon } from '../components/Icon'
import { ActionMenu, Alert, Badge, Button, Card, CopyButton, Field, IconButton, Input, Modal, PageHeader, Switch, Toggle, type MenuItem } from '../components/ui'
import { ask } from '../components/confirm'
import CountryPicker from '../components/CountryPicker'
import { useLoad } from '../hooks'
import { formatBytes, formatUptime } from '../lib'
import type { Dashboard, ServerClient, ServerInfo } from '../types'
import { JobLog, NodeInstaller } from './NodeInstall'
import XrayProfileModal from './XrayProfile'
import type { XrayStatus } from '../types'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

function installCommand(token: string, port: number, address: string) {
  return `sudo bash install-node.sh --token ${token} --port ${port} --host ${address} --yes`
}

// Shown once after a server is added by hand or its token is rotated: the token is not stored in a readable form anywhere in the UI.
function TokenModal({ token, server, onClose }: { token: string; server: ServerInfo; onClose: () => void }) {
  const cmd = installCommand(token, server.port, server.address)
  return (
    <Modal title={`Установка агента: ${server.name}`} onClose={onClose} wide>
      <ol className="grid list-decimal gap-3 pl-5 text-sm">
        <li>Скопируйте на новый сервер каталог <code className="rounded bg-surface-2 px-1">dist</code> с панелью (файлы <code className="rounded bg-surface-2 px-1">anypanel</code> и <code className="rounded bg-surface-2 px-1">install-node.sh</code>).</li>
        <li>
          Запустите на нём от root:
          <div className="mt-2 flex flex-wrap items-start gap-2">
            <code className="block max-w-full overflow-x-auto rounded-lg bg-surface-2 px-3 py-2 text-xs">{cmd}</code>
            <CopyButton text={cmd} />
          </div>
        </li>
        <li>Установщик напечатает отпечаток сертификата. Вставьте его в карточку сервера («Изменить») для защиты от подмены, затем нажмите «Проверить связь».</li>
      </ol>
      <div className="mt-4"><Alert tone="warn">Токен показывается один раз. Если потеряете — выпустите новый в карточке сервера и переустановите агент.</Alert></div>
      <div className="mt-4 flex justify-end"><Button variant="primary" onClick={onClose}>Готово</Button></div>
    </Modal>
  )
}

type FormState = { name: string; address: string; port: string; country: string; fingerprint: string; enable: boolean }

function ServerForm({ initial, onClose, onSaved }: { initial: ServerInfo | null; onClose: () => void; onSaved: (token?: string, s?: ServerInfo) => void }) {
  const [f, setF] = useState<FormState>({
    name: initial?.name ?? '',
    address: initial?.address ?? '',
    port: String(initial?.port ?? 8765),
    country: initial?.country ?? '',
    fingerprint: initial?.fingerprint ?? '',
    enable: initial?.enable ?? true,
  })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    setError('')
    const body = { name: f.name, address: f.address, port: Number(f.port), country: f.country, fingerprint: f.fingerprint, enable: f.enable }
    try {
      if (initial) {
        const s = await api<ServerInfo>(`/api/servers/${initial.id}`, { method: 'PUT', body })
        onSaved(undefined, s)
      } else {
        const r = await api<{ item: ServerInfo; token: string }>('/api/servers', { method: 'POST', body })
        onSaved(r.token, r.item)
      }
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={initial ? `Сервер: ${initial.name}` : 'Добавить сервер вручную'} onClose={onClose}>
      <div className="grid gap-3">
        <Field label="Имя в подписке" hint="Например: Франкфурт. Доступно в шаблонах имён ссылок как {{SERVER}}."><Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} autoFocus /></Field>
        <Field label="Страна и флаг" hint="Пусто: страна определится сама по адресу сервера. Шаблоны имён ссылок: {{FLAG}} и {{COUNTRY}}.">
          <CountryPicker value={f.country} onChange={(country) => setF({ ...f, country })} emptyLabel="Определить автоматически" />
        </Field>
        <Field label="Адрес" hint="Домен или IP сервера: сюда подключаются клиенты и панель">
          <Input value={f.address} onChange={(e) => setF({ ...f, address: e.target.value })} placeholder="de1.example.com" />
        </Field>
        <Field label="Порт агента"><Input type="number" min={1} max={65535} value={f.port} onChange={(e) => setF({ ...f, port: e.target.value })} /></Field>
        <Field label="Отпечаток сертификата агента (SHA-256)" hint="Печатает установщик. Пусто — панель запомнит сертификат при первом соединении.">
          <Input value={f.fingerprint} onChange={(e) => setF({ ...f, fingerprint: e.target.value })} spellCheck={false} className="font-mono text-xs" />
        </Field>
        {initial && <Toggle checked={f.enable} onChange={(v) => setF({ ...f, enable: v })} label="Включён (подключения и ссылки этого сервера выдаются клиентам)" />}
      </div>
      {error && <div className="mt-3"><Alert>{error}</Alert></div>}
      <div className="mt-4 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>Отмена</Button>
        <Button variant="primary" onClick={submit} disabled={busy || !f.name.trim() || !f.address.trim()}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
      </div>
    </Modal>
  )
}

const statusLabel: Record<ServerClient['status'], string> = { active: 'активен', expired: 'срок истёк', depleted: 'трафик исчерпан', disabled: 'отключён' }

// Clients of one server and what each moved through it.
function ClientsTable({ id }: { id: string }) {
  const rows = useLoad<ServerClient[]>(`/api/servers/${id}/clients`)
  const reload = rows.reload
  useEffect(() => {
    const t = setInterval(() => void reload(), 10000)
    return () => clearInterval(t)
  }, [reload])
  if (rows.error) return <Alert>{rows.error}</Alert>
  if (!rows.data) return <p className="text-sm text-dim">Загрузка…</p>
  if (rows.data.length === 0) return <p className="text-sm text-dim">К подключениям этого сервера пока не привязан ни один клиент.</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[32rem] text-left text-sm">
        <thead className="text-xs text-dim">
          <tr><th className="py-1 pr-3 font-medium">Клиент</th><th className="py-1 pr-3 font-medium">Статус</th><th className="py-1 pr-3 text-right font-medium">Отправлено</th><th className="py-1 pr-3 text-right font-medium">Получено</th><th className="py-1 text-right font-medium">Всего</th></tr>
        </thead>
        <tbody className="divide-y divide-line">
          {rows.data.map((c) => (
            <tr key={c.email}>
              <td className="py-1.5 pr-3">
                <span className={`mr-2 inline-block size-2 rounded-full ${c.online ? 'bg-emerald-500' : 'bg-line'}`} title={c.online ? 'онлайн' : 'не в сети'} aria-label={c.online ? 'онлайн' : 'не в сети'} />
                {c.email}
              </td>
              <td className="py-1.5 pr-3 text-dim">{statusLabel[c.status]}</td>
              <td className="py-1.5 pr-3 text-right tabular-nums">{formatBytes(c.up)}</td>
              <td className="py-1.5 pr-3 text-right tabular-nums">{formatBytes(c.down)}</td>
              <td className="py-1.5 text-right tabular-nums">{formatBytes(c.up + c.down)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// RealityModal asks for the node's domain and rebuilds the node the nginx-SNI way (nginx, certificate, decoy site and a
// new REALITY connection); the old connections on port 443 are replaced and their clients move to the new one.
function RealityModal({ server, onClose, onStarted }: { server: ServerInfo; onClose: () => void; onStarted: (jobId: string) => void }) {
  const [domain, setDomain] = useState('')
  const [force, setForce] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function start() {
    setBusy(true)
    setError('')
    try {
      const j = await api<{ id: string }>(`/api/servers/${server.id}/reality`, { method: 'POST', body: { domain: domain.trim(), forcePort: force } })
      onStarted(j.id)
    } catch (e) {
      setError(errText(e))
      setBusy(false)
    }
  }

  return (
    <Modal title={`REALITY-сайт: ${server.name}`} onClose={onClose} persistent>
      <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); if (domain.trim() && !busy) void start() }}>
        <p className="text-sm text-dim">
          Панель зайдёт на ноду по своему SSH-ключу и поставит nginx на порт 443 (маршрутизация по SNI), сертификат Let&apos;s Encrypt и сайт-заглушку для домена,
          по схеме с nginx. Прежние подключения этой ноды на порту 443 будут заменены одним новым подключением REALITY, оно сразу попадёт в подписки всех клиентов.
        </p>
        <Field label="Домен ноды" hint="Свой домен или поддомен с A-записью на адрес ноды (сертификат выпускается через порт 80).">
          <Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="reality.example.com" autoFocus spellCheck={false} />
        </Field>
        <Toggle checked={force} onChange={setForce} label="Освободить порты 80 и 443 принудительно (остановить nginx, apache и т. п.)" />
        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>Отмена</Button>
          <Button variant="primary" disabled={busy || domain.trim() === ''} onClick={() => void start()}>{busy ? 'Запускаем…' : 'Настроить'}</Button>
        </div>
      </form>
    </Modal>
  )
}

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-faint">{label}</div>
      <div className="mt-1 truncate text-[13.5px] font-medium tabular-nums">{children}</div>
    </div>
  )
}

// One server as a card: who it is and whether it is up on top, its numbers in the middle, switch and delete at the bottom.
// Everything else (restart, agent upgrade, ports, REALITY site, token, settings) is rare, so it lives in the "⋯" menu.
function ServerCard({ title, flag, code, badge, state, ping, first, uptime, load, online, footer, actions, children }: {
  title: string
  flag?: string
  code?: string
  badge?: string
  state: 'up' | 'down' | 'off'
  ping?: number
  first: { label: string; value: string }
  uptime: number
  load: string
  online: number | string
  footer: ReactNode
  actions: ReactNode
  children?: ReactNode
}) {
  const stateText = state === 'up' ? (ping ? `В сети · ${ping} мс` : 'В сети') : state === 'down' ? 'Нет связи' : 'Выключен'
  const stateTone = state === 'up' ? 'text-online' : state === 'down' ? 'text-blocked' : 'text-faint'
  return (
    <Card className="ap-row overflow-hidden">
      <div className="flex items-start justify-between gap-3 px-5 py-4">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-surface-2 text-2xl leading-none">{flag || <Icon name="server" className="size-5 text-dim" />}</span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate text-[15px] font-semibold">{title}</span>
              {badge && <Badge>{badge}</Badge>}
            </div>
            {code && <div className="mt-0.5 text-xs tracking-wider text-faint">{code}</div>}
          </div>
        </div>
        <div className={`flex shrink-0 items-center gap-1.5 text-[13px] font-medium ${stateTone}`}>
          <i className={`size-[7px] rounded-full bg-current ${state === 'up' ? 'ap-pulse [--c:var(--st-online)]' : ''}`} />
          {stateText}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-x-6 gap-y-4 border-t border-line px-5 py-4">
        <Stat label={first.label}>{first.value}</Stat>
        <Stat label="Онлайн">{online}</Stat>
        <Stat label="Аптайм">{state === 'up' && uptime > 0 ? formatUptime(uptime) : '—'}</Stat>
        <Stat label="Нагрузка">{state === 'up' ? load : '—'}</Stat>
      </div>
      {children}
      <div className="flex items-center justify-between gap-3 border-t border-line px-5 py-2.5">
        <div className="min-w-0 truncate text-xs text-faint">{footer}</div>
        <div className="flex shrink-0 items-center gap-1">{actions}</div>
      </div>
    </Card>
  )
}

const loadText = (sys: ServerInfo['system']) =>
  sys.memTotal > 0 ? `CPU ${sys.cpuPercent.toFixed(0)}% · RAM ${Math.round((sys.memUsed / sys.memTotal) * 100)}%` : `CPU ${sys.cpuPercent.toFixed(0)}%`

function Row({ s, onChanged, onNote, onEdit, onToken, onUpgrade, onFirewall, onReality }: { s: ServerInfo; onChanged: () => void; onNote: (text: string) => void; onEdit: () => void; onToken: (token: string) => void; onUpgrade: (jobId: string) => void; onFirewall: (jobId: string) => void; onReality: () => void }) {
  const [busy, setBusy] = useState(false)
  const [clientsOpen, setClientsOpen] = useState(false)
  const [xrayOpen, setXrayOpen] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  async function act(fn: () => Promise<void>, okText?: string) {
    setBusy(true)
    setMsg(null)
    try {
      await fn()
      if (okText) setMsg({ ok: true, text: okText })
      onChanged()
    } catch (e) {
      setMsg({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  const state = !s.enable ? 'off' : s.online ? 'up' : 'down'
  const items: MenuItem[] = [
    { icon: 'users', label: 'Клиенты сервера', onClick: () => setClientsOpen(true) },
    { icon: 'refresh', label: 'Проверить связь', onClick: () => void act(async () => { await api(`/api/servers/${s.id}/test`, { method: 'POST' }) }) },
    { icon: 'sliders', label: 'Настройки Xray', onClick: () => setXrayOpen(true) },
    { icon: 'reset', label: 'Перезапустить Xray', disabled: !s.online, onClick: () => void act(async () => { await api(`/api/servers/${s.id}/restart`, { method: 'POST' }) }, 'Xray перезапущен.') },
    ...(s.managed
      ? ([
          { icon: 'up', label: 'Обновить агент', onClick: () => void act(async () => { const j = await api<{ id: string }>(`/api/servers/${s.id}/upgrade`, { method: 'POST' }); onUpgrade(j.id) }) },
          { icon: 'plug', label: 'Открыть порты', onClick: () => void act(async () => { const j = await api<{ id: string }>(`/api/servers/${s.id}/open-ports`, { method: 'POST' }); onFirewall(j.id) }) },
          { icon: 'globe', label: 'REALITY-сайт', onClick: onReality },
        ] as MenuItem[])
      : []),
    { icon: 'pencil', label: 'Изменить', onClick: onEdit },
    {
      icon: 'link',
      label: 'Новый токен',
      onClick: async () => {
        if (!(await ask('Выпустить новый токен? Агент на сервере перестанет подключаться, пока его не переустановить с новым токеном.', { confirmLabel: 'Выпустить' }))) return
        void act(async () => { const r = await api<{ token: string }>(`/api/servers/${s.id}/token`, { method: 'POST' }); onToken(r.token) })
      },
    },
  ]

  return (
    <ServerCard
      title={s.name}
      flag={s.flag}
      code={s.country}
      state={state}
      ping={s.pingMs}
      first={{ label: 'Адрес', value: `${s.address}:${s.port}` }}
      uptime={s.system.uptimeSeconds}
      load={loadText(s.system)}
      online={s.onlineClients}
      footer={s.error ? <span className="text-blocked">{s.error}</span> : [s.xray.version && `Xray ${s.xray.version}`, s.agentVersion && `агент ${s.agentVersion}`].filter(Boolean).join(' · ')}
      actions={
        <>
          <Switch checked={s.enable} disabled={busy} label={s.enable ? 'Выключить сервер' : 'Включить сервер'} onChange={(v) => void act(async () => { await api(`/api/servers/${s.id}`, { method: 'PUT', body: { name: s.name, address: s.address, port: s.port, country: s.country, fingerprint: s.fingerprint, enable: v } }) })} />
          <IconButton icon="trash" label="Удалить" tone="danger" disabled={busy} onClick={async () => { if (await ask(`Удалить сервер «${s.name}» из панели? Сам сервер не изменится.`, { confirmLabel: 'Удалить' })) void act(async () => { const r = await api<{ warning?: string }>(`/api/servers/${s.id}`, { method: 'DELETE' }); if (r.warning) onNote(r.warning) }) }} />
          <ActionMenu items={items} />
        </>
      }
    >
      {msg && <div className="px-5 pb-4"><Alert tone={msg.ok ? 'ok' : 'bad'}>{msg.text}</Alert></div>}
      {clientsOpen && <Modal title={`Клиенты: ${s.name}`} onClose={() => setClientsOpen(false)} wide><ClientsTable id={s.id} /></Modal>}
      {xrayOpen && <XrayProfileModal id={s.id} title={s.name} onClose={() => setXrayOpen(false)} />}
    </ServerCard>
  )
}

// The main server: the machine the panel itself runs on. Its Xray is tuned the same way as every node's.
function LocalServer() {
  const st = useLoad<XrayStatus>('/api/xray/status')
  const dash = useLoad<Dashboard>('/api/dashboard')
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const x = st.data
  const d = dash.data
  const items: MenuItem[] = [
    { icon: 'sliders', label: 'Настройки Xray', onClick: () => setOpen(true) },
    {
      icon: 'reset',
      label: 'Перезапустить Xray',
      onClick: () => {
        setMsg(null)
        api('/api/xray/restart', { method: 'POST' }).then(
          () => { setMsg({ ok: true, text: 'Xray перезапущен.' }); void st.reload() },
          (e) => setMsg({ ok: false, text: errText(e) }),
        )
      },
    },
  ]
  return (
    <ServerCard
      title="Главный сервер"
      badge="Панель"
      state="up"
      first={{ label: 'Подключения', value: d ? `${d.inbounds.enabled} из ${d.inbounds.total}` : '—' }}
      uptime={d?.system.uptimeSeconds ?? 0}
      load={d ? loadText({ ...d.system }) : '—'}
      online={d?.clients.onlineHere ?? 0}
      footer={x?.lastError ? <span className="text-blocked">{x.lastError}</span> : x?.version ? `Xray ${x.version}${x.running ? '' : ' · остановлен'}` : x && !x.installed ? 'Xray не установлен' : ''}
      actions={<ActionMenu items={items} />}
    >
      {msg && <div className="px-5 pb-4"><Alert tone={msg.ok ? 'ok' : 'bad'}>{msg.text}</Alert></div>}
      {open && <XrayProfileModal id="local" title="главный сервер" onClose={() => { setOpen(false); void st.reload() }} />}
    </ServerCard>
  )
}

export default function Servers() {
  const list = useLoad<ServerInfo[]>('/api/servers')
  const [editing, setEditing] = useState<ServerInfo | 'new' | null>(null)
  const [shown, setShown] = useState<{ token: string; server: ServerInfo } | null>(null)
  const [installing, setInstalling] = useState(false)
  const [upgradeJob, setUpgradeJob] = useState('')
  const [firewallJob, setFirewallJob] = useState('')
  const [realityFor, setRealityFor] = useState<ServerInfo | null>(null)
  const [realityJob, setRealityJob] = useState('')
  const [note, setNote] = useState('') // a warning that has to outlive the row it came from (the row is gone)

  const reload = list.reload
  useEffect(() => {
    const t = setInterval(() => void reload(), 10000)
    return () => clearInterval(t)
  }, [reload])

  const rows = list.data ?? []
  const up = rows.filter((s) => s.enable && s.online)
  const down = rows.filter((s) => !(s.enable && s.online))
  const card = (s: ServerInfo) => (
    <Row key={s.id} s={s} onChanged={() => void list.reload()} onNote={setNote} onEdit={() => setEditing(s)} onToken={(token) => setShown({ token, server: s })} onUpgrade={setUpgradeJob} onFirewall={setFirewallJob} onReality={() => setRealityFor(s)} />
  )
  return (
    <section className="max-w-5xl">
      <PageHeader title="Серверы" hint="Дополнительные серверы, на которых работает только нода: Xray и лёгкий агент. Управление, клиенты и статистика остаются здесь, панель сама отправляет настройки и собирает трафик.">
        <Button onClick={() => setEditing('new')}>Добавить вручную</Button>
        <Button variant="primary" onClick={() => setInstalling(true)}>Установить ноду</Button>
      </PageHeader>      {list.error && <div className="mb-3"><Alert>{list.error}</Alert></div>}
      {note && <div className="mb-3"><Alert tone="warn"><div className="flex items-start justify-between gap-3"><span>{note}</span><Button variant="ghost" onClick={() => setNote('')}>Скрыть</Button></div></Alert></div>}
      <h2 className="mb-3 text-sm font-semibold">В сети <span className="ml-1 text-faint tabular-nums">{up.length + 1}</span></h2>
      <div className="grid gap-3 min-[900px]:grid-cols-2">
        <LocalServer />
        {up.map(card)}
      </div>
      {down.length > 0 && (
        <>
          <h2 className="mb-3 mt-7 text-sm font-semibold">Не в сети <span className="ml-1 text-faint tabular-nums">{down.length}</span></h2>
          <div className="grid gap-3 min-[900px]:grid-cols-2">{down.map(card)}</div>
        </>
      )}
      {list.data && rows.length === 0 && <p className="mt-4 text-sm text-faint">Дополнительных серверов пока нет</p>}

      {installing && <NodeInstaller onClose={() => setInstalling(false)} onDone={() => void list.reload()} />}
      {upgradeJob && (
        <Modal title="Обновление агента" onClose={() => { setUpgradeJob(''); void list.reload() }}>
          <JobLog jobId={upgradeJob} onEnd={() => void list.reload()} />
        </Modal>
      )}
      {realityFor && !realityJob && (
        <RealityModal
          server={realityFor}
          onClose={() => setRealityFor(null)}
          onStarted={(id) => setRealityJob(id)}
        />
      )}
      {realityJob && (
        <Modal title="REALITY-сайт на ноде" onClose={() => { setRealityJob(''); setRealityFor(null); void list.reload() }} persistent>
          <JobLog jobId={realityJob} onEnd={() => void list.reload()} />
        </Modal>
      )}
      {firewallJob && (
        <Modal title="Открытие портов" onClose={() => { setFirewallJob(''); void list.reload() }}>
          <JobLog jobId={firewallJob} onEnd={() => void list.reload()} />
        </Modal>
      )}
      {editing && (
        <ServerForm
          initial={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={(token, s) => {
            setEditing(null)
            if (token && s) setShown({ token, server: s })
            void list.reload()
          }}
        />
      )}
      {shown && <TokenModal token={shown.token} server={shown.server} onClose={() => setShown(null)} />}
    </section>
  )
}
