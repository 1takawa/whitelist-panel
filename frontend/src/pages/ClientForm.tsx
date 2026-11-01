import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { Alert, CopyButton, Button, Field, Input, Modal, Select, Toggle } from '../components/ui'
import type { Client, Inbound, Saved } from '../types'

const GB = 1024 ** 3

function emptyClient(): Client {
  return { id: '', email: '', subId: '', uuid: '', password: '', flow: 'xtls-rprx-vision', enable: true, totalBytes: 0, expiryAt: null, inboundIds: [], up: 0, down: 0, comment: '', trial: false, createdAt: '' }
}

// <input type="datetime-local"> works in local time without a zone.
function toLocalInput(iso: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function ClientForm({ initial, inbounds, onClose, onSaved }: { initial: Client | null; inbounds: Inbound[]; onClose: () => void; onSaved: (warning?: string) => void }) {
  // A new client starts on every connection (a switched-off one shows nothing until it is switched on): one without any
  // connection has nothing in its subscription.
  const [d, setD] = useState<Client>(initial ?? { ...emptyClient(), inboundIds: inbounds.map((i) => i.id) })
  const [limitGB, setLimitGB] = useState(initial && initial.totalBytes ? String(+(initial.totalBytes / GB).toFixed(2)) : '')
  const [expiry, setExpiry] = useState(toLocalInput(initial?.expiryAt ?? null))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const editing = initial !== null
  const touched = useRef(false) // the admin has changed the connection list by hand
  const allIds = inbounds.map((i) => i.id).join(",")
  // The connection list may arrive after the form opened: until the admin touches it, a new client follows it.
  useEffect(() => {
    if (!editing && !touched.current) setD((p) => ({ ...p, inboundIds: allIds ? allIds.split(',') : [] }))
  }, [editing, allIds])

  const toggleInbound = (id: string, on: boolean) => {
    touched.current = true
    setD((p) => ({ ...p, inboundIds: on ? [...p.inboundIds, id] : p.inboundIds.filter((x) => x !== id) }))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const { createdAt: _createdAt, up: _up, down: _down, ...rest } = d // server-managed fields are not sent
      const body = {
        ...rest,
        totalBytes: limitGB ? Math.round(Number(limitGB) * GB) : 0,
        expiryAt: expiry ? new Date(expiry).toISOString() : null,
      }
      const res = editing
        ? await api<Saved<Client>>(`/api/clients/${d.id}`, { method: 'PUT', body })
        : await api<Saved<Client>>('/api/clients', { method: 'POST', body })
      onSaved(res.warning)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Не удалось сохранить')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={editing ? `Клиент: ${initial.email}` : 'Новый клиент'} onClose={onClose}>
      <form onSubmit={submit} className="grid gap-3">
        <Field label="Имя" hint="Буквы, цифры и _ . @ + -. Используется для учёта трафика.">
          <Input required value={d.email} onChange={(e) => setD({ ...d, email: e.target.value })} autoFocus={!editing} />
        </Field>
        <Toggle label="Включён" checked={d.enable} onChange={(v) => setD({ ...d, enable: v })} />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Лимит трафика, ГБ" hint="Пусто — без лимита">
            <Input type="number" min={0} step="any" value={limitGB} onChange={(e) => setLimitGB(e.target.value)} />
          </Field>
          <Field label="Действует до" hint="Пусто — бессрочно">
            <Input type="datetime-local" value={expiry} onChange={(e) => setExpiry(e.target.value)} />
          </Field>
        </div>
        <Field label="Flow (VLESS)" hint="xtls-rprx-vision работает только для VLESS по TCP с TLS/REALITY">
          <Select value={d.flow} onChange={(e) => setD({ ...d, flow: e.target.value as Client['flow'] })}>
            <option value="">нет</option>
            <option value="xtls-rprx-vision">xtls-rprx-vision</option>
          </Select>
        </Field>

        <fieldset className="rounded-lg border border-line p-3">
          <legend className="px-1 text-sm font-medium">Подключения</legend>
          {inbounds.length === 0 && <p className="text-sm text-dim">Сначала создайте подключение.</p>}
          <div className="grid gap-2">
            {inbounds.map((i) => (
              <Toggle key={i.id} label={`${i.remark} — ${i.protocol.toUpperCase()} ${i.network}${i.enable ? "" : " (выключено)"}`} checked={d.inboundIds.includes(i.id)} onChange={(v) => toggleInbound(i.id, v)} />
            ))}
          </div>
        </fieldset>

        <div>
          <Toggle label="Тестовая подписка" checked={d.trial} onChange={(v) => setD({ ...d, trial: v })} />
          <p className="mt-1 text-xs text-dim">Влияет только на баннер в Happ: такой клиент видит баннер для тестовой подписки (вкладка «Подписка» → Happ). Срок и лимит задайте выше.</p>
        </div>
        <Field label="Комментарий"><Input value={d.comment} onChange={(e) => setD({ ...d, comment: e.target.value })} /></Field>

        {editing && (
          <dl className="grid gap-1 rounded-lg bg-surface-2 p-3 text-xs">
            {([['UUID (VLESS)', d.uuid], ['Пароль (Trojan)', d.password], ['ID подписки', d.subId]] as const).map(([k, v]) => (
              <div key={k} className="flex items-center justify-between gap-2">
                <dt className="text-dim">{k}</dt>
                <dd className="flex min-w-0 items-center gap-1"><code className="truncate">{v}</code><CopyButton text={v} label="Копировать" /></dd>
              </div>
            ))}
          </dl>
        )}

        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="primary" disabled={busy}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
        </div>
      </form>
    </Modal>
  )
}
