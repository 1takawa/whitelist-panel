import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { api } from '../api'
import { clientState, type ClientState } from '../clientState'
import { Icon } from '../components/Icon'
import { Alert, Button, Card, IconButton, Modal, PageHeader, Segmented, StatusChip } from '../components/ui'
import { useLoad } from '../hooks'
import { formatBytes, formatDate } from '../lib'
import type { Client, Inbound, Saved } from '../types'
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

const isFilter = (v: string | null): v is Filter => filters.some((f) => f.id === v)

export default function Clients() {
  const clients = useLoad<Client[]>('/api/clients')
  const inbounds = useLoad<Inbound[]>('/api/inbounds')
  const [editing, setEditing] = useState<Client | 'new' | null>(null)
  const [links, setLinks] = useState<Client | null>(null)
  const [devicesOf, setDevicesOf] = useState<string | null>(null) // the client whose devices are open (looked up again after a reload)
  const [deleting, setDeleting] = useState<Client | null>(null)
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
      .filter((c) => matchesFilter(filter, states.get(c.id)!) && (!q || c.email.toLowerCase().includes(q) || c.comment.toLowerCase().includes(q)))
      .sort((a, b) => {
        let r = 0
        if (sort.key === 'status') r = states.get(a.id)!.rank - states.get(b.id)!.rank
        else if (sort.key === 'traffic') r = used(a) - used(b)
        else if (sort.key === 'expiry') r = expiry(a) === expiry(b) ? 0 : expiry(a) < expiry(b) ? -1 : 1
        else r = a.email.localeCompare(b.email, 'ru')
        return (r || a.email.localeCompare(b.email, 'ru')) * dir
      })
  }, [all, states, filter, query, sort])

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
            placeholder="Поиск по имени"
            aria-label="Поиск по имени"
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
        <table className="w-full min-w-[640px] text-left text-[13.5px]">
          <thead className="border-b border-line">
            <tr>
              <Th k="name">Имя</Th>
              <Th k="status">Статус</Th>
              <Th k="traffic">Трафик</Th>
              <Th k="expiry" className="max-[820px]:hidden">До</Th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody>
            {rows.map((c, i) => {
              const s = states.get(c.id)!
              const used = c.up + c.down
              const pct = c.totalBytes > 0 ? Math.min(100, (used / c.totalBytes) * 100) : 0
              return (
                <tr key={c.id} className="ap-row border-b border-line last:border-0 hover:bg-surface-2/60" style={{ animationDelay: `${Math.min(i, 12) * 22}ms` }}>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-3 font-medium">
                      <span className="grid size-7 shrink-0 place-items-center rounded-full bg-surface-2 text-xs font-semibold text-dim">{c.email.slice(0, 1).toUpperCase()}</span>
                      <div className="min-w-0">
                        <div className="truncate">{c.email}</div>
                        {c.comment && <div className="truncate text-xs font-normal text-faint">{c.comment}</div>}
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3"><StatusChip tone={s.online ? 'ok' : s.tone} live={s.online}>{s.online ? 'Онлайн' : s.label}</StatusChip></td>
                  <td className="min-w-40 px-4 py-3 tabular-nums">
                    {formatBytes(used)}<span className="text-faint"> / {c.totalBytes ? formatBytes(c.totalBytes) : '∞'}</span>
                    {c.totalBytes > 0 && (
                      <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-surface-2">
                        <div className={`h-full rounded-full transition-[width] duration-500 ease-[var(--ease)] ${pct >= 100 ? 'bg-blocked' : pct >= 80 ? 'bg-expired' : 'bg-active'}`} style={{ width: `${pct}%` }} />
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-3 text-dim max-[820px]:hidden">{c.expiryAt ? formatDate(c.expiryAt) : 'Бессрочно'}</td>
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
