<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import type { TrafficHistory, TrafficUnit } from '../types'
import { dateLocale, t } from '../i18n'
import Icon from './Icon.vue'

const props = defineProps<{ unit: TrafficUnit }>()
const history = ref<TrafficHistory | null>(null)
const mode = ref<'day' | 'month'>('day')
const localNow = new Date()
const month = ref(`${localNow.getFullYear()}-${String(localNow.getMonth() + 1).padStart(2, '0')}`)
const year = ref(month.value.slice(0, 4))
const loading = ref(false)
const error = ref('')
let request = 0
let timer = 0
const initialized = ref(false)
const validSelection = computed(() => mode.value === 'day'
  ? /^(?:19[7-9]\d|[2-9]\d{3})-(?:0[1-9]|1[0-2])$/.test(month.value)
  : /^(?:19[7-9]\d|[2-9]\d{3})$/.test(year.value))
const rows = computed(() => [...(history.value?.rows ?? [])].reverse())
const total = computed(() => rows.value.reduce((sum, row) => sum + row.proxyUsedBytes, 0))
const peak = computed(() => Math.max(1, ...rows.value.map(row => row.proxyUsedBytes)))
const imported = computed(() => (history.value?.imports ?? []).reduce((sum, item) => sum + item.upload + item.download, 0))
const bytes = (value: number) => {
  const base = props.unit === 'GiB' ? 1024 : 1000
  const units = props.unit === 'GiB' ? ['B', 'KiB', 'MiB', 'GiB', 'TiB'] : ['B', 'KB', 'MB', 'GB', 'TB']
  const index = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(base))) : 0
  return `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(value / base ** index)} ${units[index]}`
}
const started = computed(() => history.value ? new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short', timeZone: history.value.timezone }).format(new Date(history.value.startedAt)) : '')

async function load(background = false) {
  const id = ++request
  if (initialized.value && !validSelection.value) {
    loading.value = false
    error.value = t('history.invalidPeriod')
    return
  }
  if (!background) loading.value = true
  if (!background) error.value = ''
  const query = new URLSearchParams({ granularity: mode.value })
  if (initialized.value) {
    const lastDay = new Date(Date.UTC(Number(month.value.slice(0, 4)), Number(month.value.slice(5, 7)), 0)).getUTCDate()
    query.set('from', mode.value === 'day' ? `${month.value}-01` : `${year.value}-01`)
    query.set('to', mode.value === 'day' ? `${month.value}-${String(lastDay).padStart(2, '0')}` : `${year.value}-12`)
  }
  try {
    const next = await api<TrafficHistory>(`/api/traffic/history?${query}`)
    if (id !== request) return
    if (!initialized.value) {
      // Let the database choose the initial month, then keep the selector in
      // its fixed statistics timezone instead of the browser's UTC month.
      const parts = new Intl.DateTimeFormat('en-US', { year: 'numeric', month: '2-digit', timeZone: next.timezone }).formatToParts(new Date())
      year.value = parts.find(part => part.type === 'year')!.value
      month.value = `${year.value}-${parts.find(part => part.type === 'month')!.value}`
      initialized.value = true
    }
    history.value = next
    error.value = ''
  } catch (reason) {
    if (id === request) error.value = reason instanceof Error ? reason.message : t('history.failed')
  } finally {
    if (id === request) loading.value = false
  }
}
function select(value: 'day' | 'month') {
  if (mode.value === value) return
  mode.value = value
  load()
}
function changeMonth() { ++request; loading.value = false; load() }
onMounted(() => { load(); timer = window.setInterval(() => load(true), 60000) })
onBeforeUnmount(() => { ++request; clearInterval(timer) })
</script>

<template>
  <section class="traffic-history" :aria-label="t('history.title')" :aria-busy="loading">
    <header class="history-head">
      <div><span class="eyebrow">TRAFFIC / HISTORY</span><h2>{{ t('history.title') }}</h2><p>{{ t('history.help') }}</p></div>
      <div class="history-controls">
        <div class="history-switch" :aria-label="t('history.granularity')" role="group">
          <button type="button" :disabled="!initialized" :aria-pressed="mode === 'day'" @click="select('day')">{{ t('history.daily') }}</button>
          <button type="button" :disabled="!initialized" :aria-pressed="mode === 'month'" @click="select('month')">{{ t('history.monthly') }}</button>
        </div>
        <label class="history-month"><span>{{ mode === 'day' ? t('history.selectMonth') : t('history.selectYear') }}</span><input v-if="mode === 'day'" v-model="month" :disabled="!initialized" type="month" min="1970-01" max="9999-12" required @change="changeMonth"><input v-else v-model="year" :disabled="!initialized" type="number" min="1970" max="9999" required @change="changeMonth"></label>
        <button class="secondary history-refresh" :class="{ refreshing: loading }" type="button" :disabled="loading || !validSelection" :aria-label="t('dashboard.refresh')" @click="load()"><Icon name="refresh" /></button>
      </div>
    </header>
    <p v-if="error" class="history-error" role="alert">{{ error }}</p>
    <span v-if="loading && history" class="history-loading" role="status">{{ t('history.loading') }}</span>
    <p v-if="!history && !error" class="history-empty">{{ t('history.loading') }}</p>
    <template v-if="history">
      <div class="history-summary"><strong>{{ bytes(total) }} <small>{{ t('history.proxyTotal') }}</small></strong><span>{{ t('history.since', { date: started, timezone: history.timezone }) }}</span></div>
      <p v-if="imported" class="history-note">{{ t('history.importedHelp', { amount: bytes(imported) }) }}</p>
      <div v-if="rows.length" class="history-table-scroll">
        <table class="history-table">
          <caption class="history-caption">{{ t('history.tableLabel', { timezone: history.timezone }) }}</caption>
          <thead><tr><th scope="col">{{ history.granularity === 'day' ? t('history.day') : t('history.month') }}</th><th scope="col">{{ t('dashboard.upload') }}</th><th scope="col">{{ t('dashboard.download') }}</th><th scope="col">{{ t('history.proxyTotal') }}</th><th scope="col">{{ t('history.providerTotal') }}</th></tr></thead>
          <tbody><tr v-for="row in rows" :key="row.date">
            <th scope="row"><span>{{ row.date }}</span><small v-if="row.imported">{{ t('history.imported') }}</small><small v-else-if="row.partial">{{ t('history.partial') }}</small></th>
            <td>{{ bytes(row.upload) }}</td><td>{{ bytes(row.download) }}</td>
            <td class="history-usage"><b>{{ bytes(row.proxyUsedBytes) }}</b><i aria-hidden="true" :style="{ width: `${row.proxyUsedBytes / peak * 100}%` }"></i></td>
            <td>{{ bytes(row.estimatedProviderUsedBytes) }}</td>
          </tr></tbody>
        </table>
      </div>
      <p v-if="rows.length" class="history-swipe">{{ t('history.swipe') }}</p>
      <p v-else class="history-empty">{{ t('history.empty') }}</p>
      <footer>{{ t('history.footnote') }}</footer>
    </template>
  </section>
</template>
