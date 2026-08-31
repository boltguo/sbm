<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import { dateLocale, t } from '../i18n'
import type { HealthCheck, HealthStatus, ServerStatus } from '../types'

const data = ref<ServerStatus | null>(null)
const showAllChecks = ref(false)
let timer = 0

const passedChecks = computed(() => data.value?.health.checks.filter(check => check.status === 'ok') ?? [])
const attentionChecks = computed(() => data.value?.health.checks.filter(check => check.status !== 'ok') ?? [])
const visibleChecks = computed(() => showAllChecks.value ? data.value?.health.checks ?? [] : attentionChecks.value)

const bytes = (value: number) => {
  if (!value) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`
}
const uptime = (seconds: number) => {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  return days ? t('server.days', { days, hours }) : t('server.hours', { hours, minutes })
}
const time = (value: string) => new Intl.DateTimeFormat(dateLocale(), { timeStyle: 'medium' }).format(new Date(value))
const dateTime = (value: string) => new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
const statusLabel = (status: HealthStatus) => t(`server.status.${status}`)
const overallStatusLabel = (status: HealthStatus) => t(`server.overall.${status}`)
const checkTitle = (check: HealthCheck) => {
  if (check.kind === 'listener_panel') return t('server.check.listenerPanel', { protocol: check.protocol?.toUpperCase() || '', port: check.port || 0 })
  if (check.kind === 'listener_inbound') return t('server.check.listenerInbound', { protocol: check.protocol?.toUpperCase() || '', port: check.port || 0 })
  return t(`server.check.${check.kind}`)
}
const checkReason = (check: HealthCheck) => t(`server.reason.${check.reason}`)
const checkDetail = (check: HealthCheck) => {
  if (check.expiresAt) return t('server.detail.expires', { time: dateTime(check.expiresAt) })
  if (check.failureSince) return t('server.detail.failedSince', { time: dateTime(check.failureSince) })
  if (check.lastSuccessAt) return t('server.detail.lastSample', { time: dateTime(check.lastSuccessAt) })
  if (check.nextResetAt) return t('server.detail.nextReset', { time: dateTime(check.nextResetAt), timezone: check.timezone || 'Local' })
  if (check.kind === 'disk' && check.percent !== undefined) return `${check.percent.toFixed(1)}%`
  return t('server.detail.checked', { time: time(check.checkedAt) })
}
// Read-only polling: a 401 already returns to the login screen, and any other
// failure simply leaves the last snapshot on screen until the next tick.
const load = () => { api<ServerStatus>('/api/server').then(next => { data.value = next }).catch(() => {}) }
onMounted(() => { load(); timer = window.setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div v-if="data" class="page server-page">
    <header class="page-head"><div><span class="eyebrow">HOST / LIVE</span><h1>{{ t('server.title') }}</h1><p>{{ t('server.help') }}</p></div><small class="server-updated">{{ t('server.updated', { time: time(data.collectedAt) }) }}</small></header>
    <section class="doctor-panel">
      <header class="doctor-head">
        <div><span class="eyebrow">DOCTOR / READ ONLY</span><h2>{{ t('server.doctor') }}</h2><p>{{ t('server.doctorHelp') }}</p></div>
        <div class="doctor-actions"><span class="health-chip" :data-status="data.health.overall">{{ overallStatusLabel(data.health.overall) }}</span></div>
      </header>
      <div class="doctor-summary">
        <p v-if="attentionChecks.length === 0">{{ t('server.allPassed', { count: passedChecks.length }) }}</p>
        <p v-else>{{ t('server.somePassed', { passed: passedChecks.length, total: data.health.checks.length }) }}</p>
        <button v-if="passedChecks.length" type="button" @click="showAllChecks = !showAllChecks">{{ t(showAllChecks ? 'server.hideHealthy' : 'server.showAll') }}</button>
      </div>
      <div v-if="visibleChecks.length" class="health-grid">
        <article v-for="check in visibleChecks" :key="check.id" class="health-check" :data-status="check.status">
          <div><span class="health-dot"></span><strong>{{ checkTitle(check) }}</strong><small>{{ statusLabel(check.status) }}</small></div>
          <p>{{ checkReason(check) }}</p>
          <code>{{ checkDetail(check) }}</code>
        </article>
      </div>
    </section>
    <section class="resource-grid">
      <article class="resource-card"><div class="resource-head"><span>{{ t('server.cpu') }}</span><strong>{{ data.cpuPercent.toFixed(1) }}%</strong></div><div class="resource-meter"><i :style="{ width: `${data.cpuPercent}%` }"></i></div><footer><span>{{ t('server.cores', { count: data.cpuCores }) }}</span><code>{{ data.architecture }}</code></footer></article>
      <article class="resource-card"><div class="resource-head"><span>{{ t('server.memory') }}</span><strong>{{ data.memoryPercent.toFixed(1) }}%</strong></div><div class="resource-meter"><i :style="{ width: `${data.memoryPercent}%` }"></i></div><footer><span>{{ t('server.usedOf', { used: bytes(data.memoryUsed), total: bytes(data.memoryTotal) }) }}</span></footer></article>
      <article class="resource-card"><div class="resource-head"><span>{{ t('server.disk') }}</span><strong>{{ data.diskPercent.toFixed(1) }}%</strong></div><div class="resource-meter"><i :style="{ width: `${data.diskPercent}%` }"></i></div><footer><span>{{ t('server.usedOf', { used: bytes(data.diskUsed), total: bytes(data.diskTotal) }) }}</span></footer></article>
    </section>
    <section class="host-grid">
      <article class="load-card"><div><span class="eyebrow">LOAD AVERAGE</span><h2>{{ t('server.load') }}</h2><p>{{ t('server.loadHelp') }}</p></div><div class="load-values"><div><strong>{{ data.load1.toFixed(2) }}</strong><small>1 MIN</small></div><div><strong>{{ data.load5.toFixed(2) }}</strong><small>5 MIN</small></div><div><strong>{{ data.load15.toFixed(2) }}</strong><small>15 MIN</small></div></div></article>
      <article class="uptime-card"><span class="eyebrow">UPTIME</span><h2>{{ uptime(data.uptimeSeconds) }}</h2><p>{{ t('server.uptime') }}</p></article>
    </section>
    <section class="system-facts"><div><span>{{ t('server.hostname') }}</span><strong>{{ data.hostname || '—' }}</strong></div><div><span>{{ t('server.system') }}</span><strong>{{ data.os || '—' }}</strong></div><div><span>{{ t('server.kernel') }}</span><strong>{{ data.kernel || '—' }}</strong></div><div><span>{{ t('server.arch') }}</span><strong>{{ data.architecture }}</strong></div></section>
  </div>
  <div v-else class="loading">{{ t('server.loading') }}</div>
</template>
