import { useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { Alert, Button, CopyButton, Field, Modal, Select, Textarea, Toggle } from '../components/ui'
import { JsonText } from './inbound/form'
import type { Saved, XrayProfile } from '../types'

const empty: XrayProfile = { logLevel: '', bittorrent: '', blockPrivate: false, domainStrategy: '', template: null }

const example = {
  outbounds: [{ tag: 'warp', protocol: 'socks', settings: { servers: [{ address: '127.0.0.1', port: 40000 }] } }],
  routing: { rules: [{ type: 'field', domain: ['geosite:netflix'], outboundTag: 'warp' }] },
  dns: { servers: ['1.1.1.1', 'https://dns.google/dns-query'], queryStrategy: 'UseIP' },
}

// Settings of one machine's Xray that are not inbounds: logging, routing options and a template merged into the
// generated config. id is "local" for the main server or a server id.
export default function XrayProfileModal({ id, title, onClose }: { id: string; title: string; onClose: () => void }) {
  const [p, setP] = useState<XrayProfile | null>(null)
  const [error, setError] = useState('')
  const [warning, setWarning] = useState('')
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const [config, setConfig] = useState('')

  useEffect(() => {
    api<XrayProfile>(`/api/xray-profile/${id}`).then((v) => setP({ ...empty, ...v }), (e) => setError(String(e instanceof Error ? e.message : e)))
  }, [id])

  async function save() {
    if (!p) return
    setBusy(true)
    setError('')
    setWarning('')
    setSaved(false)
    try {
      const res = await api<Saved<XrayProfile>>(`/api/xray-profile/${id}`, { method: 'PUT', body: p })
      setWarning(res.warning ?? '')
      setSaved(true)
      setConfig('')
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Не удалось сохранить')
    } finally {
      setBusy(false)
    }
  }

  async function preview() {
    setError('')
    try {
      const res = await fetch(`/api/xray/config/${id}`, { credentials: 'same-origin' })
      if (!res.ok) throw new Error(((await res.json().catch(() => ({}))) as { error?: string }).error ?? res.statusText)
      setConfig(JSON.stringify(await res.json(), null, 2))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const set = <K extends keyof XrayProfile>(k: K, v: XrayProfile[K]) => setP((cur) => (cur ? { ...cur, [k]: v } : cur))

  return (
    <Modal title={`Настройки Xray: ${title}`} onClose={onClose} wide persistent>
      {!p ? (
        error ? <Alert>{error}</Alert> : <p className="text-sm text-dim">Загрузка…</p>
      ) : (
        <div className="grid gap-4">
          <p className="text-sm text-dim">
            Всё, что относится к Xray этого сервера, кроме подключений (они настраиваются на странице «Подключения»). Изменения проверяются самим Xray и применяются сразу;
            на другой сервер настройки уходят в течение нескольких секунд.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Уровень журнала">
              <Select value={p.logLevel} onChange={(e) => set('logLevel', e.target.value)}>
                <option value="">по умолчанию (warning)</option>
                {['debug', 'info', 'warning', 'error', 'none'].map((l) => <option key={l} value={l}>{l}</option>)}
              </Select>
            </Field>
            <Field label="BitTorrent">
              <Select value={p.bittorrent} onChange={(e) => set('bittorrent', e.target.value as XrayProfile['bittorrent'])}>
                <option value="">по умолчанию (блокировать)</option>
                <option value="block">блокировать</option>
                <option value="allow">разрешить</option>
              </Select>
            </Field>
            <Field label="Стратегия доменов маршрутизации (domainStrategy)">
              <Select value={p.domainStrategy} onChange={(e) => set('domainStrategy', e.target.value as XrayProfile['domainStrategy'])}>
                <option value="">AsIs (по умолчанию)</option>
                <option value="IPIfNonMatch">IPIfNonMatch</option>
                <option value="IPOnDemand">IPOnDemand</option>
              </Select>
            </Field>
            <div className="flex items-end pb-2">
              <Toggle label="Не пускать клиентов в частные сети сервера (10.x, 192.168.x, 127.x…)" checked={p.blockPrivate} onChange={(v) => set('blockPrivate', v)} />
            </div>
          </div>

          <div>
            <JsonText
              label="Шаблон конфигурации (JSON)"
              rows={12}
              value={p.template ?? undefined}
              onChange={(v) => set('template', v && typeof v === 'object' && !Array.isArray(v) ? (v as XrayProfile['template']) : null)}
              hint="Накладывается на создаваемую панелью конфигурацию: outbounds добавляются (с тем же tag — заменяют), routing.rules ставятся перед остальными правилами, остальные поля (dns, policy, log…) дополняют или заменяют. inbounds здесь не задаются."
            />
            <div className="mt-2">
              <Button onClick={() => set('template', example)}>Вставить пример (outbound + правило + DNS)</Button>
            </div>
          </div>

          {error && <Alert>{error}</Alert>}
          {warning && <Alert tone="warn">{warning}</Alert>}
          {saved && !warning && <Alert tone="ok">Сохранено и применено.</Alert>}

          <div className="flex flex-wrap justify-end gap-2">
            <Button onClick={() => void preview()}>Показать итоговую конфигурацию</Button>
            <Button onClick={onClose}>Закрыть</Button>
            <Button variant="primary" disabled={busy} onClick={() => void save()}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button>
          </div>

          {config && (
            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-sm font-medium">Итоговая конфигурация Xray</span>
                <CopyButton text={config} />
              </div>
              <Textarea readOnly rows={16} value={config} />
              <p className="mt-1 text-xs text-dim">Содержит ключи и учётные данные клиентов: не показывайте её посторонним.</p>
            </div>
          )}
        </div>
      )}
    </Modal>
  )
}
