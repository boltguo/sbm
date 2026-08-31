<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { api, guard, post } from '../api'
import type { Dashboard, UpdateStatus } from '../types'
import Icon from '../components/Icon.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import { dateLocale, t } from '../i18n'
import { createQrCard, downloadQrCard } from '../qr'

const emit = defineEmits<{ toast: [message: string] }>()
const data = ref<Dashboard | null>(null)
const update = ref<UpdateStatus | null>(null)
const checkingUpdate = ref(false)
const refreshingTraffic = ref(false)
const qr = ref('')
let timer = 0

const bytes = (value: number) => {
  if (!value) return '0 B'
  const units = ['B','KiB','MiB','GiB','TiB']; const i = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4)
  return `${(value / 1024 ** i).toFixed(i > 2 ? 2 : 1)} ${units[i]}`
}
const providerBytes = (value: number) => {
  const amount = value / 1e9
  return `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: amount < 10 ? 2 : 1 }).format(amount)} GB`
}
const date = (value?: string) => !value || value.startsWith('0001') ? t('dashboard.noReset') : new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
const panelVersion = (value: string) => !value ? 'unknown' : value === 'dev' || value.startsWith('v') ? value : `v${value}`
const updateLabel = () => update.value?.updateAvailable ? t('dashboard.updateFound', { version: panelVersion(update.value.latestVersion) }) : t('dashboard.checkUpdate')
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
  if (health.status === 'paused') return t('dashboard.samplePaused')
  if (health.status === 'interrupted') return `${t('dashboard.sampleInterrupted')} · ${elapsed(health.failureSince)}`
  if (health.status === 'waiting') return t('dashboard.sampleWaiting')
  return t('dashboard.sampleHealthy', { age: sampleAge(health.lastSuccessAt) })
}
const sampleDetail = () => {
  if (!data.value) return ''
  const health = data.value.sampleHealth
  if (health.status === 'interrupted') return t('dashboard.sampleInterruptedHelp', { time: date(health.failureSince) })
  if (health.status === 'paused') return t('dashboard.samplePausedHelp')
  return t('dashboard.sampleHelp')
}

const safe = guard(message => emit('toast', message))

async function load() {
  const next = await api<Dashboard>('/api/dashboard')
  if (!data.value || data.value.subscriptionURL !== next.subscriptionURL || data.value.subscriptionName !== next.subscriptionName) {
    qr.value = await createQrCard(next.subscriptionURL, next.subscriptionName)
  }
  data.value = next
}
// A failed background poll stays quiet — a 401 already returns to the login
// screen, and toasting every 5 seconds during an outage helps nobody.
const poll = () => { load().catch(() => {}) }
const refreshTraffic = safe(async () => {
  if (refreshingTraffic.value) return
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
const restart = safe(async () => { await post('/api/core/restart'); emit('toast', t('dashboard.restartDone')); await load() })
const reset = safe(async () => { await post('/api/traffic/reset'); emit('toast', t('dashboard.resetDone')); await load() })
async function checkUpdate(notify = true) {
  checkingUpdate.value = true
  try {
    update.value = await api<UpdateStatus>('/api/update')
    if (notify) emit('toast', update.value.updateAvailable ? t('dashboard.updateFound', { version: panelVersion(update.value.latestVersion) }) : t('dashboard.updateCurrent'))
  } catch {
    if (notify) emit('toast', t('dashboard.updateFailed'))
  } finally {
    checkingUpdate.value = false
  }
}
onMounted(() => { poll(); checkUpdate(false); timer = window.setInterval(poll, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div v-if="data" class="page dashboard">
    <header class="page-head"><div><span class="eyebrow">OVERVIEW / LIVE</span><h1>{{ t('dashboard.title') }}</h1></div><div class="head-actions"><ConfirmAction :title="t('dashboard.reset')" :message="t('dashboard.resetConfirm')" @confirm="reset"><button class="secondary"><Icon name="refresh"/>{{ t('dashboard.reset') }}</button></ConfirmAction><ConfirmAction :title="t('dashboard.restart')" :message="t('dashboard.restartConfirm')" @confirm="restart"><button class="primary" :disabled="data.quotaExceeded"><Icon name="power"/>{{ t('dashboard.restart') }}</button></ConfirmAction></div></header>
    <div v-if="data.quotaExceeded" class="alert danger"><strong>{{ t('dashboard.exceeded') }}</strong><span>{{ t('dashboard.exceededHelp') }}</span></div>
    <section class="status-strip">
      <div><span class="status-dot" :class="data.coreStatus === 'running' ? 'online' : data.coreStatus === 'stopped' ? 'offline' : 'unknown'"></span><small>CORE STATUS</small><strong>{{ t(`dashboard.${data.coreStatus}`) }}</strong></div>
      <div class="version-cell">
        <small>SBM VERSION</small>
        <strong>{{ panelVersion(data.panelVersion) }}</strong>
        <button class="version-check" :class="{ checking: checkingUpdate }" :disabled="checkingUpdate" :title="updateLabel()" :aria-label="updateLabel()" @click="checkUpdate()"><Icon name="refresh"/><span v-if="update?.updateAvailable" class="version-update-dot"></span></button>
      </div>
      <div><small>SING-BOX VERSION</small><strong>{{ data.coreVersion }}</strong></div>
      <div><small>PERIOD START</small><strong>{{ date(data.periodStartedAt) }}</strong></div>
      <div><small>NEXT RESET</small><strong>{{ date(data.nextResetAt) }}</strong></div>
    </section>
    <section class="traffic-panel">
      <div class="traffic-copy">
        <div class="traffic-kicker">
          <span class="eyebrow">ESTIMATED PROVIDER USAGE</span>
          <button class="traffic-refresh" :class="{ refreshing: refreshingTraffic }" :disabled="refreshingTraffic" :title="t('dashboard.refreshHelp')" @click="refreshTraffic"><Icon name="refresh"/>{{ t('dashboard.refresh') }}</button>
        </div>
        <h2>{{ providerBytes(data.estimatedProviderUsedBytes) }}</h2>
        <p v-if="data.providerAllowanceBytes">{{ t('dashboard.providerSummary', { total: providerBytes(data.providerAllowanceBytes), remaining: providerBytes(data.providerRemainingBytes), reserve: data.trafficQuota.headroomPercent }) }}</p>
        <p v-else>{{ t('dashboard.unlimitedHelp') }}</p>
        <p class="proxy-actual">{{ t('dashboard.proxyActual', { amount: bytes(data.proxyUsedBytes) }) }}</p>
        <small class="traffic-source" :class="{ warning: data.sampleHealth.status === 'interrupted', pending: data.sampleHealth.status === 'waiting' || data.sampleHealth.status === 'paused' }" :title="sampleDetail()"><i></i>{{ sampleLabel() }}</small>
      </div>
      <div class="traffic-ring" :style="{ '--progress': `${data.providerAllowanceBytes ? data.providerProgress : 0}%` }"><div><b>{{ data.providerAllowanceBytes ? Math.round(data.providerProgress) : '∞' }}</b><small>{{ data.providerAllowanceBytes ? '%' : t('dashboard.unlimited') }}</small></div></div>
      <div class="traffic-split"><div><span>↑</span><p>{{ t('dashboard.upload') }}</p><strong>{{ bytes(data.upload) }}</strong></div><div><span>↓</span><p>{{ t('dashboard.download') }}</p><strong>{{ bytes(data.download) }}</strong></div></div>
      <div class="progress-track"><i :style="{ width: data.providerAllowanceBytes ? `${data.providerProgress}%` : '0%' }"></i><b v-if="data.providerAllowanceBytes" :style="{ left: `${100 - data.trafficQuota.headroomPercent}%` }" :title="t('dashboard.safetyThreshold', { amount: providerBytes(data.providerStopBytes) })"></b></div>
    </section>
    <section class="subscription-card">
      <div class="sub-copy"><span class="eyebrow">ONE SUBSCRIPTION / ALL ENABLED INBOUNDS</span><h2>{{ t('dashboard.subscription') }}</h2><p>{{ t('dashboard.subscriptionHelp') }}</p><div class="copy-field"><code>{{ data.subscriptionURL }}</code><button @click="copy(data.subscriptionURL)"><Icon name="copy"/>{{ t('dashboard.copy') }}</button></div><small>{{ t('dashboard.secretHelp') }}</small></div>
      <div class="qr-frame"><img :src="qr" :alt="t('dashboard.qrAlt', { name: data.subscriptionName })"><button class="qr-download" type="button" @click="saveQR"><Icon name="download"/>{{ t('dashboard.qrDownload') }}</button></div>
    </section>
  </div>
  <div v-else class="loading">{{ t('dashboard.loading') }}</div>
</template>
