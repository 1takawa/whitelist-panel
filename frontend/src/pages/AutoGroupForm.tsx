import { useMemo, useState, type FormEvent } from 'react'
import CountryPicker from '../components/CountryPicker'
import { Alert, Button, Field, Input, Modal } from '../components/ui'
import type { AutoGroup, Inbound, ServerInfo } from '../types'

// A flag emoji (two regional indicator letters) at the start of a connection's name.
const LEAD_FLAG = /^[\u{1F1E6}-\u{1F1FF}]{2}\s*/u

// One more automatic node: its name, its flag and the connections it chooses among. The app picks the fastest of them
// and moves on when it stops answering, so a node over only the Russian servers always lands on the fastest Russian one.
export default function AutoGroupForm({ initial, inbounds, servers, onSave, onClose }: {
  initial: AutoGroup | null
  inbounds: Inbound[]
  servers: ServerInfo[]
  onSave: (g: AutoGroup) => Promise<void>
  onClose: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [country, setCountry] = useState(initial?.country ?? '')
  const [members, setMembers] = useState<Set<string>>(() => new Set(initial?.members ?? []))
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const rows = useMemo(
    () =>
      inbounds.map((r) => {
        const lead = r.remark.match(LEAD_FLAG)
        const server = r.serverId ? servers.find((s) => s.id === r.serverId) : undefined
        return { id: r.id, name: r.remark.replace(LEAD_FLAG, ''), flag: lead?.[0].trim() || server?.flag || '', where: server?.name ?? 'Главный сервер' }
      }),
    [inbounds, servers],
  )
  const needle = q.trim().toLowerCase()
  const shown = rows.filter((r) => !needle || `${r.name} ${r.where}`.toLowerCase().includes(needle))
  const chosen = rows.filter((r) => members.has(r.id)).length

  const flip = (id: string) =>
    setMembers((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  const addShown = () => setMembers((prev) => new Set([...prev, ...shown.map((r) => r.id)]))
  const dropShown = () => setMembers((prev) => new Set([...prev].filter((id) => !shown.some((r) => r.id === id))))

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      // Ids of connections that no longer exist are dropped here.
      await onSave({ id: initial?.id ?? '', name: name.trim(), country, enable: initial?.enable ?? true, members: rows.filter((r) => members.has(r.id)).map((r) => r.id), last: initial?.last ?? true, position: initial?.position ?? 0 })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={initial ? 'Автоузел' : 'Новый автоузел'} onClose={onClose} wide persistent>
      <form onSubmit={submit} className="grid gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Название" hint="Так запись называется в списке серверов приложения.">
            <Input required value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder="Авто РУ" autoFocus />
          </Field>
          <Field label="Флаг">
            <CountryPicker value={country} onChange={setCountry} emptyLabel="Без флага" />
          </Field>
        </div>

        <div className="grid gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-sm font-medium">Подключения в узле <span className="text-dim">· выбрано {chosen}</span></span>
            <div className="flex flex-wrap gap-2">
              <Button onClick={addShown}>Выбрать показанные</Button>
              <Button onClick={dropShown}>Снять показанные</Button>
            </div>
          </div>
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Найти подключение или сервер" />
          <div className="max-h-72 overflow-y-auto rounded-lg border border-line">
            {shown.length === 0 && <div className="px-3 py-6 text-center text-sm text-dim">Ничего не найдено</div>}
            {shown.map((r) => (
              <label key={r.id} className="flex cursor-pointer items-center gap-3 border-b border-line px-3 py-2 text-sm last:border-b-0 hover:bg-surface-2">
                <input type="checkbox" checked={members.has(r.id)} onChange={() => flip(r.id)} />
                <span className="w-6 text-center text-lg leading-none">{r.flag}</span>
                <span className="min-w-0 flex-1 truncate">{r.name}</span>
                <span className="shrink-0 text-xs text-faint">{r.where}</span>
              </label>
            ))}
          </div>
        </div>
        {chosen < 2 && <Alert tone="warn">Выберите хотя бы два подключения: из одного выбирать нечего, такой узел в подписку не попадёт.</Alert>}
        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="primary" disabled={busy || name.trim() === ''}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
        </div>
      </form>
    </Modal>
  )
}
