import { useState } from 'react'
import { Alert, CopyButton } from '../../components/ui'
import type { Doc, Inbound } from '../../types'
import { JsonText, useForm } from './form'

type View = 'all' | 'settings' | 'stream' | 'sniffing'

const views: Array<[View, string]> = [
  ['all', 'Всё'],
  ['settings', 'Настройки'],
  ['stream', 'Транспорт'],
  ['sniffing', 'Sniffing'],
]

// What the panel keeps as typed fields: the Xray shape of "sniffing" and back.
const sniffOut = (s: Inbound['sniffing']) => ({
  enabled: s.enabled,
  destOverride: s.destOverride ?? [],
  metadataOnly: s.metadataOnly,
  routeOnly: s.routeOnly,
  domainsExcluded: s.domainsExcluded ?? [],
  ipsExcluded: s.ipsExcluded ?? [],
})

const isObject = (v: unknown): v is Doc => v !== null && typeof v === 'object' && !Array.isArray(v)

// The same JSON Xray-панелей shows: the whole inbound as Xray sees it (without the clients, which the panel manages).
export default function AdvancedTab() {
  const { d, patch } = useForm()
  const [view, setView] = useState<View>('all')
  const [err, setErr] = useState('')

  const all = {
    listen: d.listen,
    port: d.port,
    protocol: d.protocol,
    tag: d.id ? `in-${d.id}` : undefined,
    settings: d.settings,
    sniffing: sniffOut(d.sniffing),
    streamSettings: d.streamSettings,
  }
  const text = JSON.stringify(all, null, 2)

  function applyAll(v: unknown) {
    if (!isObject(v)) return setErr('Ожидается JSON-объект')
    if (v.protocol !== undefined && v.protocol !== 'vless' && v.protocol !== 'trojan') return setErr('Поддерживаются протоколы vless и trojan')
    setErr('')
    patch((p) => {
      const n = { ...p }
      if (typeof v.listen === 'string') n.listen = v.listen
      if (typeof v.port === 'number') n.port = v.port
      if (!p.id && (v.protocol === 'vless' || v.protocol === 'trojan')) n.protocol = v.protocol
      if (isObject(v.settings)) {
        const { clients: _drop, ...rest } = v.settings
        n.settings = rest
      }
      if (isObject(v.streamSettings)) n.streamSettings = v.streamSettings
      if (isObject(v.sniffing)) n.sniffing = { ...p.sniffing, ...v.sniffing } as Inbound['sniffing']
      return n
    })
  }

  return (
    <div>
      <p className="mb-3 text-sm text-dim">
        Полный JSON подключения в формате Xray. Любая опция Xray, для которой нет поля в форме, задаётся здесь; изменения сразу видны на остальных вкладках.
        Сервер проверит итоговую конфигурацию самим Xray перед сохранением.
      </p>
      <div role="tablist" aria-label="Часть JSON" className="mb-3 flex gap-1 border-b border-line">
        {views.map(([v, t]) => (
          <button
            key={v}
            type="button"
            role="tab"
            aria-selected={view === v}
            onClick={() => { setView(v); setErr('') }}
            className={`-mb-px border-b-2 px-3 py-1.5 text-sm font-medium ${view === v ? 'border-accent text-ink' : 'border-transparent text-dim hover:text-ink'}`}
          >
            {t}
          </button>
        ))}
        <span className="ml-auto self-center"><CopyButton text={view === 'all' ? text : JSON.stringify(view === 'settings' ? d.settings : view === 'stream' ? d.streamSettings : sniffOut(d.sniffing), null, 2)} /></span>
      </div>
      {view === 'all' && <JsonText rows={22} value={all} onChange={applyAll} />}
      {view === 'settings' && <JsonText rows={22} value={d.settings} hint="Настройки протокола: decryption, encryption, testseed, fallbacks" onChange={(v) => isObject(v) && patch((p) => ({ ...p, settings: v }))} />}
      {view === 'stream' && <JsonText rows={22} value={d.streamSettings} hint="network, security, настройки транспорта и безопасности, sockopt, finalmask" onChange={(v) => isObject(v) && patch((p) => ({ ...p, streamSettings: v }))} />}
      {view === 'sniffing' && <JsonText rows={12} value={sniffOut(d.sniffing)} onChange={(v) => isObject(v) && patch((p) => ({ ...p, sniffing: { ...p.sniffing, ...v } as Inbound['sniffing'] }))} />}
      {err && <div className="mt-2"><Alert>{err}</Alert></div>}
    </div>
  )
}
