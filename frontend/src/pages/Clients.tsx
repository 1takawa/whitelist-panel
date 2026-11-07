import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useSearchParams } from 'react-router'
import { api } from '../api'
import { clientState, type ClientState } from '../clientState'
import { Icon } from '../components/Icon'
import { Alert, Button, Card, IconButton, Input, Modal, PageHeader, Segmented, StatusChip, Textarea } from '../components/ui'
import { useLoad } from '../hooks'
import { agoText, copyText, formatBytes, formatDate } from '../lib'
import type { Client, ClientLinks, Inbound, Saved } from '../types'
import ClientForm from './ClientForm'
import DevicesModal from './DevicesModal'
import LinksModal from './LinksModal'

type Filter = 'all' | 'online' | 'active' | 'trial' | 'expired' | 'blocked'
type SortKey = 'status' | 'traffic' | 'name' | 'expiry'

const filters: { id: Filter; label: string }[] = [
  { id: 'all', label: 'Все' },
  { id: 'online', label: 'Онлайн' },
  { id: 'active', label: 'Активны' },
  { id: 'trial', label: 'Тест' },
  { id: 'expired', label: 'Истекли' },
  { id: 'blocked', label: 'Заблок.' },
]

const sorts: { id: SortKey; label: string }[] = [
  { id: 'status', label: 'По статусу' },
  { id: 'traffic', label: 'По трафику' },
  { id: 'name', label: 'По имени' },
  { id: 'expiry', label: 'По сроку' },
]

function matchesFilter(f: Filter, s: ClientState): boolean {
  switch (f) {
    case 'all': return true
    case 'online': return s.online
    case 'active': return s.key === 'active' || s.key === 'trial'
    default: return s.key === f
  }
}

const DAY = 86400000

// What the second line under a name says: online, never connected, or when the client was last seen.
function SeenLine({ c, online }: { c: Client; online: boolean }) {
  if (online) return <div className="truncate text-xs font-normal text-online">онлайн</div>
  if (!c.lastActiveAt) return <div className="truncate text-xs font-normal text-faint">не подключался</div>
  return <div className="truncate text-xs font-normal text-faint" title={new Date(c.lastActiveAt).toLocaleString('ru-RU')}>был {agoText(c.lastActiveAt)}</div>
}

// The note of a client, edited in place: a click opens a field, Enter or leaving it saves, Esc cancels.
function NoteCell({ c, onSave }: { c: Client; onSave: (note: string) => Promise<void> }) {
  const [edit, setEdit] = useState(false)
  const [value, setValue] = useState(c.comment)
  const [busy, setBusy] = useState(false)
  useEffect(() => setValue(c.comment), [c.comment])

  async function commit() {
    const v = value.trim()
    setEdit(false)
    if (v === c.comment) return
    setBusy(true)
    try {
      await onSave(v)
    } finally {
      setBusy(false)
    }
  }
  function onKey(e: ReactKeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') e.currentTarget.blur()
    else if (e.key === 'Escape') {
      setValue(c.comment)
      setEdit(false)
    }
  }

  if (edit) {
    return <Input autoFocus value={value} maxLength={200} onChange={(e) => setValue(e.target.value)} onBlur={() => void commit()} onKeyDown={onKey} className="!h-8 !py-0 !text-xs" aria-label={`Заметка о ${c.email}`} />
  }
  return (
    <button type="button" onClick={() => setEdit(true)} title={c.comment || 'Добавить заметку'} className={`block w-full max-w-60 truncate rounded-md px-1.5 py-1 text-left text-xs hover:bg-surface-2 ${busy ? 'opacity-50' : ''} ${c.comment ? 'text-dim' : 'text-faint'}`}>
      {c.comment || 'Добавить заметку…'}
    </button>
  )
}

// How many days to add to the chosen clients.
function ExtendModal({ count, onClose, onConfirm }: { count: number; onClose: () => void; onConfirm: (days: number) => void }) {
  const [days, setDays] = useState(30)
  const ok = Number.isInteger(days) && days >= 1 && days <= 3650
  return (
    <Modal title={`Продлить ${count} ${count === 1 ? 'клиента' : 'клиентов'}`} onClose={onClose}>
      <p className="mb-3 text-sm text-dim">Дни добавятся к сроку подписки. Если срок уже вышел, отсчёт идёт от сегодняшнего дня. Клиенты без срока (бессрочные) останутся как есть.</p>
      <div className="mb-3 flex flex-wrap gap-2">
        {[7, 30, 90, 365].map((d) => (
          <Button key={d} variant={days === d ? 'primary' : 'default'} onClick={() => setDays(d)}>{d} дн.</Button>
        ))}
      </div>
      <Input type="number" min={1} max={3650} value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="Дней" />
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>Отмена</Button>
        <Button variant="primary" disabled={!ok} onClick={() => onConfirm(days)}>Продлить</Button>
      </div>
    </Modal>
  )
}

const isFilter = (v: string | null): v is Filter => filters.some((f) => f.id === v)

export default function Clients() {
  const clients = useLoad<Client[]>('/api/clients')
  const inbounds = useLoad<Inbound[]>('/api/inbounds')
  const [editing, setEditing] = useState<Client | 'new' | null>(null)
  const [links, setLinks] = useState<Client | null>(null)
  const [devicesOf, setDevicesOf] = useState<string | null>(null) // the client whose devices are open (looked up again after a reload)
  const [deleting, setDeleting] = useState<Client | null>(null)
  const [picked, setPicked] = useState<Set<string>>(new Set()) // ids ticked for a bulk action
  const [bulkModal, setBulkModal] = useState<'extend' | 'delete' | null>(null)
  const [bulkBusy, setBulkBusy] = useState('')
  const [linksText, setLinksText] = useState('') // the links to copy by hand when the browser does not let the page copy
  const [notice, setNotice] = useState<{ tone: 'ok' | 'warn'; text: string } | null>(null)
  const [menu, setMenu] = useState<{ id: string; x: number; y: number } | null>(null)
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: 'status', asc: true })
  const [params, setParams] = useSearchParams()
  const searchBox = useRef<HTMLInputElement>(null)

  const f = params.get('f')
  const filter: Filter = isFilter(f) ? f : 'all'
  const setFilter = (id: Filter) => setParams(id === 'all' ? {} : { f: id }, { replace: true })

  async function act(fn: () => Promise<Saved<unknown>>) {
    setError('')
    try {
      const res = await fn()
      setWarning(res.warning ?? '')
      await clients.reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  // "/" jumps to the search box, like in most tools.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '/' && !(document.activeElement instanceof HTMLInputElement) && !(document.activeElement instanceof HTMLTextAreaElement)) {
        e.preventDefault()
        searchBox.current?.focus()
      }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [])

  // The row menu closes on any click elsewhere, Esc, or scrolling.
  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(null)
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    addEventListener('pointerdown', close)
    addEventListener('keydown', onKey)
    addEventListener('scroll', close, true)
    return () => {
      removeEventListener('pointerdown', close)
      removeEventListener('keydown', onKey)
      removeEventListener('scroll', close, true)
    }
  }, [menu])

  const all = clients.data ?? []
  const ins = inbounds.data ?? []

  const now = Date.now()
  const states = useMemo(() => new Map(all.map((c) => [c.id, clientState(c, now)])), [all, now])
  const counts = useMemo(() => {
    const n: Record<Filter, number> = { all: all.length, online: 0, active: 0, trial: 0, expired: 0, blocked: 0 }
    for (const c of all) {
      const s = states.get(c.id)!
      for (const x of filters) if (x.id !== 'all' && matchesFilter(x.id, s)) n[x.id]++
    }
    return n
  }, [all, states])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const dir = sort.asc ? 1 : -1
    const used = (c: Client) => c.up + c.down
    const expiry = (c: Client) => (c.expiryAt ? new Date(c.expiryAt).getTime() : Infinity)
    return all
      .filter((c) => matchesFilter(filter, states.get(c.id)!) && (!q || c.email.toLowerCase().includes(q) || c.comment.toLowerCase().includes(q) || String(c.num ?? '') === q.replace(/^#/, '')))
      .sort((a, b) => {
        let r = 0
        if (sort.key === 'status') r = states.get(a.id)!.rank - states.get(b.id)!.rank
        else if (sort.key === 'traffic') r = used(a) - used(b)
        else if (sort.key === 'expiry') r = expiry(a) === expiry(b) ? 0 : expiry(a) < expiry(b) ? -1 : 1
        else r = a.email.localeCompare(b.email, 'ru')
        return (r || a.email.localeCompare(b.email, 'ru')) * dir
      })
  }, [all, states, filter, query, sort])

  // Ticks follow the clients that still exist; «select all» takes the rows that are shown.
  const chosen = all.filter((c) => picked.has(c.id))
  const shownIds = rows.map((c) => c.id)
  const allShown = shownIds.length > 0 && shownIds.every((id) => picked.has(id))
  const someShown = shownIds.some((id) => picked.has(id))
  const headBox = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (headBox.current) headBox.current.indeterminate = someShown && !allShown
  }, [someShown, allShown])
  const toggleAll = () => setPicked(allShown ? new Set() : new Set(shownIds))
  const toggleOne = (id: string) =>
    setPicked((p) => {
      const n = new Set(p)
      if (!n.delete(id)) n.add(id)
      return n
    })

  // Runs one request per chosen client, one after another (each change is applied to Xray), and tells how it went.
  async function bulk(title: string, run: (c: Client) => Promise<unknown> | null, done: (ok: number, skipped: number) => string) {
    setBulkBusy(title)
    setNotice(null)
    setError('')
    let ok = 0, skipped = 0
    const failed: string[] = []
    for (const c of chosen) {
      const job = run(c)
      if (!job) {
        skipped++
        continue
      }
      try {
        await job
        ok++
      } catch (e) {
        failed.push(`${c.email}: ${e instanceof Error ? e.message : String(e)}`)
      }
    }
    await clients.reload()
    setBulkBusy('')
    setPicked(new Set())
    setNotice({ tone: failed.length ? 'warn' : 'ok', text: [done(ok, skipped), ...failed.slice(0, 3)].join('. ') })
  }

  const save = (c: Client, patch: Partial<Client>) => api<Saved<Client>>(`/api/clients/${c.id}`, { method: 'PUT', body: { ...c, ...patch } })
  const extend = (days: number) => {
    setBulkModal(null)
    void bulk('extend', (c) => {
      if (!c.expiryAt) return null
      const base = Math.max(Date.now(), new Date(c.expiryAt).getTime())
      return save(c, { expiryAt: new Date(base + days * DAY).toISOString() })
    }, (ok, skipped) => `Продлено на ${days} дн.: ${ok}${skipped ? `, пропущено бессрочных: ${skipped}` : ''}`)
  }
  const freezing = chosen.some((c) => c.enable) // any active one in the choice: the action freezes, otherwise it thaws
  const freeze = () => void bulk('freeze', (c) => (c.enable === !freezing ? null : save(c, { enable: !freezing })), (ok) => `${freezing ? 'Заморожено' : 'Разморожено'}: ${ok}`)
  const removeChosen = () => {
    setBulkModal(null)
    void bulk('delete', (c) => api(`/api/clients/${c.id}`, { method: 'DELETE' }), (ok) => `Удалено: ${ok}`)
  }
  async function copyLinks() {
    setBulkBusy('links')
    setNotice(null)
    const lines: string[] = []
    for (let i = 0; i < chosen.length; i += 6) {
      const part = await Promise.all(chosen.slice(i, i + 6).map(async (c) => {
        try {
          const d = await api<ClientLinks>(`/api/clients/${c.id}/links`)
          return d.subscriptionUrl ? `${c.email} — ${d.subscriptionUrl}` : ''
        } catch {
          return ''
        }
      }))
      lines.push(...part.filter(Boolean))
    }
    setBulkBusy('')
    if (lines.length === 0) setNotice({ tone: 'warn', text: 'Ссылок нет: задайте домен панели в «Настройках».' })
    else if (await copyText(lines.join('\n'))) setNotice({ tone: 'ok', text: `Скопировано ссылок подписки: ${lines.length}` })
    else setLinksText(lines.join('\n'))
  }

  const sortBy = (key: SortKey) => setSort((s) => (s.key === key ? { key, asc: !s.asc } : { key, asc: key !== 'traffic' }))

  const Th = ({ k, children, className = '' }: { k: SortKey; children: string; className?: string }) => (
    <th className={`px-4 py-3 text-left text-xs font-medium ${className}`}>
      <button type="button" onClick={() => sortBy(k)} className={`inline-flex items-center gap-1 ${sort.key === k ? 'text-ink' : 'text-faint hover:text-ink'}`}>
        {children}
        <Icon name="up" className={`size-3 transition-[transform,opacity] duration-300 ease-[var(--ease)] ${sort.key === k ? (sort.asc ? 'rotate-180' : '') : 'opacity-0'}`} />
      </button>
    </th>
  )

  const menuClient = menu ? all.find((c) => c.id === menu.id) : undefined

  return (
    <section>
      <PageHeader title="Клиенты">
        <Button variant="primary" onClick={() => setEditing('new')}><Icon name="plus" className="size-4" />Добавить</Button>
      </PageHeader>

      <div className="mb-4 grid gap-2">
        {clients.error && <Alert>{clients.error}</Alert>}
        {error && <Alert>{error}</Alert>}
        {warning && <Alert tone="warn">{warning}</Alert>}
        {notice && <Alert tone={notice.tone}>{notice.text}</Alert>}
      </div>

      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <div className="relative max-w-80 min-w-52 flex-1">
          <Icon name="search" className="pointer-events-none absolute left-3 top-[11px] text-faint" />
          <input
            ref={searchBox}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && (setQuery(''), e.currentTarget.blur())}
            placeholder="Поиск по имени или номеру"
            aria-label="Поиск по имени или номеру"
            autoComplete="off"
            className="h-[38px] w-full rounded-[10px] border border-line-strong bg-surface pl-10 pr-9 text-sm transition-[border-color,box-shadow] placeholder:text-faint hover:border-faint focus:border-brand focus:shadow-[0_0_0_3px_var(--brand-soft)] focus:outline-none [&::-webkit-search-cancel-button]:hidden"
          />
          {query ? (
            <button type="button" aria-label="Очистить поиск" onClick={() => { setQuery(''); searchBox.current?.focus() }} className="absolute right-2 top-2 grid size-[22px] place-items-center rounded-md text-faint hover:bg-surface-2 hover:text-ink"><Icon name="x" className="size-3.5" /></button>
          ) : (
            <kbd className="pointer-events-none absolute right-2.5 top-2 rounded-md border border-line-strong px-1.5 py-0.5 font-mono text-[11px] text-faint">/</kbd>
          )}
        </div>
        <Segmented
          label="Статус"
          value={filter}
          onChange={setFilter}
          items={filters.map((x) => ({ id: x.id, label: x.label, count: counts[x.id] }))}
        />
        <select
          aria-label="Сортировка"
          value={sort.key}
          onChange={(e) => setSort({ key: e.target.value as SortKey, asc: e.target.value !== 'traffic' })}
          className="h-[34px] cursor-pointer appearance-none rounded-[10px] border border-line-strong bg-surface bg-[length:12px] bg-[right_10px_center] bg-no-repeat pl-3 pr-8 text-sm font-medium"
          style={{ backgroundImage: `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%238f939b' stroke-width='2.5' stroke-linecap='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E")` }}
        >
          {sorts.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
        </select>
      </div>

      <Card className="overflow-x-auto">
        <table className="w-full min-w-[720px] text-left text-[13.5px]">
          <thead className="border-b border-line">
            <tr>
              <th className="w-10 py-3 pl-4 pr-0">
                <input ref={headBox} type="checkbox" checked={allShown} onChange={toggleAll} aria-label="Выбрать всех показанных" className="size-4 cursor-pointer accent-[var(--accent)]" />
              </th>
              <Th k="name">Имя</Th>
              <th className="px-3 py-3 text-left text-xs font-medium text-faint max-[1100px]:hidden">№</th>
              <Th k="status">Статус</Th>
              <th className="px-3 py-3 text-left text-xs font-medium text-faint max-[980px]:hidden">Нода</th>
              <Th k="expiry" className="max-[820px]:hidden">До</Th>
              <Th k="traffic">Трафик</Th>
              <th className="px-3 py-3 text-left text-xs font-medium text-faint max-[1180px]:hidden">Заметка</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody>
            {rows.map((c, i) => {
              const s = states.get(c.id)!
              const used = c.up + c.down
              const pct = c.totalBytes > 0 ? Math.min(100, (used / c.totalBytes) * 100) : 0
              const left = c.expiryAt ? Math.ceil((new Date(c.expiryAt).getTime() - now) / DAY) : null
              return (
                <tr key={c.id} className={`ap-row border-b border-line last:border-0 hover:bg-surface-2/60 ${picked.has(c.id) ? 'bg-surface-2/50' : ''}`} style={{ animationDelay: `${Math.min(i, 12) * 22}ms` }}>
                  <td className="w-10 py-3 pl-4 pr-0">
                    <input type="checkbox" checked={picked.has(c.id)} onChange={() => toggleOne(c.id)} aria-label={`Выбрать ${c.email}`} className="size-4 cursor-pointer accent-[var(--accent)]" />
                  </td>
                  <td className="px-3 py-3">
                    <div className="flex items-center gap-3 font-medium">
                      <span className="grid size-7 shrink-0 place-items-center rounded-full bg-surface-2 text-xs font-semibold text-dim">{c.email.slice(0, 1).toUpperCase()}</span>
                      <div className="min-w-0">
                        <div className="truncate">{c.email}</div>
                        <SeenLine c={c} online={s.online} />
                      </div>
                    </div>
                  </td>
                  <td className="px-3 py-3 max-[1100px]:hidden">
                    <span title="Порядковый номер: по порядку создания, не меняется" className="px-1.5 font-mono text-xs tabular-nums text-faint">{c.num || '—'}</span>
                  </td>
                  <td className="px-3 py-3"><StatusChip tone={s.online ? 'ok' : s.tone} live={s.online}>{s.online ? 'Онлайн' : s.label}</StatusChip></td>
                  <td className="px-3 py-3 max-[980px]:hidden">
                    {c.lastNode ? (
                      <span className="inline-flex max-w-44 items-center gap-1.5 truncate" title={`Последний раз: ${agoText(c.lastNode.at)}`}>
                        <span className="text-base leading-none">{c.lastNode.flag}</span>
                        <span className="truncate">{c.lastNode.name}</span>
                      </span>
                    ) : (
                      <span className="text-faint">—</span>
                    )}
                  </td>
                  <td className="px-3 py-3 max-[820px]:hidden">
                    <div className="text-dim">{c.expiryAt ? formatDate(c.expiryAt) : 'Бессрочно'}</div>
                    {left !== null && <div className={`text-xs ${left <= 0 ? 'text-blocked' : left <= 7 ? 'text-expired' : 'text-faint'}`}>{left <= 0 ? 'истёк' : `через ${left} дн.`}</div>}
                  </td>
                  <td className="min-w-44 px-3 py-3 tabular-nums">
                    <div className="flex items-baseline justify-between gap-3">
                      <span>{formatBytes(used)}<span className="text-faint"> / {c.totalBytes ? formatBytes(c.totalBytes) : '∞'}</span></span>
                      {c.totalBytes > 0 && <span className="text-xs text-faint">{pct < 10 ? pct.toFixed(1) : Math.round(pct)}%</span>}
                    </div>
                    {c.totalBytes > 0 && (
                      <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-surface-2">
                        <div className={`h-full rounded-full transition-[width] duration-500 ease-[var(--ease)] ${pct >= 100 ? 'bg-blocked' : pct >= 80 ? 'bg-expired' : 'bg-active'}`} style={{ width: `${pct}%` }} />
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-3 max-[1180px]:hidden"><NoteCell c={c} onSave={async (note) => { await act(() => save(c, { comment: note })) }} /></td>
                  <td className="px-4 py-3">
                    <div className="flex justify-end gap-0.5">
                      <IconButton icon="link" label="Ссылки" onClick={() => setLinks(c)} />
                      <button type="button" aria-label={`Устройства: ${c.devices?.length ?? 0}`} title="Устройства на подписке" onClick={() => setDevicesOf(c.id)} className="flex h-8 min-w-[3.4rem] items-center justify-center gap-1 rounded-[9px] px-1.5 text-dim hover:bg-surface-2 hover:text-ink">
                        <Icon name="phone" />
                        <span className={`text-xs tabular-nums ${c.devices?.length ? '' : 'text-faint'}`}>({c.devices?.length ?? 0})</span>
                      </button>
                      <IconButton icon="pencil" label="Изменить" onClick={() => setEditing(c)} />
                      <IconButton
                        icon="more"
                        label="Ещё"
                        onPointerDown={(e) => e.stopPropagation()}
                        onClick={(e) => {
                          const r = e.currentTarget.getBoundingClientRect()
                          setMenu(menu?.id === c.id ? null : { id: c.id, x: r.right, y: r.bottom + 4 })
                        }}
                      />
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
        {!clients.loading && rows.length === 0 && (
          <div className="px-4 py-14 text-center text-dim">{all.length === 0 ? 'Клиентов пока нет' : 'Никого не нашлось'}</div>
        )}
      </Card>

      {chosen.length > 0 && (
        <div role="toolbar" aria-label="Действия с выбранными" className="ap-pop glass fixed bottom-6 left-1/2 z-30 flex max-w-[calc(100%-1.5rem)] -translate-x-1/2 flex-wrap items-center justify-center gap-2 rounded-2xl border border-line-strong p-2 pl-4 shadow-pop">
          <span className="mr-1 text-sm font-medium tabular-nums">Выбрано: {chosen.length}</span>
          <Button disabled={!!bulkBusy} onClick={() => setBulkModal('extend')}>Продлить</Button>
          <Button disabled={!!bulkBusy} onClick={freeze}>{freezing ? 'Заморозить' : 'Разморозить'}</Button>
          <Button disabled={!!bulkBusy} onClick={() => void copyLinks()}>{bulkBusy === 'links' ? 'Собираем…' : 'Скопировать ссылки'}</Button>
          <Button variant="danger" disabled={!!bulkBusy} onClick={() => setBulkModal('delete')}>Удалить</Button>
          <IconButton icon="x" label="Снять выбор" onClick={() => setPicked(new Set())} />
        </div>
      )}

      {menu && menuClient && (
        <div
          role="menu"
          onPointerDown={(e) => e.stopPropagation()}
          className="ap-pop glass fixed z-40 min-w-52 rounded-[14px] border border-line-strong p-1.5 shadow-pop"
          style={{ top: menu.y, left: Math.max(8, menu.x - 208) }}
        >
          <button type="button" role="menuitem" className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left hover:bg-surface-2" onClick={() => { setMenu(null); void act(() => api(`/api/clients/${menuClient.id}/reset-traffic`, { method: 'POST' })) }}>
            <Icon name="reset" />Сбросить трафик
          </button>
          <button type="button" role="menuitem" className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-blocked hover:bg-surface-2" onClick={() => { setMenu(null); setDeleting(menuClient) }}>
            <Icon name="trash" />Удалить
          </button>
        </div>
      )}

      {editing && (
        <ClientForm
          initial={editing === 'new' ? null : editing}
          inbounds={ins}
          onClose={() => setEditing(null)}
          onSaved={(w) => { setEditing(null); setWarning(w ?? ''); void clients.reload(); void inbounds.reload() }}
        />
      )}
      {bulkModal === 'extend' && <ExtendModal count={chosen.length} onClose={() => setBulkModal(null)} onConfirm={extend} />}
      {bulkModal === 'delete' && (
        <Modal title={`Удалить клиентов: ${chosen.length}?`} onClose={() => setBulkModal(null)}>
          <p className="mb-4 text-sm text-dim">{chosen.slice(0, 6).map((c) => c.email).join(', ')}{chosen.length > 6 ? ` и ещё ${chosen.length - 6}` : ''}. Их подписки перестанут работать, вернуть клиентов нельзя.</p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setBulkModal(null)}>Отмена</Button>
            <Button variant="primary" className="!border-blocked !bg-blocked !text-white" onClick={removeChosen}>Удалить</Button>
          </div>
        </Modal>
      )}
      {linksText && (
        <Modal title="Ссылки подписки" onClose={() => setLinksText('')}>
          <p className="mb-3 text-sm text-dim">Браузер не дал скопировать сам. Выделите текст и скопируйте его.</p>
          <Textarea readOnly autoFocus rows={Math.min(12, linksText.split('\n').length + 1)} value={linksText} onFocus={(e) => e.currentTarget.select()} />
          <div className="mt-4 flex justify-end"><Button variant="primary" onClick={() => setLinksText('')}>Готово</Button></div>
        </Modal>
      )}
      {links && <LinksModal client={links} onClose={() => setLinks(null)} />}
      {devicesOf && all.find((c) => c.id === devicesOf) && (
        <DevicesModal client={all.find((c) => c.id === devicesOf)!} onClose={() => setDevicesOf(null)} onChanged={() => void clients.reload()} />
      )}
      {deleting && (
        <Modal title={`Удалить «${deleting.email}»?`} onClose={() => setDeleting(null)}>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setDeleting(null)}>Отмена</Button>
            <Button variant="primary" className="!border-blocked !bg-blocked !text-white" onClick={() => { const c = deleting; setDeleting(null); void act(() => api(`/api/clients/${c.id}`, { method: 'DELETE' })) }}>Удалить</Button>
          </div>
        </Modal>
      )}
    </section>
  )
}
