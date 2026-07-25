<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import { dateLocale, t } from '../i18n'
import type { ServerStatus } from '../types'

const data = ref<ServerStatus | null>(null)
let timer = 0

const bytes = (value: number) => {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
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
// Read-only polling: a 401 already returns to the login screen, and any other
// failure simply leaves the last snapshot on screen until the next tick.
const load = () => { api<ServerStatus>('/api/server').then(next => { data.value = next }).catch(() => {}) }
onMounted(() => { load(); timer = window.setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div v-if="data" class="page server-page">
    <header class="page-head"><div><span class="eyebrow">HOST / LIVE</span><h1>{{ t('server.title') }}</h1><p>{{ t('server.help') }}</p></div><small class="server-updated">{{ t('server.updated', { time: time(data.collectedAt) }) }}</small></header>
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
