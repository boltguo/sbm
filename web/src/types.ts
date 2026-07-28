export interface Dashboard {
  active: boolean
  coreVersion: string
  panelVersion: string
  upload: number
  download: number
  used: number
  totalBytes: number
  remaining: number
  progress: number
  periodStartedAt: string
  nextResetAt?: string
  quotaExceeded: boolean
  subscriptionURL: string
  trafficAudit: TrafficAudit
}

export interface TrafficAudit {
  status: 'collecting' | 'normal' | 'different' | 'unavailable'
  interface?: string
  proxyBytes: number
  receiveBytes: number
  transmitBytes: number
  receiveRatio: number
  transmitRatio: number
  startedAt?: string
}

export interface UpdateStatus {
  currentVersion: string
  latestVersion: string
  updateAvailable: boolean
  releaseURL: string
  checkedAt: string
}

export interface VLESSOptions { uuid: string; sni: string; privateKey: string; publicKey: string; shortId: string }
export interface Hysteria2Options { password: string; obfs?: string; obfsPassword?: string }
export interface Inbound {
  id: string
  type: 'vless-reality' | 'hysteria2'
  name: string
  enabled: boolean
  port: number
  network: 'tcp' | 'udp'
  link: string
  vless?: VLESSOptions
  hysteria2?: Hysteria2Options
}

export interface ResetConfig { mode: 'none' | 'monthly'; day: number; timezone: string }
export type OutboundStrategy = 'auto' | 'prefer_ipv4' | 'prefer_ipv6' | 'ipv4_only' | 'ipv6_only'
export interface Settings { domain: string; panelPort: number; totalBytes: number; reset: ResetConfig; outboundStrategy: OutboundStrategy; subscriptionURL: string }

export interface ServerStatus {
  hostname: string
  os: string
  kernel: string
  architecture: string
  cpuCores: number
  cpuPercent: number
  load1: number
  load5: number
  load15: number
  memoryTotal: number
  memoryUsed: number
  memoryPercent: number
  diskTotal: number
  diskUsed: number
  diskPercent: number
  uptimeSeconds: number
  collectedAt: string
}
