import { useState } from 'react'
import { api } from '../../api'
import { Alert, Button, CopyButton, Field, Input, Select, Textarea, Toggle } from '../../components/ui'
import type { Doc, ScanResult, Security } from '../../types'
import { randomPath, randomShortIds } from './doc'
import { BBool, BNum, BSel, BTags, BText, Full, Grid, Section, Tags, useForm, useVal } from './form'

const fingerprints = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']
const fpOptions: Array<[string, string]> = fingerprints.map((f) => [f, f])
const tlsVersions: Array<[string, string]> = ['1.0', '1.1', '1.2', '1.3'].map((v) => [v, v])
const realityNetworks = ['tcp', 'grpc', 'xhttp']

export const realityDefaults: Doc = {
  show: false,
  xver: 0,
  target: '',
  serverNames: [],
  privateKey: '',
  minClientVer: '',
  maxClientVer: '',
  maxTimediff: 0,
  shortIds: [],
  settings: { publicKey: '', fingerprint: 'firefox', spiderX: '/' },
}

export const tlsDefaults: Doc = {
  serverName: '',
  minVersion: '1.2',
  maxVersion: '1.3',
  alpn: ['h2', 'http/1.1'],
  certificates: [{ certificateFile: '', keyFile: '', usage: 'encipherment' }],
  settings: { fingerprint: 'firefox' },
}

// uTLS fingerprint: kept with the security settings; the link-level override of older releases is cleared by it.
function Fingerprint({ base }: { base: string }) {
  const { d, set } = useForm()
  const v = (d.streamSettings[base.includes('reality') ? 'realitySettings' : 'tlsSettings']?.settings?.fingerprint as string | undefined) ?? d.public.fingerprint ?? ''
  return (
    <Field label="uTLS (fingerprint клиента)">
      <Select
        value={v}
        onChange={(e) => {
          set(`${base}.settings.fingerprint`, e.target.value || undefined)
          set('public.fingerprint', '')
        }}
      >
        <option value="">не задан (firefox)</option>
        {fpOptions.map(([f]) => <option key={f} value={f}>{f}</option>)}
      </Select>
    </Field>
  )
}

function Reality() {
  const { d, set, editing } = useForm()
  const b = 'streamSettings.realitySettings'
  const [scan, setScan] = useState<ScanResult | null>(null)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [shortIds] = useVal<string[]>(`${b}.shortIds`)
  const [pub] = useVal<string>(`${b}.settings.publicKey`)
  const [seed, setSeed] = useVal<string>(`${b}.mldsa65Seed`)
  const [verify] = useVal<string>(`${b}.settings.mldsa65Verify`)
  const [target] = useVal<string>(`${b}.target`)
  const [names] = useVal<string[]>(`${b}.serverNames`)
  const [limits, setLimits] = useState(!!(d.streamSettings.realitySettings?.limitFallbackUpload || d.streamSettings.realitySettings?.limitFallbackDownload))

  async function run<T>(what: string, fn: () => Promise<T>, then: (r: T) => void) {
    setBusy(what)
    setErr('')
    try {
      then(await fn())
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <div>
      <Section title="REALITY">
        <BBool path={`${b}.show`} label="Show (отладочный вывод)" />
        <BNum path={`${b}.xver`} label="Xver" min={0} max={2} placeholder="0" hint="0, 1 или 2: передача PROXY protocol в Target" />
        <Fingerprint base={b} />
        <Full>
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-[14rem] flex-1">
              <BText path={`${b}.target`} label="Target" placeholder="127.0.0.1:7443 или example.com:443" hint="Сайт, под который маскируется сервер. Для схемы с nginx: 127.0.0.1:7443 (сайт на домене REALITY за Caddy)." />
            </div>
            <Button
              disabled={busy === 'scan' || !target}
              title="Проверить сайт с сервера панели: TLS 1.3, HTTP/2, сертификат"
              onClick={() => void run('scan', () => api<ScanResult>('/api/reality/scan', { method: 'POST', body: { target, serverName: names?.[0] ?? '' } }), setScan)}
            >
              {busy === 'scan' ? 'Проверяем…' : 'Проверить'}
            </Button>
          </div>
          {scan && (
            <div className="mt-2 grid gap-1 text-xs">
              <Alert tone={scan.ok ? 'ok' : 'warn'}>
                {scan.ok ? 'Подходит как Target: ' : 'Есть замечания: '}
                {scan.tls}, ALPN {scan.alpn || '—'}, ответ за {scan.latencyMs} мс{scan.issuer && `, выдал ${scan.issuer}`}
                {scan.warnings.length > 0 && <ul className="mt-1 list-disc pl-5">{scan.warnings.map((w) => <li key={w}>{w}</li>)}</ul>}
              </Alert>
              {scan.names.length > 0 && (
                <div className="flex flex-wrap items-center gap-1">
                  Имена в сертификате:
                  {scan.names.slice(0, 8).map((n) => (
                    <button key={n} type="button" className="rounded-md border border-line px-1.5 py-0.5 font-mono hover:bg-surface-2" onClick={() => set(`${b}.serverNames`, [...new Set([...(names ?? []), n.replace(/^\*\./, '')])])}>+ SNI {n}</button>
                  ))}
                </div>
              )}
            </div>
          )}
        </Full>
        <Full><BTags path={`${b}.serverNames`} label="SNI (serverNames)" placeholder="reality.example.com" hint="Домен REALITY этого сервера. Каждый сервер может иметь свой." /></Full>
        <BNum path={`${b}.maxTimediff`} label="Max time diff, мс" min={0} placeholder="0" />
        <span />
        <BText path={`${b}.minClientVer`} label="Min client ver" placeholder="x.y.z" />
        <BText path={`${b}.maxClientVer`} label="Max client ver" placeholder="x.y.z" />
        <Full>
          <span className="mb-1 block text-sm font-medium">Short IDs</span>
          <div className="flex items-start gap-2">
            <div className="flex-1"><Tags value={shortIds ?? []} placeholder="hex, 2–16 символов; пусто — создастся автоматически" onChange={(v) => set(`${b}.shortIds`, v)} /></div>
            <Button title="Сгенерировать 8 случайных short id" onClick={() => set(`${b}.shortIds`, randomShortIds())}>↻</Button>
          </div>
        </Full>
        <Full>
          <div className="flex items-end gap-2">
            <div className="flex-1"><BText path={`${b}.settings.spiderX`} label="SpiderX" placeholder="/" /></div>
            <Button title="Случайный путь" onClick={() => set(`${b}.settings.spiderX`, randomPath())}>↻</Button>
          </div>
        </Full>
        <Full>
          <Field label="Public key" hint="Вычисляется из приватного ключа при сохранении">
            <div className="flex items-center gap-2">
              <Input readOnly value={pub ?? ''} className="font-mono text-xs" />
              {pub && <CopyButton text={pub} />}
            </div>
          </Field>
        </Full>
        <Full>
          <BText path={`${b}.privateKey`} label="Private key" mono hint={editing ? 'Не меняйте, если не хотите сменить ключи у всех клиентов. Пусто — останется прежний.' : 'Пусто — создастся автоматически'} />
        </Full>
        <Full>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              disabled={busy === 'keys'}
              onClick={() =>
                void run('keys', () => api<{ privateKey: string; publicKey: string }>('/api/reality/keys'), (k) => {
                  set(`${b}.privateKey`, k.privateKey)
                  set(`${b}.settings.publicKey`, k.publicKey)
                })
              }
            >
              Новые ключи (Get New Cert)
            </Button>
            {!editing && <Button onClick={() => { set(`${b}.privateKey`, ''); set(`${b}.settings.publicKey`, '') }}>Очистить</Button>}
          </div>
        </Full>
        <Full>
          <Field label="ML-DSA-65 Seed" hint="Постквантовая подпись сертификата REALITY. Seed остаётся на сервере. Не проверено: на Xray 26.10.29 соединения с ML-DSA-65 не устанавливались даже без панели, включайте только после проверки клиентом.">
            <Textarea rows={2} value={seed ?? ''} onChange={(e) => setSeed(e.target.value || undefined)} />
          </Field>
        </Full>
        <Full>
          <Field label="ML-DSA-65 Verify" hint="Публичная часть: попадает в ссылки клиентов (pqv)">
            <Textarea rows={2} value={verify ?? ''} onChange={(e) => set(`${b}.settings.mldsa65Verify`, e.target.value || undefined)} />
          </Field>
        </Full>
        <Full>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              disabled={busy === 'pq'}
              onClick={() =>
                void run('pq', () => api<{ seed: string; verify: string }>('/api/xray/mldsa65', { method: 'POST' }), (k) => {
                  set(`${b}.mldsa65Seed`, k.seed)
                  set(`${b}.settings.mldsa65Verify`, k.verify)
                })
              }
            >
              Новый seed (Get New Seed)
            </Button>
            <Button onClick={() => { set(`${b}.mldsa65Seed`, undefined); set(`${b}.settings.mldsa65Verify`, undefined) }}>Очистить</Button>
          </div>
        </Full>
        <Full><BText path={`${b}.masterKeyLog`} label="Master key log" placeholder="/path/to/sslkeylog.txt" hint="Файл для расшифровки трафика в отладчике. Включайте только для диагностики." /></Full>
      </Section>
      {err && <div className="mt-3"><Alert>{err}</Alert></div>}

      <div className="mt-4">
        <Toggle label="Ограничение скорости fallback (Limit fallback)" checked={limits} onChange={(c) => { setLimits(c); if (!c) { set(`${b}.limitFallbackUpload`, undefined); set(`${b}.limitFallbackDownload`, undefined) } }} />
        {limits && (
          <>
            <Section title="Upload" hint="Сколько байт можно передать без ограничения, затем скорость в байтах в секунду.">
              <BNum path={`${b}.limitFallbackUpload.afterBytes`} label="После, байт" min={0} />
              <BNum path={`${b}.limitFallbackUpload.bytesPerSec`} label="Скорость, байт/с" min={0} />
              <BNum path={`${b}.limitFallbackUpload.burstBytesPerSec`} label="Всплеск, байт/с" min={0} />
            </Section>
            <Section title="Download">
              <BNum path={`${b}.limitFallbackDownload.afterBytes`} label="После, байт" min={0} />
              <BNum path={`${b}.limitFallbackDownload.bytesPerSec`} label="Скорость, байт/с" min={0} />
              <BNum path={`${b}.limitFallbackDownload.burstBytesPerSec`} label="Всплеск, байт/с" min={0} />
            </Section>
          </>
        )}
      </div>
    </div>
  )
}

type Cert = { certificateFile?: string; keyFile?: string; certificate?: string[]; key?: string[]; usage?: string; ocspStapling?: number; oneTimeLoading?: boolean; buildChain?: boolean }

function Certificates() {
  const [v, set] = useVal<Cert[]>('streamSettings.tlsSettings.certificates')
  const list = v ?? []
  const write = (next: Cert[]) => set(next)
  const change = (i: number, patch: Cert) => write(list.map((c, j) => (j === i ? { ...c, ...patch } : c)))
  return (
    <Full>
      <span className="mb-1 block text-sm font-medium">Сертификаты</span>
      <div className="grid gap-3">
        {list.map((c, i) => {
          const inline = c.certificate !== undefined || c.key !== undefined
          return (
            <div key={i} className="rounded-lg border border-line p-3">
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <Toggle
                  label="Вставить содержимое вместо путей к файлам"
                  checked={inline}
                  onChange={(on) =>
                    change(i, on ? { certificateFile: undefined, keyFile: undefined, certificate: [''], key: [''] } : { certificate: undefined, key: undefined, certificateFile: '', keyFile: '' })
                  }
                />
                {list.length > 1 && <button type="button" className="text-sm text-dim hover:text-danger" onClick={() => write(list.filter((_, j) => j !== i))}>Удалить</button>}
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                {inline ? (
                  <>
                    <Field label="Сертификат (PEM)"><Textarea rows={5} value={(c.certificate ?? []).join('\n')} onChange={(e) => change(i, { certificate: e.target.value.split('\n') })} /></Field>
                    <Field label="Ключ (PEM)"><Textarea rows={5} value={(c.key ?? []).join('\n')} onChange={(e) => change(i, { key: e.target.value.split('\n') })} /></Field>
                  </>
                ) : (
                  <>
                    <Field label="Файл сертификата"><Input value={c.certificateFile ?? ''} onChange={(e) => change(i, { certificateFile: e.target.value })} /></Field>
                    <Field label="Файл ключа"><Input value={c.keyFile ?? ''} onChange={(e) => change(i, { keyFile: e.target.value })} /></Field>
                  </>
                )}
                <Field label="Назначение (usage)">
                  <Select value={c.usage ?? 'encipherment'} onChange={(e) => change(i, { usage: e.target.value })}>
                    {['encipherment', 'verify', 'issue'].map((u) => <option key={u} value={u}>{u}</option>)}
                  </Select>
                </Field>
                <Field label="OCSP stapling, с">
                  <Input type="number" min={0} value={c.ocspStapling ?? ''} onChange={(e) => change(i, { ocspStapling: e.target.value === '' ? undefined : Number(e.target.value) })} />
                </Field>
                <Toggle label="Загрузить один раз (oneTimeLoading)" checked={c.oneTimeLoading === true} onChange={(on) => change(i, { oneTimeLoading: on || undefined })} />
                <Toggle label="Достроить цепочку (buildChain)" checked={c.buildChain === true} onChange={(on) => change(i, { buildChain: on || undefined })} />
              </div>
            </div>
          )
        })}
        <div><Button onClick={() => write([...list, { certificateFile: '', keyFile: '', usage: 'encipherment' }])}>+ Добавить сертификат</Button></div>
      </div>
    </Full>
  )
}

function TLS() {
  const { set } = useForm()
  const b = 'streamSettings.tlsSettings'
  const [name] = useVal<string>(`${b}.serverName`)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function ech() {
    setBusy(true)
    setErr('')
    try {
      const r = await api<{ echServerKeys: string; echConfigList: string }>('/api/xray/ech', { method: 'POST', body: { serverName: name } })
      set(`${b}.echServerKeys`, r.echServerKeys)
      set(`${b}.settings.echConfigList`, r.echConfigList)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <Section title="TLS">
        <BText path={`${b}.serverName`} label="SNI (serverName)" placeholder="example.com" />
        <Fingerprint base={b} />
        <BSel path={`${b}.minVersion`} label="Минимальная версия" empty="по умолчанию" options={tlsVersions} />
        <BSel path={`${b}.maxVersion`} label="Максимальная версия" empty="по умолчанию" options={tlsVersions} />
        <Full><BText path={`${b}.cipherSuites`} label="Cipher suites" placeholder="по умолчанию" hint="Имена наборов через двоеточие. Для TLS 1.3 не действует." /></Full>
        <Full><BTags path={`${b}.alpn`} label="ALPN" suggestions={['h3', 'h2', 'http/1.1']} /></Full>
        <Full><BTags path={`${b}.curvePreferences`} label="Curve preferences" placeholder="X25519MLKEM768" /></Full>
        <BBool path={`${b}.rejectUnknownSni`} label="Отклонять неизвестный SNI" />
        <BBool path={`${b}.disableSystemRoot`} label="Не использовать системные корневые сертификаты" />
        <BBool path={`${b}.enableSessionResumption`} label="Возобновление сессий (session resumption)" />
        <BBool path={`${b}.settings.allowInsecure`} label="Клиентам: allowInsecure" />
        <Certificates />
      </Section>
      <Section title="Encrypted Client Hello (ECH)">
        <Full><BText path={`${b}.echServerKeys`} label="ECH server keys" mono /></Full>
        <Full><BText path={`${b}.settings.echConfigList`} label="ECH config list (для клиентов)" mono /></Full>
        <div className="flex items-end gap-2">
          <Button variant="primary" disabled={busy || !name} title={name ? undefined : 'Сначала укажите SNI'} onClick={() => void ech()}>{busy ? 'Генерируем…' : 'Создать ECH-ключи'}</Button>
          <Button onClick={() => { set(`${b}.echServerKeys`, undefined); set(`${b}.settings.echConfigList`, undefined) }}>Очистить</Button>
        </div>
        <Full><BText path={`${b}.masterKeyLog`} label="Master key log" placeholder="/path/to/sslkeylog.txt" /></Full>
      </Section>
      {err && <div className="mt-3"><Alert>{err}</Alert></div>}
    </div>
  )
}

export default function SecurityTab() {
  const { d, patch } = useForm()
  const sec = (d.streamSettings.security ?? 'none') as Security
  const net = String(d.streamSettings.network ?? 'tcp')

  function setSecurity(s: Security) {
    patch((p) => {
      const ss: Doc = { ...p.streamSettings, security: s }
      if (s === 'reality' && !ss.realitySettings) ss.realitySettings = structuredClone(realityDefaults)
      if (s === 'tls' && !ss.tlsSettings) ss.tlsSettings = structuredClone(tlsDefaults)
      return { ...p, streamSettings: ss }
    })
  }

  const options: Array<[Security, string, boolean]> = [
    ['none', 'None', true],
    ['tls', 'TLS', true],
    ['reality', 'Reality', d.protocol === 'vless' && realityNetworks.includes(net)],
  ]

  return (
    <div>
      <div className="flex items-center gap-3">
        <span className="text-sm font-medium">Безопасность</span>
        <div role="group" aria-label="Безопасность" className="inline-flex overflow-hidden rounded-lg border border-line">
          {options.map(([v, t, ok]) => (
            <button
              key={v}
              type="button"
              disabled={!ok}
              aria-pressed={sec === v}
              title={ok ? undefined : v === 'reality' ? 'REALITY работает с VLESS поверх TCP, gRPC или XHTTP' : undefined}
              onClick={() => setSecurity(v)}
              className={`px-4 py-1.5 text-sm font-medium disabled:opacity-40 ${sec === v ? 'bg-accent text-accent-ink' : 'bg-surface hover:bg-surface-2'}`}
            >
              {t}
            </button>
          ))}
        </div>
      </div>
      {sec === 'none' && <Grid><p className="mt-4 text-sm text-dim sm:col-span-2">Xray не шифрует соединение сам. Так работают подключения за nginx/Caddy, которые терминируют TLS (см. вкладку «Ссылка»).</p></Grid>}
      {sec === 'reality' && <Reality />}
      {sec === 'tls' && <TLS />}
    </div>
  )
}
