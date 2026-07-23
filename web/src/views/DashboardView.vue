<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import { api, post } from '../api'
import type { Dashboard, UpdateStatus } from '../types'
import Icon from '../components/Icon.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import { dateLocale, t } from '../i18n'

const emit = defineEmits<{ toast: [message: string] }>()
const data = ref<Dashboard | null>(null)
const update = ref<UpdateStatus | null>(null)
const checkingUpdate = ref(false)
const refreshingTraffic = ref(false)
const qr = ref('')
let timer = 0

const bytes = (value: number) => {
  if (!value) return '0 B'
  const units = ['B','KB','MB','GB','TB']; const i = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4)
  return `${(value / 1024 ** i).toFixed(i > 2 ? 2 : 1)} ${units[i]}`
}
const date = (value?: string) => !value || value.startsWith('0001') ? t('dashboard.noReset') : new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
const panelVersion = (value: string) => !value ? 'unknown' : value === 'dev' || value.startsWith('v') ? value : `v${value}`
const updateLabel = () => update.value?.updateAvailable ? t('dashboard.updateFound', { version: panelVersion(update.value.latestVersion) }) : t('dashboard.checkUpdate')
const auditLabel = () => {
  if (!data.value || data.value.trafficAudit.status === 'unavailable') return t('dashboard.trafficSource')
  return `${t('dashboard.trafficSource')} · ${t(`dashboard.audit.${data.value.trafficAudit.status}`)}`
}
const auditDetail = () => {
  if (!data.value || data.value.trafficAudit.status === 'unavailable') return t('dashboard.audit.unavailableHelp')
  const audit = data.value.trafficAudit
  if (audit.status === 'collecting') return t('dashboard.audit.collectingHelp', { amount: bytes(audit.proxyBytes) })
  return t('dashboard.audit.detail', { interface: audit.interface || 'WAN', proxy: bytes(audit.proxyBytes), receive: bytes(audit.receiveBytes), transmit: bytes(audit.transmitBytes) })
}

async function load() {
  const next = await api<Dashboard>('/api/dashboard')
  if (!data.value || data.value.subscriptionURL !== next.subscriptionURL) {
    qr.value = await QRCode.toDataURL(next.subscriptionURL, { width: 256, margin: 1, color: { dark: '#111712', light: '#f4f1e8' } })
  }
  data.value = next
}
async function refreshTraffic() {
  if (refreshingTraffic.value) return
  refreshingTraffic.value = true
  try {
    await load()
    emit('toast', t('dashboard.refreshed'))
  } finally {
    refreshingTraffic.value = false
  }
}
async function copy(value: string) { await navigator.clipboard.writeText(value); emit('toast', t('dashboard.copyDone')) }
async function restart() { await post('/api/core/restart'); emit('toast', t('dashboard.restartDone')); await load() }
async function reset() { await post('/api/traffic/reset'); emit('toast', t('dashboard.resetDone')); await load() }
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
onMounted(() => { load(); checkUpdate(false); timer = window.setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div v-if="data" class="page dashboard">
    <header class="page-head"><div><span class="eyebrow">OVERVIEW / LIVE</span><h1>{{ t('dashboard.title') }}</h1></div><div class="head-actions"><ConfirmAction :title="t('dashboard.reset')" :message="t('dashboard.resetConfirm')" @confirm="reset"><button class="secondary"><Icon name="refresh"/>{{ t('dashboard.reset') }}</button></ConfirmAction><button class="primary" :disabled="data.quotaExceeded" @click="restart"><Icon name="power"/>{{ t('dashboard.restart') }}</button></div></header>
    <div v-if="data.quotaExceeded" class="alert danger"><strong>{{ t('dashboard.exceeded') }}</strong><span>{{ t('dashboard.exceededHelp') }}</span></div>
    <section class="status-strip">
      <div><span class="status-dot" :class="data.active ? 'online' : 'offline'"></span><small>CORE STATUS</small><strong>{{ data.active ? t('dashboard.running') : t('dashboard.stopped') }}</strong></div>
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
          <span class="eyebrow">SHARED TRAFFIC POOL</span>
          <button class="traffic-refresh" :class="{ refreshing: refreshingTraffic }" :disabled="refreshingTraffic" :title="t('dashboard.refreshHelp')" @click="refreshTraffic"><Icon name="refresh"/>{{ t('dashboard.refresh') }}</button>
        </div>
        <h2>{{ bytes(data.used) }}</h2>
        <p>{{ data.totalBytes ? t('dashboard.totalRemaining', { total: bytes(data.totalBytes), remaining: bytes(data.remaining) }) : t('dashboard.unlimitedHelp') }}</p>
        <small class="traffic-source" :class="{ warning: data.trafficAudit.status === 'different', pending: data.trafficAudit.status === 'collecting' || data.trafficAudit.status === 'unavailable' }" :title="auditDetail()"><i></i>{{ auditLabel() }}</small>
      </div>
      <div class="traffic-ring" :style="{ '--progress': `${data.totalBytes ? data.progress : 0}%` }"><div><b>{{ data.totalBytes ? Math.round(data.progress) : '∞' }}</b><small>{{ data.totalBytes ? '%' : t('dashboard.unlimited') }}</small></div></div>
      <div class="traffic-split"><div><span>↑</span><p>{{ t('dashboard.upload') }}</p><strong>{{ bytes(data.upload) }}</strong></div><div><span>↓</span><p>{{ t('dashboard.download') }}</p><strong>{{ bytes(data.download) }}</strong></div></div>
      <div class="progress-track"><i :style="{ width: data.totalBytes ? `${data.progress}%` : '0%' }"></i></div>
    </section>
    <section class="subscription-card">
      <div class="sub-copy"><span class="eyebrow">ONE SUBSCRIPTION / ALL ENABLED INBOUNDS</span><h2>{{ t('dashboard.subscription') }}</h2><p>{{ t('dashboard.subscriptionHelp') }}</p><div class="copy-field"><code>{{ data.subscriptionURL }}</code><button @click="copy(data.subscriptionURL)"><Icon name="copy"/>{{ t('dashboard.copy') }}</button></div><small>{{ t('dashboard.secretHelp') }}</small></div>
      <div class="qr-frame"><img :src="qr" :alt="t('dashboard.qrAlt')"><span>SCAN TO IMPORT</span></div>
    </section>
  </div>
  <div v-else class="loading">{{ t('dashboard.loading') }}</div>
</template>
