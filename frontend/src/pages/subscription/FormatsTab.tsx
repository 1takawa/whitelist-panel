import { Card, Field, Input, Textarea, Toggle } from '../../components/ui'
import type { FormatSettings } from '../../types'

export default function FormatsTab({ value: f, onChange, example }: { value: FormatSettings; onChange: (patch: Partial<FormatSettings>) => void; example: (path: string) => string }) {
  const json = (key: keyof FormatSettings, label: string, hint: string, placeholder: string) => (
    <Field label={label} hint={hint}>
      <Textarea rows={3} value={f[key] as string} placeholder={placeholder} spellCheck={false} onChange={(e) => onChange({ [key]: e.target.value } as Partial<FormatSettings>)} />
    </Field>
  )
  return (
    <div className="grid gap-5">
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Clash / Mihomo</h2>
        <Toggle label="Отдавать профиль Clash" checked={f.clashEnable} onChange={(v) => onChange({ clashEnable: v })} />
        {f.clashEnable && (
          <>
            <Field label="Путь" hint={`Пример адреса: ${example(f.clashPath || '/clash/')}`}>
              <Input value={f.clashPath} onChange={(e) => onChange({ clashPath: e.target.value })} placeholder="/clash/" />
            </Field>
            <div>
              <Toggle label="Определять приложение по User-Agent на основном адресе подписки" checked={f.clashAutoDetect} onChange={(v) => onChange({ clashAutoDetect: v })} />
              {f.clashAutoDetect && (
                <div className="mt-2">
                  <Field label="Шаблон User-Agent (регулярное выражение)">
                    <Input value={f.clashUaRegex} onChange={(e) => onChange({ clashUaRegex: e.target.value })} placeholder="(?i)(clash|mihomo|stash)" />
                  </Field>
                </div>
              )}
            </div>
            <Toggle label="Удалённый DNS (DoH, fake-ip)" checked={f.clashRemoteDns} onChange={(v) => onChange({ clashRemoteDns: v })} />
            <div>
              <Toggle label="Свои правила маршрутизации" checked={f.clashEnableRouting} onChange={(v) => onChange({ clashEnableRouting: v })} />
              {f.clashEnableRouting && (
                <div className="mt-2">
                  <Field label="Правила" hint="По одному в строке: ТИП,ЗНАЧЕНИЕ,ЦЕЛЬ. Цели: DIRECT, REJECT или Proxy. Идут перед встроенными (локальная сеть напрямую, остальное через Proxy).">
                    <Textarea rows={6} value={f.clashRules} placeholder={'DOMAIN-SUFFIX,ru,DIRECT\nGEOIP,RU,DIRECT'} spellCheck={false} onChange={(e) => onChange({ clashRules: e.target.value })} />
                  </Field>
                </div>
              )}
            </div>
            <p className="text-xs text-dim">XHTTP в профиле требует свежей версии Mihomo; для других клиентов используйте подключения с другим транспортом.</p>
          </>
        )}
      </Card>

      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Xray JSON</h2>
        <Toggle label="Отдавать конфигурации Xray (JSON)" checked={f.jsonEnable} onChange={(v) => onChange({ jsonEnable: v })} />
        {f.jsonEnable && (
          <>
            <Field label="Путь" hint={`Пример адреса: ${example(f.jsonPath || '/json/')}`}>
              <Input value={f.jsonPath} onChange={(e) => onChange({ jsonPath: e.target.value })} placeholder="/json/" />
            </Field>
            <div>
              <Toggle label="Определять приложение по User-Agent на основном адресе подписки" checked={f.jsonAutoDetect} onChange={(v) => onChange({ jsonAutoDetect: v })} />
              {f.jsonAutoDetect && (
                <div className="mt-2">
                  <Field label="Шаблон User-Agent (регулярное выражение)" hint="Обязателен, если определение включено. Clash проверяется раньше.">
                    <Input value={f.jsonUaRegex} onChange={(e) => onChange({ jsonUaRegex: e.target.value })} placeholder="(?i)(streisand|v2box)" />
                  </Field>
                </div>
              )}
            </div>
            <Toggle label="Всегда отдавать массив, даже если конфигурация одна" checked={f.jsonAlwaysArray} onChange={(v) => onChange({ jsonAlwaysArray: v })} />
            <div className="grid gap-3 sm:grid-cols-2">
              {json('jsonMux', 'Мультиплексирование (mux)', 'JSON-объект. Не применяется к ссылкам с flow xtls-rprx-vision.', '{"enabled":true,"concurrency":8}')}
              {json('jsonFragment', 'Фрагментация', 'JSON-объект. Только для TLS и REALITY.', '{"packets":"tlshello","length":"100-200","interval":"10-20"}')}
              {json('jsonNoises', 'Шум (noises)', 'JSON-массив. Для UDP.', '[{"type":"rand","packet":"10-20","delay":"10-16"}]')}
              {json('jsonDns', 'DNS', 'JSON-объект: заменяет раздел dns. По умолчанию 1.1.1.1 и 8.8.8.8.', '{"servers":["https://1.1.1.1/dns-query"]}')}
            </div>
            {json('jsonRules', 'Свои правила маршрутизации', 'JSON-массив правил Xray. Идут перед встроенным правилом «локальная сеть напрямую».', '[{"type":"field","domain":["domain:ru"],"outboundTag":"direct"}]')}
          </>
        )}
      </Card>
    </div>
  )
}
