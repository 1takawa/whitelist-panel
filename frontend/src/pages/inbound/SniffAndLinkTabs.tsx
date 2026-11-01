import { Alert, Field, Input, Select, Toggle } from '../../components/ui'
import { BTags, Grid, Section, useForm } from './form'

const kinds = ['http', 'tls', 'quic', 'fakedns']

export function SniffingTab() {
  const { d, set } = useForm()
  const s = d.sniffing
  const dest = s.destOverride?.length ? s.destOverride : ['http', 'tls', 'quic']
  return (
    <div>
      <Toggle label="Sniffing (определение протокола и домена по трафику)" checked={s.enabled} onChange={(v) => set('sniffing.enabled', v)} />
      {s.enabled && (
        <Section title="Параметры sniffing">
          <div className="sm:col-span-2">
            <span className="mb-1 block text-sm font-medium">Определять</span>
            <div className="flex flex-wrap gap-4">
              {kinds.map((k) => (
                <Toggle
                  key={k}
                  label={k.toUpperCase()}
                  checked={dest.includes(k)}
                  onChange={(on) => {
                    const next = on ? [...dest, k] : dest.filter((x) => x !== k)
                    set('sniffing.destOverride', next.length ? next : ['http', 'tls', 'quic'])
                  }}
                />
              ))}
            </div>
          </div>
          <Toggle label="Только метаданные (metadataOnly)" checked={s.metadataOnly} onChange={(v) => set('sniffing.metadataOnly', v)} />
          <Toggle label="Только для маршрутизации (routeOnly)" checked={s.routeOnly} onChange={(v) => set('sniffing.routeOnly', v)} />
          <div className="sm:col-span-2"><BTags path="sniffing.domainsExcluded" label="Исключённые домены" placeholder="example.com" hint="Для них адрес назначения не подменяется" /></div>
          <div className="sm:col-span-2"><BTags path="sniffing.ipsExcluded" label="Исключённые IP" placeholder="10.0.0.0/8" /></div>
        </Section>
      )}
    </div>
  )
}

// What the client link says: the front proxy may serve another address, port or TLS than Xray does.
export function LinkTab() {
  const { d, set } = useForm()
  const p = d.public
  return (
    <div>
      <Alert tone="ok">Здесь задаётся то, что попадает в ссылки клиентов, если оно отличается от настроек Xray: например, nginx принимает 443 и пересылает на порт Xray.</Alert>
      <Grid>
        <Field label="Адрес" hint="Пусто — домен из настроек (REALITY или панели) или адрес сервера">
          <Input value={p.host} onChange={(e) => set('public.host', e.target.value)} />
        </Field>
        <Field label="Порт" hint="0 — как порт Xray">
          <Input type="number" min={0} max={65535} value={p.port} onChange={(e) => set('public.port', Number(e.target.value) || 0)} />
        </Field>
        <Field label="TLS в ссылке">
          <Select value={p.security} onChange={(e) => set('public.security', e.target.value)}>
            <option value="">как на сервере</option>
            <option value="tls">TLS (добавляет фронт-прокси)</option>
            <option value="none">без TLS</option>
          </Select>
        </Field>
        <Field label="SNI" hint="Пусто — из настроек безопасности">
          <Input value={p.sni} onChange={(e) => set('public.sni', e.target.value)} />
        </Field>
        <Field label="ALPN" hint="Через запятую">
          <Input value={p.alpn} onChange={(e) => set('public.alpn', e.target.value)} placeholder="h2,http/1.1" />
        </Field>
        <Field label="Fingerprint (замена uTLS)" hint="Обычно пусто: берётся uTLS со вкладки «Безопасность»">
          <Input value={p.fingerprint} onChange={(e) => set('public.fingerprint', e.target.value)} />
        </Field>
      </Grid>
    </div>
  )
}
