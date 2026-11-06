export type Protocol = 'vless' | 'trojan'
export type Network = 'tcp' | 'ws' | 'grpc' | 'xhttp' | 'httpupgrade' | 'kcp'
export type Security = 'none' | 'tls' | 'reality'

// Free-form JSON as Xray defines it: the protocol "settings" and the "streamSettings" of an inbound.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Doc = Record<string, any>

export type Inbound = {
  id: string
  remark: string
  enable: boolean
  protocol: Protocol
  listen: string
  port: number
  network: Network
  security: Security
  // The documents are the source of truth for Xray; the typed fields around them are derived by the server.
  settings: Doc
  streamSettings: Doc
  ws: { path: string; host: string }
  httpupgrade: { path: string; host: string }
  xhttp: { path: string; host: string; mode: string }
  grpc: { serviceName: string; authority: string; multiMode: boolean }
  tls: { serverName: string; certFile: string; keyFile: string; alpn: string[] }
  reality: { dest: string; serverNames: string[]; privateKey: string; publicKey: string; shortIds: string[]; spiderX: string }
  sniffing: {
    enabled: boolean
    destOverride: string[]
    metadataOnly: boolean
    routeOnly: boolean
    domainsExcluded: string[]
    ipsExcluded: string[]
  }
  public: { host: string; port: number; security: '' | 'tls' | 'none'; sni: string; fingerprint: string; alpn: string }
  acceptProxyProtocol: boolean
  sortOrder: number
  excludeFromSub: boolean
  disableFlow: boolean
  excludeFromAuto: boolean
  nodeDomain?: string // a node of the main server on a domain of its own
  shareAddress: '' | 'listen'
  totalBytes: number
  expiryAt: string | null
  trafficReset: '' | 'never' | 'hourly' | 'daily' | 'weekly' | 'monthly'
  up: number
  down: number
  lastReset: string | null
  serverId?: string
  cascadeId?: string // set on the node a cascade created: its traffic leaves through the exit server
  createdAt: string
  clients?: number
}

export type XrayProfile = {
  logLevel: string
  bittorrent: '' | 'block' | 'allow'
  blockPrivate: boolean
  domainStrategy: '' | 'AsIs' | 'IPIfNonMatch' | 'IPOnDemand'
  template: Doc | null
}

export type ScanResult = {
  target: string
  ok: boolean
  tls: string
  alpn: string
  latencyMs: number
  names: string[]
  issuer: string
  expires: string
  warnings: string[]
  suggestion: string
}

export type Client = {
  id: string
  email: string
  subId: string
  uuid: string
  password: string
  flow: '' | 'xtls-rprx-vision'
  enable: boolean
  totalBytes: number
  expiryAt: string | null
  inboundIds: string[]
  up: number
  down: number
  comment: string
  trial: boolean
  createdAt: string
  lastActiveAt?: string | null
  devices?: Device[]
}

// A device of a subscriber, as it introduced itself (HWID) when its app asked for the subscription.
export type Device = {
  hwid: string
  os: string
  osVersion: string
  model: string
  app: string
  ip: string
  firstSeen: string
  lastSeen: string
  blocked: boolean
}

export type LinkView = {
  inboundId: string
  remark: string
  name: string
  protocol: Protocol
  network: Network
  security: string
  enabled: boolean
  link: string
  error?: string
}

export type XrayStatus = {
  installed: boolean
  running: boolean
  pid: number
  version: string
  uptimeSeconds: number
  lastError: string
}

export type ProfileMode = 'none' | 'builtin' | 'custom'

// How the subscription link looks when it is pasted into a messenger.
export type LinkPreview = {
  mode: 'default' | 'custom'
  siteName: string
  title: string
  description: string
  imageUrl: string
  // the picture uploaded to the panel (the bytes are served from /api/sub-preview); type is empty when there is none
  image: { type: string; width: number; height: number; sum: string }
}

export type SubSettings = {
  version: number
  // general
  enable: boolean
  path: string
  domain: string
  uri: string
  // information
  encode: boolean
  remarkTemplate: string
  infoNode: boolean
  calendarExpire: boolean
  timeZone: string
  expiredTemplate: string
  depletedTemplate: string
  updateHours: number
  // profile
  title: string
  supportUrl: string
  profileMode: ProfileMode
  profileUrl: string
  announce: string
  preview: LinkPreview
  // client apps
  hideSettings: boolean
  happ: HappSettings
  incy: IncySettings
  formats: FormatSettings
  autoNode: AutoNodeSettings
  page: PageSettings
}

// The subscriber's page written by the administrator.
export type PageSettings = {
  enable: boolean
  html: string
  css: string // put into the page as <style>
  js: string // put into the page as <script>; the subscription is in window.sub
}

export type AutoNodeSettings = {
  enable: boolean
  name: string
  last: boolean
  position: number
  keepLinks: boolean
  probeUrl: string
  intervalSec: number
  timeoutSec: number
  toleranceMs: number
  groups: AutoGroup[]
}

// One more automatic node over its own connections.
export type AutoGroup = {
  id: string
  name: string
  country: string
  enable: boolean
  members: string[]
  last: boolean
  position: number
}

export type FormatSettings = {
  clashEnable: boolean
  clashPath: string
  clashAutoDetect: boolean
  clashUaRegex: string
  clashEnableRouting: boolean
  clashRules: string
  clashRemoteDns: boolean
  jsonEnable: boolean
  jsonPath: string
  jsonAutoDetect: boolean
  jsonUaRegex: string
  jsonAlwaysArray: boolean
  jsonMux: string
  jsonFragment: string
  jsonNoises: string
  jsonRules: string
  jsonDns: string
}

export type HappBanner = { color: string; text: string; buttonText: string; buttonLink: string }
export type HappBanners = { active: HappBanner; trial: HappBanner; expired: HappBanner; depleted: HappBanner }

export type HappSettings = {
  autoDetect: boolean
  providerId: string
  newUrl: string
  fallbackUrl: string
  subInfoColor: string
  subInfoText: string
  subInfoButtonText: string
  subInfoButtonLink: string
  banners: HappBanners
  subExpire: boolean
  subExpireButtonLink: string
  notificationExpire: boolean
  noLimit: boolean
  alwaysHwid: boolean
  tunMode: string
  tunType: string
  excludeRoutes: string
  excludeApns: boolean
  colorProfile: string
  pingType: string
  autoConnect: boolean
  autoConnectType: string
  perAppMode: string
  perAppList: string
  localProxyAuth: string
  enableRouting: boolean
  routingRules: string
}

export type IncySettings = {
  autoDetect: boolean
  profileDescription: string
  sortOrder: string
  supportEmail: string
  announceUrl: string
  premiumUrl: string
  bannerText: string
  bannerButtonText: string
  bannerButtonUrl: string
  bannerBgColor: string
  bannerButtonColor: string
  hideUrl: string
  hideCheck: string
  noLimitEnabled: string
  perAppEnable: string
  perAppMode: string
  perAppList: string
  fragmentationEnable: string
  fragmentationLength: string
  fragmentationInterval: string
  fragmentationPackets: string
  noisesEnable: string
  noisesType: string
  noisesPacket: string
  noisesDelay: string
  resolveEnable: string
  resolveDnsDomain: string
  resolveDnsIp: string
  enableRouting: boolean
  routingRules: string
}

export type TemplateToken = { name: string; help: string }

export type TelegramSettings = {
  enable: boolean
  adminIds: number[]
}

export type Settings = {
  panelDomain: string
  realityDomain: string
  subscription: SubSettings
  telegram: TelegramSettings
}

export type ApiTokenInfo = { exists: boolean; prefix?: string; createdAt?: string }

export type TelegramStatus = { enable: boolean; adminIds: number[]; tokenSet: boolean; running: boolean; username?: string; error?: string }

export type ImportReport = {
  kind: 'full' | 'users' | 'legacy'
  dryRun: boolean
  exportedAt?: string
  created: number
  skipped: number
  counts?: Record<string, number>
  items: { name: string; action: 'create' | 'skip'; reason?: string; notes?: string[] }[]
  notMigrated?: Record<string, number>
  warning?: string
}

export type ExternalNode = {
  id: string
  name: string
  uri: string
  enable: boolean
  createdAt: string
  protocol: string
  host: string
  port: number
  network: string
  security: string
}

export type ImportResult = { added: number; problems: { line: number; error: string }[] }

export type TopClient = { name: string; used: number; limit: number }

export type Dashboard = {
  version: string
  system: {
    hostname: string
    uptimeSeconds: number
    cpuPercent: number
    load1: number
    load5: number
    load15: number
    memTotal: number
    memUsed: number
    diskTotal: number
    diskUsed: number
    netRx: number
    netTx: number
    cpus: number
  }
  xray: XrayStatus
  inbounds: { total: number; enabled: number }
  clients: { total: number; active: number; online: number; onlineHere: number; expired: number; depleted: number; disabled: number; trial: number }
  traffic: { up: number; down: number }
  top: { name: string; used: number; limit: number }[]
  topPeriods: { day: TopClient[]; week: TopClient[]; all: TopClient[] }
  history: { time: number; up: number; down: number; nodes: { name: string; bytes: number }[] }[]
  onlineNames: string[]
}

export type ClientLinks = { subscriptionUrl: string; links: LinkView[] }

export type PublicLink = { remark: string; name: string; protocol: Protocol; network: Network; security: string; link: string }

export type PublicSub = {
  name: string
  title: string
  status: 'active' | 'disabled' | 'expired' | 'exhausted'
  up: number
  down: number
  total: number
  expiryAt: string | null
  url: string
  supportUrl: string
  profileUrl: string
  announce: string
  links: PublicLink[]
}

export type Saved<T> = { item: T; warning?: string }

export type ServerInfo = {
  id: string
  name: string
  address: string
  port: number
  enable: boolean
  fingerprint: string
  country: string
  flag: string
  managed: boolean // installed from the panel over SSH: the panel holds a key and can upgrade the agent
  createdAt: string
  inbounds: number
  clients: number
  onlineClients: number
  up: number
  down: number
  online: boolean
  pingMs: number
  error?: string
  lastSeen: string | null
  agentVersion?: string
  xray: XrayStatus
  system: Dashboard['system']
}

export type ServerClient = {
  email: string
  up: number
  down: number
  online: boolean
  lastActiveAt: string | null
  status: 'active' | 'expired' | 'depleted' | 'disabled'
}

export type SshReport = {
  hostKey: string
  os: string
  arch: string
  root: boolean
  systemd: boolean
  agentInstalled: boolean
  panelInstalled: boolean
  listening: number[]
  busyPorts: { port: number; holder: string }[]
  domainIp: string
  dnsProblem?: string // the entry of problems that is about the domain's A record
  serverIp: string
  problems: string[]
  warnings: string[]
  ok: boolean
}

export type NodeJob = {
  id: string
  kind: 'install' | 'upgrade' | 'firewall' | 'reality'
  status: 'running' | 'done' | 'failed'
  log: string[]
  warnings: string[]
  error?: string
  serverId?: string
}

// A ready-made routing profile from github.com/hydraponique/roscomvpn-routing (DEFAULT or WHITELIST).
export type RoutingPreset = {
  id: 'default' | 'whitelist'
  label: string
  hint: string
  link: string
  name: string
  updated: string | null
  source: 'github' | 'embedded'
}

// Two servers chained: clients enter on A (the entry), traffic leaves from B (the exit). Clients see it as a node.
export type Cascade = {
  id: string
  name: string
  country: string
  entryServerId: string
  exitServerId: string
  exitInboundId: string
  inboundId: string
  domain?: string
  adopted?: boolean
  mode: 'merge' | 'split'
  createdAt: string
  flag: string
  entryName: string
  exitName: string
  exitRemark: string
  entryAddress: string
  port: number
  state: 'running' | 'off' | 'broken'
  problem?: string
}

// The answer to "is this domain free and does it point at the main server".
export type DomainCheck = {
  ok: boolean
  available: boolean // the panel can manage nginx and Caddy for extra domains
  message?: string
  problem?: string
  usedBy?: string
  pending?: boolean // fine, but DNS does not show the record yet
  addresses: string[]
  main: string[]
}

// A key pair the panel made on its own server for the REG.RU API.
export type RegRuCertificate = { certificate: string; key: string }

// What the settings page knows about updates of the panel itself.
export type UpdateInfo = {
  current: string
  latest: string
  newer: boolean
  published?: string
  notes?: string
  url?: string
  canApply: boolean
  reason?: string
  status: { state: 'idle' | 'downloading' | 'verifying' | 'installing' | 'failed'; message: string; version: string }
}

export type RegRuStatus = {
  configured: boolean
  connected: boolean
  username: string
  zone: string
  passwordSet: boolean
  certSet: boolean
  error?: string
  unused: string[]
  extraZones: string[]
  zoneProblems: string[]
  serverIp?: string
}
export type RegRuDomain = { name: string; ip: string }
export type RegRuGenerated = { domain: string; ip: string; created: boolean }
