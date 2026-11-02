import { useState } from 'react'
import { api, ApiError } from '../api'
import { useLoad } from '../hooks'
import type { RegRuDomain, RegRuGenerated, RegRuStatus } from '../types'
import { Alert, Button, Input, Select } from './ui'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

// DomainPicker is a domain field that, with a REG.RU account connected in Settings, can also pick one of the zone's
// existing subdomains or create a new one (an A record to the target address) with one button. target says which
// address the name must point at: an address or a name to resolve (host), a server of the panel (serverId) or, with
// neither, the main server. Without a connection it is a plain input.
export function DomainPicker({
  value, onChange, placeholder, target, targetReady = true, onCreated,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  target: { host?: string; serverId?: string }
  targetReady?: boolean // false while the address is not known yet (nothing to point the name at)
  onCreated?: (domain: string) => void // the panel just made this name: DNS may not show it for a few minutes
}) {
  const status = useLoad<RegRuStatus>('/api/regru')
  const connected = status.data?.connected === true
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null)
  const [domains, setDomains] = useState<RegRuDomain[] | null>(null)
  const [zone, setZone] = useState('') // '' = the default domain
  const zones = status.data ? [status.data.zone, ...(status.data.extraZones ?? [])] : []

  async function loadDomains() {
    if (domains !== null) return
    try {
      setDomains(await api<RegRuDomain[]>('/api/regru/domains'))
    } catch (e) {
      setDomains([])
      setNote({ ok: false, text: errText(e) })
    }
  }

  async function create() {
    setBusy(true)
    setNote(null)
    try {
      const g = await api<RegRuGenerated>('/api/regru/subdomain', { method: 'POST', body: { host: target.host ?? '', serverId: target.serverId ?? '', zone } })
      onChange(g.domain)
      setDomains(null)
      setNote({ ok: true, text: `Создана запись ${g.domain} → ${g.ip}. DNS обновляется до пары минут, поэтому сразу после создания нода может быть недоступна.` })
      onCreated?.(g.domain)
    } catch (e) {
      setNote({ ok: false, text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid gap-2">
      <div className="flex gap-2">
        <Input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} spellCheck={false} autoCapitalize="none" />
        {connected && zones.length > 1 && (
          <div className="w-44 shrink-0">
            <Select value={zone || zones[0]} onChange={(e) => setZone(e.target.value)} aria-label="Домен, в котором создать поддомен">
              {zones.map((z) => <option key={z} value={z}>.{z}</option>)}
            </Select>
          </div>
        )}
        {connected && (
          <Button variant="primary" disabled={busy || !targetReady} onClick={() => void create()} title={targetReady ? 'Создать поддомен в reg.ru и подставить его' : 'Сначала укажите адрес сервера'}>
            {busy ? 'Создаём…' : 'Создать'}
          </Button>
        )}
      </div>
      {connected && (
        <Select value="" onFocus={() => void loadDomains()} onChange={(e) => { if (e.target.value) onChange(e.target.value) }} aria-label="Выбрать из существующих поддоменов">
          <option value="">{domains === null ? 'Или выбрать из уже созданных в reg.ru…' : domains.length === 0 ? 'В зоне пока нет подходящих поддоменов' : 'Выберите поддомен…'}</option>
          {(domains ?? []).map((d) => <option key={d.name} value={d.name}>{d.name} — {d.ip}</option>)}
        </Select>
      )}
      {!connected && status.data && !status.loading && (
        <span className="text-xs text-dim">Подключите аккаунт reg.ru в Настройках, и домен можно будет создавать кнопкой.</span>
      )}
      {note && <Alert tone={note.ok ? 'ok' : 'bad'}>{note.text}</Alert>}
    </div>
  )
}
