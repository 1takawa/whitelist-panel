import { randomPort, randomString } from '../lib'
import type { Doc, Inbound, Settings } from '../types'
import { randomPath } from './inbound/doc'
import { realityDefaults } from './inbound/SecurityTab'

const tcp = (): Doc => ({ header: { type: 'none' } })

export function emptyInbound(): Inbound {
  return {
    id: '',
    remark: '',
    enable: true,
    protocol: 'vless',
    listen: '127.0.0.1',
    port: 8443,
    network: 'tcp',
    security: 'none',
    settings: { decryption: 'none' },
    streamSettings: { network: 'tcp', security: 'none', tcpSettings: tcp() },
    ws: { path: '/', host: '' },
    httpupgrade: { path: '/', host: '' },
    xhttp: { path: '/', host: '', mode: 'auto' },
    grpc: { serviceName: '', authority: '', multiMode: true },
    tls: { serverName: '', certFile: '', keyFile: '', alpn: [] },
    reality: { dest: '', serverNames: [], privateKey: '', publicKey: '', shortIds: [], spiderX: '' },
    sniffing: { enabled: true, destOverride: ['http', 'tls', 'quic'], metadataOnly: false, routeOnly: false, domainsExcluded: [], ipsExcluded: [] },
    public: { host: '', port: 443, security: '', sni: '', fingerprint: '', alpn: '' },
    acceptProxyProtocol: false,
    sortOrder: 0,
    excludeFromSub: false,
    disableFlow: false,
    excludeFromAuto: false,
    clientRouting: '',
    shareAddress: '',
    totalBytes: 0,
    expiryAt: null,
    trafficReset: 'never',
    up: 0,
    down: 0,
    lastReset: null,
    createdAt: '',
  }
}

// An inbound as stored by an older release has no documents yet; the server fills them in, this is the safety net
// that keeps the form from crashing on missing lists.
export function hydrate(i: Inbound): Inbound {
  const e = emptyInbound()
  return {
    ...e,
    ...i,
    settings: i.settings ?? e.settings,
    streamSettings: i.streamSettings ?? e.streamSettings,
    sniffing: { ...e.sniffing, ...i.sniffing, destOverride: i.sniffing?.destOverride ?? [], domainsExcluded: i.sniffing?.domainsExcluded ?? [], ipsExcluded: i.sniffing?.ipsExcluded ?? [] },
    trafficReset: i.trafficReset || 'never',
  }
}

export type Domains = Pick<Settings, 'panelDomain' | 'realityDomain'>

export type Preset = { id: string; label: string; hint: string; build: (s: Domains) => Inbound }

const behindCaddy = { host: '', port: 443, security: 'tls' as const, sni: '', fingerprint: '', alpn: '' }

// Presets match the server layout: nginx routes :443 by SNI to REALITY, Caddy terminates TLS for the other transports.
export const presets: Preset[] = [
  {
    id: 'reality',
    label: 'VLESS + REALITY (за nginx)',
    hint: 'Самый устойчивый вариант. Идёт через nginx по SNI на 443, маскируется под домен REALITY.',
    build: (s) => ({
      ...emptyInbound(),
      remark: 'VLESS Reality',
      port: 8443,
      acceptProxyProtocol: true,
      settings: { decryption: 'none' },
      streamSettings: {
        network: 'tcp',
        security: 'reality',
        tcpSettings: tcp(),
        sockopt: { acceptProxyProtocol: true },
        realitySettings: { ...structuredClone(realityDefaults), target: '127.0.0.1:7443', serverNames: s.realityDomain ? [s.realityDomain] : [] },
      },
    }),
  },
  {
    id: 'reality-direct',
    label: 'VLESS + REALITY (напрямую, для отдельного сервера)',
    hint: 'Слушает на всех адресах без nginx и Caddy. Target — чужой сайт с TLS 1.3 и HTTP/2; проверьте его кнопкой «Проверить».',
    build: () => ({
      ...emptyInbound(),
      remark: 'VLESS Reality',
      listen: '0.0.0.0',
      port: 443,
      public: { host: '', port: 0, security: '', sni: '', fingerprint: '', alpn: '' },
      streamSettings: {
        network: 'tcp',
        security: 'reality',
        tcpSettings: tcp(),
        realitySettings: { ...structuredClone(realityDefaults), target: 'www.microsoft.com:443', serverNames: ['www.microsoft.com'] },
      },
    }),
  },
  {
    id: 'vless-ws',
    label: 'VLESS + WebSocket (TLS)',
    hint: 'За Caddy на домене панели. Подходит для CDN.',
    build: () => ({
      ...emptyInbound(),
      remark: 'VLESS WS',
      port: randomPort(),
      public: behindCaddy,
      streamSettings: { network: 'ws', security: 'none', wsSettings: { path: randomPath(), headers: {} } },
    }),
  },
  {
    id: 'vless-xhttp',
    label: 'VLESS + XHTTP (TLS)',
    hint: 'За Caddy на домене панели, HTTP/2.',
    build: () => ({
      ...emptyInbound(),
      remark: 'VLESS XHTTP',
      port: randomPort(),
      public: { ...behindCaddy, alpn: 'h2' },
      streamSettings: { network: 'xhttp', security: 'none', xhttpSettings: { path: randomPath(), host: '', mode: 'auto' } },
    }),
  },
  {
    id: 'trojan-grpc',
    label: 'Trojan + gRPC (TLS)',
    hint: 'За Caddy на домене панели.',
    build: () => ({
      ...emptyInbound(),
      remark: 'Trojan gRPC',
      protocol: 'trojan',
      port: randomPort(),
      settings: {},
      public: { ...behindCaddy, alpn: 'h2' },
      streamSettings: { network: 'grpc', security: 'none', grpcSettings: { serviceName: randomString(10), authority: '', multiMode: true } },
    }),
  },
  {
    id: 'trojan-ws',
    label: 'Trojan + WebSocket (TLS)',
    hint: 'За Caddy на домене панели.',
    build: () => ({
      ...emptyInbound(),
      remark: 'Trojan WS',
      protocol: 'trojan',
      port: randomPort(),
      settings: {},
      public: behindCaddy,
      streamSettings: { network: 'ws', security: 'none', wsSettings: { path: randomPath(), headers: {} } },
    }),
  },
]
