import { useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { api } from '../api'
import { Icon } from '../components/Icon'
import { Sortable, SortGrip } from '../components/Sortable'
import { ask } from '../components/confirm'
import { Alert, Badge, Button, Card, IconButton, PageHeader, StatusChip, Switch } from '../components/ui'
import { useLoad } from '../hooks'
import { formatBytes } from '../lib'
import type { Cascade, Inbound, Saved, Settings } from '../types'
import type { ServerInfo } from '../types'
import InboundForm from './InboundForm'
import type { Domains } from './inboundPresets'

// A flag emoji (two regional indicator letters) at the start of a connection's name.
const LEAD_FLAG = /^[\u{1F1E6}-\u{1F1FF}]{2}\s*/u

// An inbound at its traffic limit or past its end date is left out of the Xray config.
const stoppedByLimit = (r: Inbound) => (r.totalBytes > 0 && r.up + r.down >= r.totalBytes) || (r.expiryAt !== null && new Date(r.expiryAt) <= new Date())

const AUTO = '__auto__' // the automatic node among the rows

export default function Inbounds() {
  const nav = useNavigate()
  const list = useLoad<Inbound[]>('/api/inbounds')
  const settings = useLoad<Settings>('/api/settings')
  const servers = useLoad<ServerInfo[]>('/api/servers')
  const cascades = useLoad<Cascade[]>('/api/cascades')
  const [editing, setEditing] = useState<Inbound | 'new' | null>(null)
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')
  const delays = useRef(new Map<string, number>())
  const [dragged, setDragged] = useState<string[] | null>(null) // the order just dropped, until the saved one comes back

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

  // The order of the subscriptions: by SortOrder (equal ones as created), the automatic node where it was placed.
  const auto = settings.data?.subscription.autoNode
  const inbounds = (list.data ?? []).map((r, i) => [r, i] as const).sort((a, b) => a[0].sortOrder - b[0].sortOrder || a[1] - b[1]).map((x) => x[0])
  const firstAfter = auto ? inbounds.findIndex((r) => r.sortOrder >= auto.position) : -1
  const autoAt = !auto || auto.last || firstAfter < 0 ? inbounds.length : firstAfter
  type Row = { id: string; inbound?: Inbound }
  const rowsInOrder: Row[] = inbounds.map((r) => ({ id: r.id, inbound: r }))
  if (auto?.enable) rowsInOrder.splice(autoAt, 0, { id: AUTO })
  const byId = new Map(rowsInOrder.map((r) => [r.id, r]))
  const rows: Row[] = dragged ? dragged.map((id) => byId.get(id)).filter((r): r is Row => !!r) : rowsInOrder

  async function saveOrder(ids: string[]) {
    setError('')
    setDragged(ids)
    try {
      await api('/api/inbounds/order', { method: 'POST', body: { order: ids } })
      await Promise.all([list.reload(), settings.reload()])
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setDragged(null)
    }
  }

  const s: Domains = settings.data ?? { panelDomain: '', realityDomain: '' }

  return (
    <section>
      <PageHeader title="Подключения" hint="Новое подключение сразу попадает в подписки всех клиентов, а новые клиенты получают все подключения. Выключатель убирает подключение из подписок у всех сразу. Строки можно перетаскивать: так задаётся порядок нод в подписке, автоузел тоже стоит в этом порядке.">
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

      <Sortable
        items={rows}
        id={(r) => r.id}
        canDrag={() => rows.length > 1}
        onReorder={(ids) => void saveOrder(ids)}
        render={(row) => {
          // The entrance delay is fixed at the row's first appearance: a delay that changed with the place would replay the entrance after a drag.
          if (!delays.current.has(row.id)) delays.current.set(row.id, delays.current.size)
          const delay = { animationDelay: `${Math.min(delays.current.get(row.id) ?? 0, 10) * 30}ms` }
          const r = row.inbound
          if (!r) {
            return (
              <Card className="ap-row flex flex-wrap items-center gap-x-4 gap-y-2 border-dashed px-[18px] py-3" style={delay}>
                <SortGrip className="-ml-2" />
                <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-surface-2 text-dim"><Icon name="refresh" className="size-[18px]" /></span>
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 font-semibold">
                  <span className="truncate">{auto?.name || 'Автоузел'}</span>
                  <Badge>Автоузел</Badge>
                </div>
                <span className="text-[12px] text-faint">приложение само выбирает самый быстрый сервер</span>
              </Card>
            )
          }
          const limited = r.enable && stoppedByLimit(r)
          const casc = r.cascadeId ? cascades.data?.find((c) => c.id === r.cascadeId) : undefined
          const server = r.serverId ? servers.data?.find((x) => x.id === r.serverId) : undefined
          // A flag the name starts with is shown as the row's picture instead, so the name itself is just the name.
          const lead = r.remark.match(LEAD_FLAG)
          const name = r.remark.replace(LEAD_FLAG, '')
          const flagOf = (id: string) => (id ? servers.data?.find((x) => x.id === id)?.flag : undefined) || ''
          const used = r.up + r.down
          return (
            <Card className="ap-row flex flex-wrap items-center gap-x-4 gap-y-2 px-[18px] py-3 transition-[transform,border-color] duration-[250ms] ease-[var(--ease)] hover:border-line-strong" style={delay}>
              {rows.length > 1 && <SortGrip className="-ml-2" />}
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
        }}
      />
      {auto && !auto.enable && inbounds.length > 1 && (
        <Card className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-2 border-dashed px-[18px] py-3 opacity-70">
          <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-surface-2 text-dim"><Icon name="refresh" className="size-[18px]" /></span>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 font-semibold">
            <span className="truncate">Автоузел</span>
            <StatusChip tone="neutral">Выключен</StatusChip>
          </div>
          <Button onClick={() => nav('/subscription')}>Настроить</Button>
        </Card>
      )}
      {!list.loading && inbounds.length === 0 && <Card className="px-4 py-14 text-center text-dim">Подключений пока нет. Нажмите «Добавить» и выберите шаблон.</Card>}

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
