import { useState } from 'react'
import { api } from '../../api'
import { Alert, Badge, Button, Field, Input, Select, Toggle } from '../../components/ui'
import { useLoad } from '../../hooks'
import { formatBytes, formatDate } from '../../lib'
import type { Inbound, Saved, ServerInfo } from '../../types'
import { GiB } from './doc'
import { Grid, useForm } from './form'

const toLocalInput = (iso: string | null) => {
  if (!iso) return ''
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function BasicsTab({ onChanged }: { onChanged: () => void }) {
  const { d, set, patch, editing } = useForm()
  const servers = useLoad<ServerInfo[]>('/api/servers')
  const [msg, setMsg] = useState('')

  // A server-side inbound is reached directly: no front proxy, so listen on all addresses and do not steal from the
  // local decoy site (127.0.0.1:…), which does not exist on the other machine.
  function setServer(serverId: string) {
    patch((p) => {
      const next: Inbound = { ...p, serverId: serverId || undefined }
      if (serverId) {
        next.listen = '0.0.0.0'
        next.public = { ...p.public, host: '', port: 0 }
        let ss = p.streamSettings
        const rs = ss.realitySettings as Record<string, unknown> | undefined
        if (ss.security === 'reality' && rs && String(rs.target ?? '').startsWith('127.0.0.1')) {
          ss = { ...ss, realitySettings: { ...rs, target: 'www.microsoft.com:443', serverNames: ['www.microsoft.com'] } }
        }
        const sock = ss.sockopt as Record<string, unknown> | undefined
        if (sock?.acceptProxyProtocol) {
          const { acceptProxyProtocol: _drop, ...rest } = sock
          ss = { ...ss, sockopt: rest }
        }
        next.streamSettings = ss
      }
      return next
    })
  }

  async function resetTraffic() {
    setMsg('')
    try {
      await api<Saved<Inbound>>(`/api/inbounds/${d.id}/reset-traffic`, { method: 'POST' })
      patch((p) => ({ ...p, up: 0, down: 0 }))
      setMsg('Счётчики подключения обнулены.')
      onChanged()
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e))
    }
  }

  const stopped = editing && ((d.totalBytes > 0 && d.up + d.down >= d.totalBytes) || (d.expiryAt !== null && new Date(d.expiryAt) <= new Date()))

  return (
    <div className="grid gap-3">
      <Grid>
        <Field label="Название"><Input required value={d.remark} onChange={(e) => set('remark', e.target.value)} /></Field>
        <div className="flex items-end pb-2"><Toggle label="Включено" checked={d.enable} onChange={(v) => set('enable', v)} /></div>

        <Field label="Протокол" hint={editing ? 'Протокол существующего подключения не меняется: создайте новое.' : undefined}>
          <Select value={d.protocol} disabled={editing} onChange={(e) => {
            const protocol = e.target.value as Inbound['protocol']
            patch((p) => ({
              ...p,
              protocol,
              settings: protocol === 'vless' ? { decryption: 'none', ...p.settings } : Object.fromEntries(Object.entries(p.settings).filter(([k]) => !['decryption', 'encryption', 'testseed'].includes(k))),
              streamSettings: protocol === 'trojan' && p.streamSettings.security === 'reality' ? { ...p.streamSettings, security: 'none' } : p.streamSettings,
            }))
          }}>
            <option value="vless">VLESS</option>
            <option value="trojan">Trojan</option>
          </Select>
        </Field>

        {(servers.data?.length ?? 0) > 0 ? (
          <Field label="Сервер" hint={d.serverId ? 'На другом сервере подключение работает напрямую, без nginx и Caddy: используйте VLESS + REALITY или TLS с сертификатом на самом сервере.' : undefined}>
            <Select value={d.serverId ?? ''} onChange={(e) => setServer(e.target.value)} disabled={editing}>
              <option value="">Этот сервер</option>
              {servers.data?.map((s) => <option key={s.id} value={s.id}>{s.name} ({s.address})</option>)}
            </Select>
          </Field>
        ) : <div />}

        <Field label="Адрес (listen)" hint="127.0.0.1 — если подключение за nginx/Caddy; 0.0.0.0 — слушать на всех адресах">
          <Input value={d.listen} onChange={(e) => set('listen', e.target.value)} placeholder="0.0.0.0" />
        </Field>
        <Field label="Порт Xray"><Input type="number" min={1} max={65535} required value={d.port || ''} onChange={(e) => set('port', Number(e.target.value))} /></Field>

        <Field label="Адрес в ссылке для клиентов" hint="Свой адрес и порт, которые попадают в ссылки, — на вкладке «Ссылка».">
          <Select value={d.shareAddress} onChange={(e) => set('shareAddress', e.target.value)}>
            <option value="">Автоматически (домен REALITY/панели или адрес сервера)</option>
            <option value="listen">Адрес прослушивания (Listen)</option>
          </Select>
        </Field>
        <Field label="Порядок в подписке" hint="Ссылки подписки идут по возрастанию этого числа.">
          <Input type="number" value={d.sortOrder} onChange={(e) => set('sortOrder', Number(e.target.value) || 0)} />
        </Field>

        <Toggle label="Не включать в подписки" checked={d.excludeFromSub} onChange={(v) => set('excludeFromSub', v)} />
        <Toggle label="Не включать в автоузел (останется отдельной записью)" checked={d.excludeFromAuto} onChange={(v) => set('excludeFromAuto', v)} />
        {d.protocol === 'vless' && <Toggle label="Отключить XTLS flow (клиентам не выдаётся xtls-rprx-vision)" checked={d.disableFlow} onChange={(v) => set('disableFlow', v)} />}
      </Grid>

      <fieldset className="mt-2 rounded-lg border border-line p-4">
        <legend className="px-1 text-sm font-semibold">Лимиты подключения</legend>
        <Grid>
          <Field label="Общий трафик, ГБ" hint="0 — без ограничения. При исчерпании подключение отключается для всех клиентов до сброса.">
            <Input type="number" min={0} step="any" value={d.totalBytes ? +(d.totalBytes / GiB).toFixed(3) : 0} onChange={(e) => set('totalBytes', Math.round((Number(e.target.value) || 0) * GiB))} />
          </Field>
          <Field label="Сброс трафика" hint="Счётчики обнуляются в начале часа, суток, недели (понедельник) или месяца.">
            <Select value={d.trafficReset || 'never'} onChange={(e) => set('trafficReset', e.target.value)}>
              <option value="never">Никогда</option>
              <option value="hourly">Каждый час</option>
              <option value="daily">Каждый день</option>
              <option value="weekly">Каждую неделю</option>
              <option value="monthly">Каждый месяц</option>
            </Select>
          </Field>
          <Field label="Действует до" hint="Пусто — бессрочно. После этого момента подключение отключается.">
            <Input type="datetime-local" value={toLocalInput(d.expiryAt)} onChange={(e) => set('expiryAt', e.target.value ? new Date(e.target.value).toISOString() : null)} />
          </Field>
          {editing && (
            <div className="flex flex-wrap items-end gap-2 pb-1 text-sm">
              <span>Использовано: ↑ {formatBytes(d.up)} ↓ {formatBytes(d.down)}</span>
              {d.lastReset && <span className="text-dim">сброс: {formatDate(d.lastReset)}</span>}
              {stopped && <Badge tone="warn">отключено лимитом</Badge>}
              <Button onClick={() => void resetTraffic()}>Обнулить</Button>
            </div>
          )}
        </Grid>
        {msg && <div className="mt-3"><Alert tone="ok">{msg}</Alert></div>}
      </fieldset>
    </div>
  )
}
