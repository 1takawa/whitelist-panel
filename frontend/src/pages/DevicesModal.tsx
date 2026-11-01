import { useState } from 'react'
import { api } from '../api'
import { Icon } from '../components/Icon'
import { ask } from '../components/confirm'
import { Alert, Badge, CopyIcon, IconButton, Modal } from '../components/ui'
import type { Client, Device } from '../types'

const when = (iso: string) => new Date(iso).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' })

// "Happ/4.12.0/ios/2606121423535" -> "Happ 4.12.0"; anything else is shown as it came.
const appName = (ua: string) => {
  const m = ua.match(/^([A-Za-z][\w .-]*?)\/([\w.]+)/)
  return m ? `${m[1]} ${m[2]}` : ua
}

// The devices (HWID) that asked for a client's subscription. A blocked device gets a subscription without servers the next
// time its app refreshes it; forgetting a device only clears the record (it comes back if the app asks again).
export default function DevicesModal({ client, onClose, onChanged }: { client: Client; onClose: () => void; onChanged: () => void }) {
  const [devices, setDevices] = useState<Device[]>(client.devices ?? [])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  async function run(hwid: string, fn: () => Promise<Client>) {
    setBusy(hwid)
    setError('')
    try {
      setDevices((await fn()).devices ?? [])
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }
  const base = (hwid: string) => `/api/clients/${client.id}/devices/${encodeURIComponent(hwid)}`
  const sorted = [...devices].sort((a, b) => new Date(b.lastSeen).getTime() - new Date(a.lastSeen).getTime())

  return (
    <Modal title={`Устройства: ${client.email}`} onClose={onClose} wide>
      {error && <div className="mb-3"><Alert>{error}</Alert></div>}
      {sorted.length === 0 ? (
        <p className="py-10 text-center text-sm text-faint">Устройств пока нет</p>
      ) : (
        <ul className="grid gap-2.5">
          {sorted.map((d) => (
            <li key={d.hwid} className="ap-row flex items-center gap-3.5 rounded-2xl border border-line bg-surface-2/50 px-4 py-3">
              <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-surface text-dim"><Icon name="phone" /></span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2 font-semibold">
                  <span className="truncate">{d.model || 'Неизвестное устройство'}</span>
                  {d.blocked && <Badge tone="bad">Заблокировано</Badge>}
                </div>
                <div className="mt-0.5 truncate text-[13px] text-faint">
                  {[[d.os, d.osVersion].filter(Boolean).join(' '), d.app && appName(d.app), `${when(d.lastSeen)}${d.ip ? `, ${d.ip}` : ''}`].filter(Boolean).join(' · ')}
                </div>
                <div className="mt-0.5 flex items-center gap-1 text-xs text-faint">
                  <span className="truncate font-mono" title="HWID">{d.hwid}</span>
                  <CopyIcon text={d.hwid} label="Копировать HWID" />
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-0.5">
                <IconButton
                  icon="ban"
                  label={d.blocked ? 'Разблокировать' : 'Заблокировать'}
                  disabled={busy === d.hwid}
                  className={d.blocked ? 'text-blocked' : ''}
                  onClick={() => void run(d.hwid, () => api<Client>(`${base(d.hwid)}/block`, { method: 'POST', body: { blocked: !d.blocked } }))}
                />
                <IconButton
                  icon="trash"
                  label="Удалить"
                  tone="danger"
                  disabled={busy === d.hwid}
                  onClick={async () => (await ask('Удалить устройство из списка?', { confirmLabel: 'Удалить' })) && void run(d.hwid, () => api<Client>(base(d.hwid), { method: 'DELETE' }))}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
    </Modal>
  )
}
