import { useState } from 'react'
import { api } from '../api'
import { Icon } from '../components/Icon'
import { ask } from '../components/confirm'
import { Alert, Badge, Button, Card, IconButton, PageHeader, StatusChip, Switch } from '../components/ui'
import { useLoad } from '../hooks'
import { formatBytes } from '../lib'
import type { Cascade, Inbound, Saved, Settings } from '../types'
import type { ServerInfo } from '../types'
import InboundForm from './InboundForm'
import type { Domains } from './inboundPresets'

// A flag emoji (two regional indicator letters) at the start of a connection's name.
const LEAD_FLAG = /^[🇦-🇿]{2}s*/u

// An inbound at its traffic limit or past its end date is left out of the Xray config.
const stoppedByLimit = (r: Inbound) => (r.totalBytes > 0 && r.up + r.down >= r.totalBytes) || (r.expiryAt !== null && new Date(r.expiryAt) <= new Date())

export default function Inbounds() {
  const list = useLoad<Inbound[]>('/api/inbounds')
  const settings = useLoad<Settings>('/api/settings')
  const servers = useLoad<ServerInfo[]>('/api/servers')
  const cascades = useLoad<Cascade[]>('/api/cascades')
  const [editing, setEditing] = useState<Inbound | 'new' | null>(null)
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')

  // Cascades that leave through this connection: it stops working for them when it is switched off.
  const usedAsExit = (in_: Inbound) => (cascades.data ?? []).filter((c) => c.exitInboundId === in_.id)

  async function save(in_: Inbound, patch: Partial<Inbound>) {
    setError('')
    try {
      const res = await api<Saved<Inbound>>(`/api/inbounds/${in_.id}`, { method: 'PUT', body: { ...in_, ...patch } })
      setWarning(res.warning ?? '')
      await list.reload()
      await cascades.reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function toggle(in_: Inbound) {
    const used = usedAsExit(in_)
    if (in_.enable && used.length > 0 && !(await ask(
      `Это подключение — выход каскада «${used.map((c) => c.name).join('», «')}». Если его выключить, каскад перестанет работать.\n\n` +
      'Чтобы убрать подключение только из подписок клиентов, оставив его работающим, используйте кнопку «Скрыть из подписок».\n\nВыключить всё равно?', { confirmLabel: 'Выключить' }))) return
    await save(in_, { enable: !in_.enable })
  }

  const toggleHidden = (in_: Inbound) => save(in_, { excludeFromSub: !in_.excludeFromSub })

  async function remove(in_: Inbound) {
    if (!(await ask(`Удалить подключение «${in_.remark}»? Клиенты останутся, но будут от него отвязаны.`, { confirmLabel: 'Удалить' }))) return
    setError('')
    try {
      const res = await api<Saved<null>>(`/api/inbounds/${in_.id}`, { method: 'DELETE' })
      setWarning(res.warning ?? '')
      await list.reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const rows = list.data ?? []
  const s: Domains = settings.data ?? { panelDomain: '', realityDomain: '' }

  return (
    <section>
      <PageHeader title="Подключения" hint="Новое подключение сразу попадает в подписки всех клиентов, а новые клиенты получают все подключения. Выключатель убирает подключение из подписок у всех сразу.">
        <Button variant="primary" onClick={() => setEditing('new')}><Icon name="plus" className="size-4" />Добавить</Button>
      </PageHeader>

      <div className="mb-4 grid gap-2">
        {list.error && <Alert>{list.error}</Alert>}
        {error && <Alert>{error}</Alert>}
        {warning && <Alert tone="warn">{warning}</Alert>}
        {!s.panelDomain && !s.realityDomain && !settings.loading && (
          <Alert tone="warn">Не заданы домены в «Настройках»: ссылки для клиентов не смогут быть собраны.</Alert>
        )}
      </div>

      <div className="grid gap-2.5">
        {rows.map((r, i) => {
          const limited = r.enable && stoppedByLimit(r)
          const casc = r.cascadeId ? cascades.data?.find((c) => c.id === r.cascadeId) : undefined
          const server = r.serverId ? servers.data?.find((x) => x.id === r.serverId) : undefined
          // A flag the name starts with is shown as the row's picture instead, so the name itself is just the name.
          const lead = r.remark.match(LEAD_FLAG)
          const name = r.remark.replace(LEAD_FLAG, '')
          const flagOf = (id: string) => (id ? servers.data?.find((x) => x.id === id)?.flag : undefined) || ''
          const used = r.up + r.down
          return (
            <Card key={r.id} className="ap-row flex flex-wrap items-center gap-x-4 gap-y-2 px-[18px] py-3 transition-[transform,border-color] duration-[250ms] ease-[var(--ease)] hover:-translate-y-px hover:border-line-strong" style={{ animationDelay: `${Math.min(i, 10) * 30}ms` }}>
              {casc ? (
                <span className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-[11px] bg-surface-2 px-2.5 text-xl leading-none">
                  <span title={casc.entryName || 'Главный сервер'}>{flagOf(casc.entryServerId) || <Icon name="server" className="size-4 text-dim" />}</span>
                  <span className="text-sm text-faint">→</span>
                  <span title={casc.exitName || 'Главный сервер'}>{flagOf(casc.exitServerId) || <Icon name="server" className="size-4 text-dim" />}</span>
                </span>
              ) : (
                <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-surface-2 text-xl leading-none text-dim" title={server?.name ?? 'Главный сервер'}>
                  {lead?.[0].trim() || server?.flag || <Icon name="plug" />}
                </span>
              )}
              <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 font-semibold">
                <span className="truncate">{name}</span>
                {r.excludeFromSub && <Badge tone="warn">Скрыто</Badge>}
              </div>
              <span className="rounded-md bg-surface-2 px-2 py-0.5 text-[12px] tabular-nums text-dim" title={r.expiryAt ? `Всего трафика через подключение. Действует до ${new Date(r.expiryAt).toLocaleDateString('ru-RU')}` : 'Всего трафика через подключение'}>
                {r.totalBytes > 0 ? `${formatBytes(used)} / ${formatBytes(r.totalBytes)}` : formatBytes(used)}
              </span>
              <StatusChip tone={!r.enable ? 'neutral' : limited ? 'warn' : 'ok'}>{!r.enable ? 'Выключено' : limited ? 'Лимит' : 'Работает'}</StatusChip>
              <div className="flex items-center gap-1">
                <Switch checked={r.enable} onChange={() => void toggle(r)} label={r.enable ? 'Выключить' : 'Включить'} />
                <IconButton icon={r.excludeFromSub ? 'eye' : 'eyeoff'} label={r.excludeFromSub ? 'Показать в подписках' : 'Скрыть из подписок'} onClick={() => void toggleHidden(r)} />
                <IconButton icon="pencil" label="Изменить" onClick={() => setEditing(r)} />
                <IconButton icon="trash" label="Удалить" tone="danger" onClick={() => void remove(r)} />
              </div>
            </Card>
          )
        })}
        {!list.loading && rows.length === 0 && <Card className="px-4 py-14 text-center text-dim">Подключений пока нет. Нажмите «Добавить» и выберите шаблон.</Card>}
      </div>

      {editing && (
        <InboundForm
          initial={editing === 'new' ? null : editing}
          settings={s}
          onClose={() => setEditing(null)}
          onSaved={(w) => {
            setEditing(null)
            setWarning(w ?? '')
            void list.reload()
          }}
        />
      )}
    </section>
  )
}
