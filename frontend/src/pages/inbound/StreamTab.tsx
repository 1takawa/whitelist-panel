import { Button, Field, Select, Toggle } from '../../components/ui'
import type { Doc, Network } from '../../types'
import { randomPath } from './doc'
import { BBool, BHeaders, BNum, BSel, BTags, BText, Full, Grid, JsonText, Section, useForm, useVal } from './form'

const transports: Array<[Network, string]> = [
  ['tcp', 'RAW (TCP)'],
  ['ws', 'WebSocket'],
  ['grpc', 'gRPC'],
  ['xhttp', 'XHTTP'],
  ['httpupgrade', 'HTTPUpgrade'],
  ['kcp', 'mKCP'],
]

export const transportDefaults: Record<Network, [string, Doc]> = {
  tcp: ['tcpSettings', { header: { type: 'none' } }],
  ws: ['wsSettings', { path: '/', headers: {} }],
  grpc: ['grpcSettings', { serviceName: '', authority: '', multiMode: false }],
  xhttp: ['xhttpSettings', { path: '/', host: '', mode: 'auto' }],
  httpupgrade: ['httpupgradeSettings', { path: '/', host: '' }],
  kcp: ['kcpSettings', { mtu: 1350, tti: 50, uplinkCapacity: 5, downlinkCapacity: 20 }],
}

const networkOf = (d: { streamSettings: Doc }): Network => {
  const n = String(d.streamSettings.network ?? 'tcp')
  return (n === 'raw' ? 'tcp' : n === 'mkcp' ? 'kcp' : n) as Network
}

const proxyKeys = ['tcpSettings', 'rawSettings', 'wsSettings', 'httpupgradeSettings', 'sockopt']

const httpHeader = {
  type: 'http',
  request: {
    version: '1.1',
    method: 'GET',
    path: ['/'],
    headers: {
      Host: [''],
      'User-Agent': ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'],
      'Accept-Encoding': ['gzip, deflate'],
      Connection: ['keep-alive'],
      Pragma: 'no-cache',
    },
  },
  response: {
    version: '1.1',
    status: '200',
    reason: 'OK',
    headers: { 'Content-Type': ['application/octet-stream', 'video/mpeg'], 'Transfer-Encoding': ['chunked'], Connection: ['keep-alive'], Pragma: 'no-cache' },
  },
}

const tcpMaskTypes = ['fragment', 'sudoku', 'header-custom']
const udpMaskTypes = ['mkcp-original', 'mkcp-aes128gcm', 'header-dns', 'header-dtls', 'header-srtp', 'header-utp', 'header-wechat', 'header-wireguard', 'noise', 'salamander', 'xdns', 'xicmp']

type Mask = { type: string; settings?: Doc }

// Transport masks (finalmask): obfuscation layers stacked over the connection.
function Masks({ path, label, types }: { path: string; label: string; types: string[] }) {
  const [v, set] = useVal<Mask[]>(path)
  const list = v ?? []
  const write = (next: Mask[]) => set(next.length ? next : undefined)
  return (
    <Full>
      <span className="mb-1 block text-sm font-medium">{label}</span>
      <div className="grid gap-2">
        {list.map((m, i) => (
          <div key={i} className="rounded-lg border border-line p-2">
            <div className="flex items-center gap-2">
              <Select value={m.type} onChange={(e) => write(list.map((x, j) => (j === i ? { ...x, type: e.target.value } : x)))}>
                {!types.includes(m.type) && <option value={m.type}>{m.type}</option>}
                {types.map((t) => <option key={t} value={t}>{t}</option>)}
              </Select>
              <button type="button" aria-label="Удалить маску" className="px-2 text-dim hover:text-danger" onClick={() => write(list.filter((_, j) => j !== i))}>×</button>
            </div>
            <div className="mt-2">
              <JsonText rows={4} value={m.settings} hint="Настройки маски (JSON); пусто — по умолчанию" onChange={(s) => write(list.map((x, j) => (j === i ? { ...x, settings: s as Doc | undefined } : x)))} />
            </div>
          </div>
        ))}
        <div>
          <Button onClick={() => write([...list, { type: types[0] }])} aria-label={`Добавить: ${label}`}>+</Button>
        </div>
      </div>
    </Full>
  )
}

function Obfuscation() {
  const { d, set } = useForm()
  const on = ((d.streamSettings.tcpSettings as Doc | undefined)?.header as Doc | undefined)?.type === 'http'
  const base = 'streamSettings.tcpSettings.header'
  return (
    <>
      <Toggle label="HTTP-маскировка (HTTP Obfuscation)" checked={on} onChange={(c) => set(base, c ? httpHeader : { type: 'none' })} />
      {on && (
        <Section title="HTTP-маскировка: запрос и ответ">
          <BText path={`${base}.request.version`} label="Версия запроса" />
          <BText path={`${base}.request.method`} label="Метод" />
          <Full><BTags path={`${base}.request.path`} label="Пути запроса" placeholder="/" /></Full>
          <BHeaders path={`${base}.request.headers`} label="Заголовки запроса" />
          <BText path={`${base}.response.version`} label="Версия ответа" />
          <BText path={`${base}.response.status`} label="Статус ответа" />
          <BText path={`${base}.response.reason`} label="Причина (reason)" />
          <span />
          <BHeaders path={`${base}.response.headers`} label="Заголовки ответа" />
        </Section>
      )}
    </>
  )
}

function Sockopt() {
  const { d, set } = useForm()
  const on = d.streamSettings.sockopt !== undefined
  const p = 'streamSettings.sockopt'
  return (
    <>
      <Toggle label="Sockopt (параметры сокета)" checked={on} onChange={(c) => set(p, c ? {} : undefined)} />
      {on && (
        <Section title="Sockopt">
          <BNum path={`${p}.mark`} label="Mark (SO_MARK)" />
          <BNum path={`${p}.tcpMaxSeg`} label="TCP max segment" />
          <BBool path={`${p}.tcpFastOpen`} label="TCP Fast Open" />
          <BSel path={`${p}.tproxy`} label="TProxy" empty="off" options={[['redirect', 'redirect'], ['tproxy', 'tproxy']]} />
          <BSel path={`${p}.domainStrategy`} label="Domain strategy" empty="AsIs" options={['UseIP', 'UseIPv6v4', 'UseIPv6', 'UseIPv4v6', 'UseIPv4'].map((x) => [x, x])} />
          <BText path={`${p}.dialerProxy`} label="Dialer proxy" />
          <BNum path={`${p}.tcpKeepAliveInterval`} label="TCP keep-alive interval, с" />
          <BNum path={`${p}.tcpKeepAliveIdle`} label="TCP keep-alive idle, с" />
          <BNum path={`${p}.tcpUserTimeout`} label="TCP user timeout, мс" />
          <BSel path={`${p}.tcpcongestion`} label="TCP congestion" empty="по умолчанию" options={['bbr', 'cubic', 'reno'].map((x) => [x, x])} />
          <BText path={`${p}.interface`} label="Interface" />
          <BNum path={`${p}.tcpWindowClamp`} label="TCP window clamp" />
          <BBool path={`${p}.V6Only`} label="V6 only" />
          <BBool path={`${p}.tcpMptcp`} label="Multipath TCP" />
          <BBool path={`${p}.penetrate`} label="Penetrate" />
          <BBool path={`${p}.acceptProxyProtocol`} label="Accept PROXY protocol" />
          <Full><BTags path={`${p}.trustedXForwardedFor`} label="Trusted X-Forwarded-For" placeholder="CF-Connecting-IP" /></Full>
        </Section>
      )}
    </>
  )
}

function Transport({ net }: { net: Network }) {
  switch (net) {
    case 'tcp':
      return <Obfuscation />
    case 'ws':
      return (
        <Section title="WebSocket">
          <BText path="streamSettings.wsSettings.path" label="Путь" />
          <BText path="streamSettings.wsSettings.host" label="Host" />
          <BNum path="streamSettings.wsSettings.heartbeatPeriod" label="Heartbeat, с" />
          <span />
          <BHeaders path="streamSettings.wsSettings.headers" label="Заголовки" />
        </Section>
      )
    case 'httpupgrade':
      return (
        <Section title="HTTPUpgrade">
          <BText path="streamSettings.httpupgradeSettings.path" label="Путь" />
          <BText path="streamSettings.httpupgradeSettings.host" label="Host" />
          <BHeaders path="streamSettings.httpupgradeSettings.headers" label="Заголовки" />
        </Section>
      )
    case 'grpc':
      return (
        <Section title="gRPC">
          <BText path="streamSettings.grpcSettings.serviceName" label="Имя сервиса" />
          <BText path="streamSettings.grpcSettings.authority" label="Authority" />
          <BBool path="streamSettings.grpcSettings.multiMode" label="Multi mode" />
        </Section>
      )
    case 'xhttp': {
      const x = 'streamSettings.xhttpSettings'
      return (
        <>
          <Section title="XHTTP">
            <BText path={`${x}.path`} label="Путь" />
            <BText path={`${x}.host`} label="Host" />
            <BSel path={`${x}.mode`} label="Режим" options={['auto', 'packet-up', 'stream-up', 'stream-one'].map((m) => [m, m])} />
            <BSel path={`${x}.uplinkHTTPMethod`} label="Метод upload" empty="POST" options={[['PUT', 'PUT'], ['GET', 'GET']]} />
            <BHeaders path={`${x}.headers`} label="Заголовки" />
          </Section>
          <Section title="XHTTP: размеры и интервалы">
            <BText path={`${x}.xPaddingBytes`} label="Padding, байт" placeholder="100-1000" />
            <BBool path={`${x}.noSSEHeader`} label="Без SSE-заголовка (noSSEHeader)" />
            <BNum path={`${x}.scMaxBufferedPosts`} label="Макс. буфер POST" placeholder="30" />
            <BText path={`${x}.scMaxEachPostBytes`} label="Макс. размер POST, байт" placeholder="1000000" />
            <BText path={`${x}.scMinPostsIntervalMs`} label="Мин. интервал POST, мс" placeholder="30" />
            <BText path={`${x}.scStreamUpServerSecs`} label="Stream-up keep-alive, с" placeholder="20-80" />
            <BNum path={`${x}.serverMaxHeaderBytes`} label="Макс. размер заголовков, байт" />
            <BNum path={`${x}.uplinkChunkSize`} label="Размер куска upload, байт" />
          </Section>
          <Section title="XHTTP: где передаются служебные поля" hint="Опции маскировки трафика. Оставьте пустым, если не знаете, зачем они нужны: клиенты получают их через параметр extra ссылки.">
            <BBool path={`${x}.xPaddingObfsMode`} label="Обфускация padding" />
            <BText path={`${x}.xPaddingKey`} label="Ключ padding" placeholder="x_padding" />
            <BText path={`${x}.xPaddingHeader`} label="Заголовок padding" placeholder="X-Padding" />
            <BSel path={`${x}.xPaddingPlacement`} label="Место padding" empty="по умолчанию" options={['queryInHeader', 'cookie', 'header', 'query'].map((v) => [v, v])} />
            <BSel path={`${x}.xPaddingMethod`} label="Способ padding" empty="по умолчанию" options={['repeat-x', 'tokenish'].map((v) => [v, v])} />
            <BSel path={`${x}.sessionIDPlacement`} label="Место session id" empty="path" options={['path', 'header', 'cookie', 'query'].map((v) => [v, v])} />
            <BText path={`${x}.sessionIDKey`} label="Ключ session id" />
            <BSel path={`${x}.seqPlacement`} label="Место номера пакета" empty="path" options={['path', 'header', 'cookie', 'query'].map((v) => [v, v])} />
            <BText path={`${x}.seqKey`} label="Ключ номера пакета" />
            <BSel path={`${x}.uplinkDataPlacement`} label="Место данных upload" empty="body" options={['body', 'auto', 'header', 'cookie'].map((v) => [v, v])} />
            <BText path={`${x}.uplinkDataKey`} label="Ключ данных upload" />
          </Section>
        </>
      )
    }
    case 'kcp':
      return (
        <Section title="mKCP">
          <BNum path="streamSettings.kcpSettings.mtu" label="MTU" placeholder="1350" />
          <BNum path="streamSettings.kcpSettings.tti" label="TTI, мс" placeholder="50" />
          <BNum path="streamSettings.kcpSettings.uplinkCapacity" label="Uplink, МБ/с" placeholder="5" />
          <BNum path="streamSettings.kcpSettings.downlinkCapacity" label="Downlink, МБ/с" placeholder="20" />
        </Section>
      )
  }
}

export default function StreamTab() {
  const { d, patch } = useForm()
  const net = networkOf(d)

  function setNetwork(n: Network) {
    patch((p) => {
      const [key, def] = transportDefaults[n]
      let ss: Doc = { ...p.streamSettings, network: n }
      if (!ss[key]) ss[key] = n === 'ws' || n === 'httpupgrade' || n === 'xhttp' ? { ...def, path: randomPath() } : def
      if (ss.security === 'reality' && !['tcp', 'grpc', 'xhttp'].includes(n)) ss = { ...ss, security: 'none' }
      return { ...p, streamSettings: ss }
    })
  }

  const proxy = proxyKeys.some((k) => (d.streamSettings[k] as Doc | undefined)?.acceptProxyProtocol === true)
  function setProxy(on: boolean) {
    patch((p) => {
      const ss: Doc = { ...p.streamSettings }
      for (const k of proxyKeys) {
        if (ss[k] && typeof ss[k] === 'object') {
          const { acceptProxyProtocol: _drop, ...rest } = ss[k] as Doc
          ss[k] = rest
        }
      }
      if (ss.sockopt && Object.keys(ss.sockopt).length === 0) delete ss.sockopt
      if (on) ss.sockopt = { ...(ss.sockopt ?? {}), acceptProxyProtocol: true }
      return { ...p, streamSettings: ss }
    })
  }

  return (
    <div>
      <Grid>
        <Field label="Транспорт (Transmission)">
          <Select value={net} onChange={(e) => setNetwork(e.target.value as Network)}>
            {transports.map(([v, t]) => <option key={v} value={v}>{t}</option>)}
          </Select>
        </Field>
        <div className="flex items-end pb-2">
          <Toggle label="PROXY protocol (если перед Xray стоит nginx)" checked={proxy} onChange={setProxy} />
        </div>
      </Grid>
      <Transport net={net} />
      <div className="mt-4 grid gap-3">
        <Sockopt />
        <Section title="Маски транспорта (TCP Masks / finalmask)" hint="Дополнительная маскировка поверх соединения. Клиенты получают маски через параметр fm ссылки. Маска fragment — клиентская: на подключении её задать нельзя.">
          <Masks path="streamSettings.finalmask.tcp" label="TCP-маски" types={tcpMaskTypes} />
          {net === 'kcp' && <Masks path="streamSettings.finalmask.udp" label="UDP-маски" types={udpMaskTypes} />}
        </Section>
      </div>
    </div>
  )
}
