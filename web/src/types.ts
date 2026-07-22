export interface Dashboard {
  active: boolean
  version: string
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
export interface Settings { domain: string; panelPort: number; totalBytes: number; reset: ResetConfig; subscriptionURL: string }

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
