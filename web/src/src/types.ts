export interface LocalNode {
  slotId: number
  name: string
  country: string
  socks5Port: number
  socks5: string
}

export interface LocalPlan {
  basePort: number
  host: string
  path: string
  missingHost: boolean
  total: number
  nodes: LocalNode[]
}

export type Protocol = 'openvpn_udp' | 'openvpn_tcp' | 'softether_https' | 'sstp' | 'l2tp' | 'socks5' | 'http'

export interface Node {
  hostName: string
  ip: string
  port?: number
  score: number
  pingMs: number
  speedBps: number
  countryLong: string
  countryShort: string
  sessions: number
  operator: string
  message: string
  protocols: Protocol[]
  measuredBps: number
  measuredAt?: string
  speedTestFailed: boolean
  lastFailure?: string
  failureReason?: string
  asn?: string
  asnOrg?: string
  source?: string
  residential?: boolean
  failCount?: number
  qualityScore?: number
  successCount?: number
}

export interface Status {
  state: string
  selection: 'auto' | 'manual'
  directMode: boolean
  connectionPaused: boolean
  activeIp?: string
  activeHostName?: string
  activeProtocol?: Protocol
  lastRefresh?: string
  lastHealthCheck?: string
  lastError?: string
  connectingIp?: string
  refreshRunning: boolean
}

export interface SubscriptionNode {
  slotId: number
  name: string
  country?: string
  asnOrg?: string
  protocol?: Protocol
  source?: string
  speedBps: number
  pingMs: number
  qualityScore: number
  failCount: number
  successCount: number
  residential: boolean
  chained: boolean
}

export interface FrontProxySettings {
  enabled: boolean
  type: string
  url: string
  slot: number
  api: string
  timeout: string
}

export interface SubscriptionClientInfo {
  name: string
  token: string
  count: number
  countries: string[] | null
  exclude: string[] | null
  shuffle: boolean
  group: string
  sort: string
  v2rayUrl: string
  clashUrl: string
  autoUrl: string
  singboxUrl: string
  surgeUrl: string
  qxUrl: string
}

export interface SubscriptionInfo {
  enabled: boolean
  v2rayUrl: string
  clashUrl: string
  autoUrl: string
  singboxUrl: string
  surgeUrl: string
  qxUrl: string
  metricsUrl: string
  publicHost: string
  token: string
  websocketPath: string
  trojanPath: string
  protocol: string
  udp: boolean
  slots: { total: number; ready: number; healthy: number; switching: number; error: number }
  countries: Record<string, number>
  poolCountries: Record<string, number>
  nodes: SubscriptionNode[]
  clients: SubscriptionClientInfo[]
  frontProxy: FrontProxySettings
}

export interface MatrixSlot {
  slotId: number
  username: string
  port: number
  mode: string
  protocol?: string
  country?: string
  state: string
  activeIp?: string
  nodeIp?: string
  exitIp?: string
  countryShort?: string
  asn?: string
  asnOrg?: string
  source?: string
  speedBps: number
  pingMs: number
  residential: boolean
  chained: boolean
  failCount: number
  qualityScore: number
  successCount: number
  cooldownUntil?: string
  healthy: boolean
  lastError?: string
  lastSwitch?: string
  lastProbe?: string
}

export interface MatrixSlotsInfo {
  enabled: boolean
  maxSlots: number
  available: number
  ready: number
  healthy: number
  slots: MatrixSlot[]
  countries: { code: string; total: number }[]
}

export interface ShareSlot extends MatrixSlot {
  host: string
  password: string
  socks5: string
  socks5h: string
  v2ray: string
}

export interface SharePoolInfo {
  enabled: boolean
  host: string
  direct: boolean
  portStart: number
  maxSlots: number
  total: number
  ready: number
  healthy: number
  slots: ShareSlot[]
}

export interface VersionInfo {
  version: string
  release: string
  commit: string
  channel: string
  toolchain?: string
  buildTime?: string
  goVersion: string
  os: string
  arch: string
}

export interface Config {
  sourceUrl: string
  refreshInterval: string
  filterExpression: string
  selectionMode: 'speed' | 'ping' | 'score'
  followRankingOnRefresh: boolean
  fallbackToDirect: boolean
  socks5: { listenAddress: string; username: string; password: string; idleTimeout: string; halfCloseTimeout: string }
  monitor: { url: string; interval: string; timeout: string }
  web: { listenAddress: string; username: string; passwordHash: string; sessionSecret: string }
  databasePath: string
  dnsServers: string[]
  speedTestUrl: string
  speedTestTimeout: string
  connectTimeout: string
  protocolPriority: Protocol[]
  vpnGateUsername: string
  vpnGatePassword: string
  vpnGatePreSharedKey: string
  matrix: { sources: string[]; slots: number; portStart: number; listenAddress?: string; publicSocksHost?: string; shareLimit?: number; dialMinMs?: number; dialMaxMs?: number }
}
