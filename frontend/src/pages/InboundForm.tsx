import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { Tabs } from '../components/Tabs'
import { Alert, Button, Field, Modal, Select } from '../components/ui'
import { useLoad } from '../hooks'
import type { Inbound, Saved } from '../types'
import AdvancedTab from './inbound/AdvancedTab'
import BasicsTab from './inbound/BasicsTab'
import { setPath } from './inbound/doc'
import { FormCtx, type FormState } from './inbound/form'
import ProtocolTab from './inbound/ProtocolTab'
import SecurityTab from './inbound/SecurityTab'
import { LinkTab, SniffingTab } from './inbound/SniffAndLinkTabs'
import StreamTab from './inbound/StreamTab'
import { emptyInbound, hydrate, presets, type Domains } from './inboundPresets'

type Props = {
  initial: Inbound | null // null = create
  settings: Domains
  onClose: () => void
  onSaved: (warning?: string) => void
}

const tabs = [
  { id: 'basics', label: 'Основное' },
  { id: 'protocol', label: 'Протокол' },
  { id: 'stream', label: 'Транспорт' },
  { id: 'security', label: 'Безопасность' },
  { id: 'sniffing', label: 'Sniffing' },
  { id: 'link', label: 'Ссылка' },
  { id: 'advanced', label: 'Дополнительно' },
]

export default function InboundForm({ initial, settings, onClose, onSaved }: Props) {
  const all = useLoad<Inbound[]>('/api/inbounds')
  const [d, setD] = useState<Inbound>(initial ? hydrate(initial) : emptyInbound())
  const [tab, setTab] = useState('basics')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const editing = initial !== null

  const ctx: FormState = {
    d,
    editing,
    others: all.data ?? [],
    set: (path, v) => setD((p) => setPath(p, path, v)),
    patch: (fn) => setD(fn),
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      // Server-managed fields are not sent: an empty createdAt would not parse as a time on the server.
      const { createdAt: _c, clients: _n, up: _u, down: _d, lastReset: _l, ...rest } = { ...d, port: Number(d.port) }
      const body = { ...rest, expiryAt: d.expiryAt || null }
      const res = editing
        ? await api<Saved<Inbound>>(`/api/inbounds/${d.id}`, { method: 'PUT', body })
        : await api<Saved<Inbound>>('/api/inbounds', { method: 'POST', body })
      onSaved(res.warning)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Не удалось сохранить')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={editing ? `Подключение: ${initial.remark}` : 'Новое подключение'} onClose={onClose} xwide persistent>
      <FormCtx.Provider value={ctx}>
        <form onSubmit={submit}>
          {!editing && (
            <div className="mb-4">
              <Field label="Шаблон" hint="Заполняет поля под вашу схему. Всё можно изменить на вкладках.">
                <Select defaultValue="" onChange={(e) => { const p = presets.find((x) => x.id === e.target.value); if (p) setD(p.build(settings)) }}>
                  <option value="">— выбрать —</option>
                  {presets.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}
                </Select>
              </Field>
            </div>
          )}

          <Tabs tabs={tabs} active={tab} onChange={setTab}>
            {tab === 'basics' && <BasicsTab onChanged={() => void all.reload()} />}
            {tab === 'protocol' && <ProtocolTab />}
            {tab === 'stream' && <StreamTab />}
            {tab === 'security' && <SecurityTab />}
            {tab === 'sniffing' && <SniffingTab />}
            {tab === 'link' && <LinkTab />}
            {tab === 'advanced' && <AdvancedTab />}
          </Tabs>

          {error && <div className="mt-4"><Alert>{error}</Alert></div>}
          <div className="sticky bottom-0 -mx-5 -mb-5 mt-5 flex justify-end gap-2 border-t border-line bg-surface px-5 py-3">
            <Button onClick={onClose}>Закрыть</Button>
            <Button type="submit" variant="primary" disabled={busy}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
          </div>
        </form>
      </FormCtx.Provider>
    </Modal>
  )
}
