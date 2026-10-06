<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, guard, post } from '../api'
import type { Dashboard, GatewayUsage } from '../types'
import Icon from '../components/Icon.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import TrafficHistory from '../components/TrafficHistory.vue'
import PanelUpdate from '../components/PanelUpdate.vue'
import { dateLocale, t } from '../i18n'
import { createQrCard, downloadQrCard } from '../qr'
import { usePollStatus } from '../poll-status'

const emit = defineEmits<{ toast: [message: string] }>()
const data = ref<Dashboard | null>(null)
const activeGateways = computed(() => data.value?.egressGateways?.filter(g => g.enabled) ?? [])
const refreshingTraffic = ref(false)
const qr = ref('')
let timer = 0
let revision = 0
let disposed = false
let pending = 0
let mutating = false
const { stale, tick, succeeded, failed } = usePollStatus()

const bytes = (value: number) => {
  if (!value) return '0 B'
  const binary = data.value?.trafficQuota.unit === 'GiB'
  const base = binary ? 1024 : 1000
  const units = binary ? ['B','KiB','MiB','GiB','TiB'] : ['B','KB','MB','GB','TB']
  const i = Math.min(Math.floor(Math.log(value) / Math.log(base)), 4)
  return `${(value / base ** i).toFixed(i > 2 ? 2 : 1)} ${units[i]}`
}
const providerBytes = (value: number) => {
  const unit = data.value?.trafficQuota.unit || 'GB'
  const amount = value / (unit === 'GiB' ? 1024 ** 3 : 1000 ** 3)
  return `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: amount < 10 ? 2 : 1 }).format(amount)} ${unit}`
}
const gatewayBytes = (value: number, g: GatewayUsage) => `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 1 }).format(value / (g.trafficQuota.unit === 'GiB' ? 1024 ** 3 : 1000 ** 3))} ${g.trafficQuota.unit}`
const date = (value?: string) => !value || value.startsWith('0001') ? t('dashboard.noReset') : new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
const panelVersion = (value: string) => !value ? 'unknown' : value === 'dev' || value.startsWith('v') ? value : `v${value}`
const sampleAge = (value?: string) => {
  if (!value || value.startsWith('0001')) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 10) return t('dashboard.justNow')
  if (seconds < 60) return t('dashboard.secondsAgo', { count: seconds })
  return t('dashboard.minutesAgo', { count: Math.floor(seconds / 60) })
}
const elapsed = (value?: string) => {
  if (!value || value.startsWith('0001')) return t('dashboard.durationUnknown')
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return t('dashboard.durationSeconds', { count: seconds })
  return t('dashboard.durationMinutes', { count: Math.floor(seconds / 60) })
}
const sampleLabel = () => {
  if (!data.value) return t('dashboard.sampleWaiting')
  const health = data.value.sampleHealth
  if (data.value.trafficSource === 'vnstat') return health.status === 'healthy' ? t('network.source', { interface: data.value.networkInterface }) + ' · ' + sampleAge(health.lastSuccessAt) : data.value.networkAvailable ? t('network.interrupted') : t('network.waiting')
  if (health.status === 'paused') return t('dashboard.samplePaused')
  if (health.status === 'interrupted') return `${t('dashboard.sampleInterrupted')} · ${elapsed(health.failureSince)}`
  if (health.status === 'waiting') return t('dashboard.sampleWaiting')
  return t('dashboard.sampleHealthy', { age: sampleAge(health.lastSuccessAt) })
}
const sampleDetail = () => {
  if (!data.value) return ''
  const health = data.value.sampleHealth
  if (data.value.trafficSource === 'vnstat') return data.value.networkReason || t('network.guideDelay')
  if (health.status === 'interrupted') return t('dashboard.sampleInterruptedHelp', { time: date(health.failureSince) })
  if (health.status === 'paused') return t('dashboard.samplePausedHelp')
  return t('dashboard.sampleHelp')
}

const safe = guard(message => emit('toast', message))

async function load() {
  const request = ++revision
  pending++
  try {
    const next = await api<Dashboard>('/api/dashboard', { signal: AbortSignal.timeout(10000) })
    let nextQR = qr.value
    if (!data.value || data.value.subscriptionURL !== next.subscriptionURL || data.value.subscriptionName !== next.subscriptionName) {
      nextQR = await createQrCard(next.subscriptionURL, next.subscriptionName)
    }
    if (disposed || request !== revision) return
    qr.value = nextQR
    data.value = next
    succeeded()
  } catch (error) {
    if (!disposed && request === revision) failed()
    throw error
  } finally { pending-- }
}
const poll = () => { tick(); if (!pending && !mutating) load().catch(() => {}) }
const refreshTraffic = safe(async () => {
  if (refreshingTraffic.value || mutating) return
  refreshingTraffic.value = true
  try {
    await load()
    emit('toast', t('dashboard.refreshed'))
  } finally {
    refreshingTraffic.value = false
  }
})
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('dashboard.copyDone')) })
const saveQR = () => { if (!data.value || !qr.value) return; downloadQrCard(qr.value, data.value.subscriptionName); emit('toast', t('dashboard.qrSaved')) }
const mutate = safe(async (url: string, message: string) => {
  if (mutating) return
  mutating = true
  ++revision
  try { await post(url); emit('toast', message); await load() }
  finally { mutating = false }
})
const restart = () => mutate('/api/core/restart', t('dashboard.restartDone'))
const reset = () => mutate('/api/traffic/reset', t('dashboard.resetDone'))
onMounted(() => { poll(); timer = window.setInterval(poll, 5000) })
onBeforeUnmount(() => { disposed = true; ++revision; clearInterval(timer) })
</script>

<template>
  <div v-if="data" class="page dashboard">
    <header class="page-head"><div><span class="eyebrow">OVERVIEW / LIVE</span><h1>{{ t('dashboard.title') }}</h1></div><div class="head-actions"><ConfirmAction :title="t('dashboard.reset')" :message="t('dashboard.resetConfirm')" @confirm="reset"><button class="secondary"><Icon name="refresh"/>{{ t('dashboard.reset') }}</button></ConfirmAction><ConfirmAction :title="t('dashboard.restart')" :message="t('dashboard.restartConfirm')" @confirm="restart"><button class="primary" :disabled="data.quotaExceeded"><Icon name="power"/>{{ t('dashboard.restart') }}</button></ConfirmAction></div></header>
    <div v-if="stale" class="alert" role="status">{{ t('api.stale') }}</div>
    <div v-if="data.quotaExceeded" class="alert danger"><strong>{{ t('dashboard.exceeded') }}</strong><span>{{ t('dashboard.exceededHelp') }}</span></div>
    <section class="status-strip">
      <div><span class="status-dot" :class="data.coreStatus === 'running' ? 'online' : data.coreStatus === 'stopped' ? 'offline' : 'unknown'"></span><small>CORE STATUS</small><strong>{{ t(`dashboard.${data.coreStatus}`) }}</strong></div>
      <div class="version-cell">
        <small>SBM VERSION</small>
        <strong>{{ panelVersion(data.panelVersion) }}</strong>
        <PanelUpdate @toast="message => emit('toast', message)" />
      </div>
      <div><small>SING-BOX VERSION</small><strong>{{ data.coreVersion }}</strong></div>
      <div><small>PERIOD START</small><strong>{{ date(data.periodStartedAt) }}</strong></div>
      <div><small>NEXT RESET</small><strong>{{ date(data.nextResetAt) }}</strong></div>
    </section>
    <section class="subscription-card">
      <div class="sub-copy"><span class="eyebrow">ONE SUBSCRIPTION / ALL ENABLED INBOUNDS</span><h2>{{ t('dashboard.subscription') }}</h2><p>{{ t('dashboard.subscriptionHelp') }}</p><div class="copy-field"><code>{{ data.subscriptionURL }}</code><button @click="copy(data.subscriptionURL)"><Icon name="copy"/>{{ t('dashboard.copy') }}</button></div><small>{{ t('dashboard.secretHelp') }}</small></div>
      <div class="qr-frame"><img :src="qr" :alt="t('dashboard.qrAlt', { name: data.subscriptionName })"><button class="qr-download" type="button" @click="saveQR"><Icon name="download"/>{{ t('dashboard.qrDownload') }}</button></div>
    </section>
    <section class="traffic-panel">
      <div class="traffic-copy">
        <div class="traffic-kicker">
          <span class="eyebrow">VNSTAT / PLAN USAGE</span>
          <button class="traffic-refresh" :class="{ refreshing: refreshingTraffic }" :disabled="refreshingTraffic" :title="t('dashboard.refreshHelp')" @click="refreshTraffic"><Icon name="refresh"/>{{ t('dashboard.refresh') }}</button>
        </div>
        <h2>{{ data.networkAvailable ? providerBytes(data.estimatedProviderUsedBytes) : '—' }}</h2>
        <p v-if="data.providerAllowanceBytes">{{ t('dashboard.providerSummary', { total: providerBytes(data.providerAllowanceBytes), remaining: providerBytes(data.providerRemainingBytes), reserve: data.trafficQuota.headroomPercent }) }}</p>
        <p v-else>{{ t('dashboard.unlimitedHelp') }}</p>
        <small class="traffic-source" :class="{ warning: data.sampleHealth.status === 'interrupted', pending: data.sampleHealth.status === 'waiting' || data.sampleHealth.status === 'paused' }" :title="sampleDetail()"><i></i>{{ sampleLabel() }}</small>
        <p v-if="data.networkPartial" class="traffic-source warning">{{ t('network.partial') }}</p>
        <p v-if="data.persistenceHealth?.status === 'interrupted'" class="traffic-source warning" role="status">{{ t('egress.persistenceFailed') }}</p>
      </div>
      <div class="traffic-ring" :style="{ '--progress': `${data.providerAllowanceBytes ? data.providerProgress : 0}%` }"><div><b>{{ data.providerAllowanceBytes ? Math.round(data.providerProgress) : '∞' }}</b><small>{{ data.providerAllowanceBytes ? '%' : t('dashboard.unlimited') }}</small></div></div>
      <div class="traffic-split"><div><span>↑</span><p>{{ t('network.tx') }}</p><strong>{{ data.networkAvailable ? bytes(data.upload) : '—' }}</strong></div><div><span>↓</span><p>{{ t('network.rx') }}</p><strong>{{ data.networkAvailable ? bytes(data.download) : '—' }}</strong></div></div>
      <div class="progress-track"><i :style="{ width: data.providerAllowanceBytes ? `${data.providerProgress}%` : '0%' }"></i><b v-if="data.providerAllowanceBytes" :style="{ left: `${100 - data.trafficQuota.headroomPercent}%` }" :title="t('dashboard.safetyThreshold', { amount: providerBytes(data.providerStopBytes) })"></b></div>
    </section>
    <section v-if="activeGateways.length" class="egress-overview">
      <div class="egress-overview-head"><h2>{{ t('egress.title') }}</h2><small>{{ t('egress.estimate') }}</small></div>
      <div class="egress-summary-grid"><article v-for="g in activeGateways" :key="g.id" class="egress-summary" :class="{ warning: g.warning }">
        <h3>{{ g.name }}</h3><strong>{{ gatewayBytes(g.estimatedProviderUsedBytes, g) }} <small v-if="g.providerAllowanceBytes">/ {{ gatewayBytes(g.providerAllowanceBytes, g) }}</small></strong>
        <div class="progress-track"><i :style="{ width: `${g.providerProgress}%` }"></i></div>
        <p>{{ t(`egress.sample.${g.sampleHealth.status}`) }}</p><p v-if="g.sampleHealth.partial" class="traffic-source warning">{{ t('egress.partialUsage') }}</p><p v-if="g.warning">{{ t('egress.quotaWarning') }}</p><p v-else-if="g.providerAllowanceBytes">{{ t('egress.remaining', { amount: gatewayBytes(g.providerRemainingBytes, g) }) }}</p><small>{{ t('egress.nextReset', { date: date(g.nextResetAt) }) }}</small>
      </article></div>
    </section>
    <TrafficHistory :unit="data.trafficQuota.unit" />
  </div>
  <div v-else class="loading" role="status">{{ t(stale ? 'api.stale' : 'dashboard.loading') }}</div>
</template>
