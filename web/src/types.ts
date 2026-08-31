export interface Dashboard {
  coreStatus: 'running' | 'stopped' | 'unknown'
  coreVersion: string
  panelVersion: string
  upload: number
  download: number
  proxyUsedBytes: number
  trafficQuota: TrafficQuota
  effectiveLimitBytes: number
  providerAllowanceBytes: number
  estimatedProviderUsedBytes: number
  providerStopBytes: number
  providerRemainingBytes: number
  providerProgress: number
  periodStartedAt: string
  nextResetAt?: string
  quotaExceeded: boolean
  sampleHealth: SampleHealth
  subscriptionURL: string
  subscriptionName: string
}

export type TrafficBillingMode = 'bidirectional' | 'single'
export type TrafficUnit = 'GB' | 'GiB'
export interface TrafficQuota {
  amount: number
  unit: TrafficUnit
  billingMode: TrafficBillingMode
  headroomPercent: number
}
export interface SampleHealth {
  status: 'waiting' | 'healthy' | 'interrupted' | 'paused'
  lastSuccessAt?: string
  failureSince?: string
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
export interface Settings {
  domain: string
  panelPort: number
  trafficQuota: TrafficQuota
  reset: ResetConfig
  outboundStrategy: OutboundStrategy
  subscriptionURL: string
}

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
  health: HealthReport
}

export type HealthStatus = 'ok' | 'warning' | 'error' | 'unknown'
export interface HealthCheck {
  id: string
  kind: string
  status: HealthStatus
  reason: string
  checkedAt: string
  protocol?: 'tcp' | 'udp'
  port?: number
  percent?: number
  expiresAt?: string
  lastSuccessAt?: string
  failureSince?: string
  nextResetAt?: string
  timezone?: string
}
export interface HealthReport {
  overall: HealthStatus
  checks: HealthCheck[]
  checkedAt: string
}
