<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, guard, post, put } from '../api'
import type { AppRoutesView, AppTrafficResponse, AppUsage, TrafficUnit } from '../types'
import { dateLocale, t } from '../i18n'
import AppRouteModal from './AppRouteModal.vue'
import ConfirmAction from './ConfirmAction.vue'
import DatePickerControl from './DatePickerControl.vue'
import Icon from './Icon.vue'
import SelectControl from './SelectControl.vue'

const props = defineProps<{ unit: TrafficUnit }>()
const emit = defineEmits<{ toast: [message: string] }>()

const data = ref<AppTrafficResponse | null>(null)
const routesView = ref<AppRoutesView | null>(null)
const routeModalOpen = ref(false)
const savingRouteId = ref('')
const period = ref<'day' | 'month'>('day')
const localNow = new Date()
const day = ref(
  `${localNow.getFullYear()}-${String(localNow.getMonth() + 1).padStart(2, '0')}-${String(localNow.getDate()).padStart(2, '0')}`,
)
const month = ref(day.value.slice(0, 7))
const loading = ref(false)
const clearing = ref(false)
const error = ref('')
const initialized = ref(false)
const expanded = ref<Record<string, boolean>>({})
let request = 0
// Bumped around every route save so a poll that started earlier cannot put
// the previous routes back while sing-box restarts.
let routesRevision = 0
let timer = 0

const validSelection = computed(() =>
  period.value === 'day'
    ? /^(?:19[7-9]\d|[2-9]\d{3})-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])$/.test(day.value)
    : /^(?:19[7-9]\d|[2-9]\d{3})-(?:0[1-9]|1[0-2])$/.test(month.value),
)

const apps = computed(() => data.value?.apps ?? [])
const totalBytes = computed(() => data.value?.totalBytes ?? 0)
const gateways = computed(() => routesView.value?.gateways ?? [])
const activeRoutes = computed(() => routesView.value?.routes ?? {})
const activeRouteCount = computed(() => Object.keys(activeRoutes.value).length)

const isRoutable = (id: string) => !id.startsWith('ip:') && id !== 'other'

const bytes = (value: number) => {
  const base = props.unit === 'GiB' ? 1024 : 1000
  const units = props.unit === 'GiB' ? ['B', 'KiB', 'MiB', 'GiB', 'TiB'] : ['B', 'KB', 'MB', 'GB', 'TB']
  const index = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(base))) : 0
  return `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(value / base ** index)} ${units[index]}`
}

const share = (value: number, total: number) => {
  if (!total || value <= 0) return '0%'
  const pct = (value / total) * 100
  if (pct < 0.1) return '<0.1%'
  return `${pct.toFixed(1)}%`
}

const shareWidth = (value: number, total: number) => {
  if (!total || value <= 0) return '0%'
  return `${Math.min(100, Math.max(1.5, (value / total) * 100)).toFixed(1)}%`
}

const appName = (app: AppUsage) => {
  const key = `apps.name.${app.id}`
  const localized = t(key)
  return localized !== key ? localized : app.name
}

const categoryLabel = (category: string) => {
  const key = `apps.cat.${category}`
  const localized = t(key)
  return localized !== key ? localized : category
}

const toggleExpand = (id: string) => {
  expanded.value[id] = !expanded.value[id]
}

const browserTimezone = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
})()

async function load(background = false) {
  const id = ++request
  if (initialized.value && !validSelection.value) {
    loading.value = false
    error.value = t('apps.invalidPeriod')
    return
  }
  if (!background) loading.value = true
  if (!background) error.value = ''
  const query = new URLSearchParams({ period: period.value })
  if (browserTimezone) {
    query.set('tz', browserTimezone)
  }
  if (initialized.value) {
    query.set('date', period.value === 'day' ? day.value : month.value)
  }
  const routesRequest = routesRevision
  api<AppRoutesView>('/api/egress/app-routes')
    .then(res => {
      if (id === request && routesRequest === routesRevision) routesView.value = res
    })
    .catch(() => {})
  try {
    const next = await api<AppTrafficResponse>(`/api/traffic/apps?${query}`)
    if (id !== request) return
    if (!initialized.value) {
      const parts = new Intl.DateTimeFormat('en-US', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        timeZone: next.timezone,
      }).formatToParts(new Date())
      const y = parts.find(p => p.type === 'year')!.value
      const m = parts.find(p => p.type === 'month')!.value
      const d = parts.find(p => p.type === 'day')!.value
      month.value = `${y}-${m}`
      day.value = `${y}-${m}-${d}`
      initialized.value = true
    }
    data.value = next
    error.value = ''
  } catch (reason) {
    if (id === request) error.value = reason instanceof Error ? reason.message : t('apps.failed')
  } finally {
    if (id === request) loading.value = false
  }
}

function selectPeriod(value: 'day' | 'month') {
  if (period.value === value) return
  period.value = value
  load()
}

function changeDate() {
  ++request
  loading.value = false
  load()
}

function exportJSON() {
  if (!data.value || !apps.value.length) return
  const current = data.value
  const unmatchedSites = apps.value
    .filter(item => item.category === 'site')
    .map(item => ({
      domain: item.name,
      upload: item.upload,
      download: item.download,
      total: item.total,
      share: share(item.total, current.totalBytes),
    }))
  const ecosystemServices = apps.value
    .filter(item => item.category === 'ecosystem' || item.category === 'cdn')
    .map(item => ({
      id: item.id,
      name: item.name,
      category: item.category,
      upload: item.upload,
      download: item.download,
      total: item.total,
      share: share(item.total, current.totalBytes),
      domains: item.domains,
    }))
  const matchedApps = apps.value
    .filter(item => item.category !== 'site' && item.category !== 'ecosystem' && item.category !== 'cdn')
    .map(item => ({
      id: item.id,
      name: item.name,
      category: item.category,
      upload: item.upload,
      download: item.download,
      total: item.total,
      share: share(item.total, current.totalBytes),
      domains: item.domains,
    }))

  const report = {
    exportedAt: new Date().toISOString(),
    period: current.period,
    date: current.date,
    timezone: current.timezone,
    totalUpload: current.totalUpload,
    totalDownload: current.totalDownload,
    totalBytes: current.totalBytes,
    unmatchedSites,
    ecosystemServices,
    matchedApps,
  }
  const blob = new Blob([JSON.stringify(report, null, 2)], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `sbm-traffic-apps-${current.date}.json`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
  emit('toast', t('apps.exportDone'))
}

const safe = guard(message => emit('toast', message))
const changeInlineRoute = safe(async (appId: string, target: string) => {
  savingRouteId.value = appId
  routesRevision++
  try {
    const next = await put<AppRoutesView>('/api/egress/app-routes', { appId, target })
    routesView.value = next
    emit('toast', t('apps.routeSaved'))
  } finally {
    routesRevision++
    savingRouteId.value = ''
  }
})
const applyRoutes = (next: AppRoutesView) => {
  routesRevision++
  routesView.value = next
}
const clearStats = safe(async () => {
  if (clearing.value) return
  clearing.value = true
  ++request
  try {
    await post('/api/traffic/apps/clear')
    expanded.value = {}
    emit('toast', t('apps.clearDone'))
    await load()
  } finally {
    clearing.value = false
  }
})

const routeOptions = computed(() => [
  { value: '', label: t('apps.routeDefault') },
  { value: 'direct', label: t('apps.routeDirect') },
  ...gateways.value.map(gw => ({
    value: gw.id,
    label: `${gw.name}${gw.enabled ? '' : t('apps.routeDisabledSuffix')}`,
  })),
])

onMounted(() => {
  load()
  timer = window.setInterval(() => load(true), 10000)
})
onBeforeUnmount(() => {
  ++request
  clearInterval(timer)
})
</script>

<template>
  <section class="traffic-history traffic-apps" :aria-label="t('apps.title')" :aria-busy="loading">
    <header class="history-head">
      <div>
        <span class="eyebrow">TRAFFIC / APPLICATIONS</span>
        <h2>{{ t('apps.title') }}</h2>
        <p>{{ t('apps.help') }}</p>
      </div>
      <div class="history-controls">
        <div class="history-switch" role="group">
          <button type="button" :disabled="!initialized" :aria-pressed="period === 'day'" @click="selectPeriod('day')">
            {{ t('apps.daily') }}
          </button>
          <button type="button" :disabled="!initialized" :aria-pressed="period === 'month'" @click="selectPeriod('month')">
            {{ t('apps.monthly') }}
          </button>
        </div>
        <div class="history-month">
          <span>{{ period === 'day' ? t('apps.selectDay') : t('apps.selectMonth') }}</span>
          <DatePickerControl
            v-if="period === 'day'"
            v-model="day"
            type="date"
            :time-zone="data?.timezone"
            :disabled="!initialized"
            :aria-label="t('apps.selectDay')"
            @change="changeDate"
          />
          <DatePickerControl
            v-else
            v-model="month"
            type="month"
            :time-zone="data?.timezone"
            :disabled="!initialized"
            :aria-label="t('apps.selectMonth')"
            @change="changeDate"
          />
        </div>
        <button
          class="secondary history-refresh"
          :class="{ refreshing: loading }"
          type="button"
          :disabled="loading || !validSelection"
          :aria-label="t('dashboard.refresh')"
          @click="load()"
        >
          <Icon name="refresh" />
        </button>
      </div>
    </header>

    <p v-if="error" class="history-error" role="alert">{{ error }}</p>
    <span v-if="loading && data" class="history-loading" role="status">{{ t('apps.loading') }}</span>
    <p v-if="!data && !error" class="history-empty">{{ t('apps.loading') }}</p>

    <template v-if="data">
      <div class="history-note app-summary-bar">
        <span>
          {{ t('apps.proxyTotal', { amount: bytes(totalBytes) }) }} · ↑ {{ bytes(data.totalUpload) }} / ↓ {{ bytes(data.totalDownload) }}
        </span>
        <div class="app-summary-actions">
          <button
            class="secondary app-action-btn app-route-trigger"
            :class="{ 'has-active': activeRouteCount > 0 }"
            type="button"
            @click="routeModalOpen = true"
          >
            <Icon name="route" />
            <span>{{ t('apps.routeButton') }}</span>
            <b v-if="activeRouteCount > 0" class="app-route-badge">{{ activeRouteCount }}</b>
          </button>
          <button
            v-if="apps.length > 0"
            class="secondary app-action-btn"
            type="button"
            @click="exportJSON"
          >
            <Icon name="download" />
            <span>{{ t('apps.export') }}</span>
          </button>
          <ConfirmAction
            v-if="apps.length > 0"
            :title="t('apps.clear')"
            :message="t('apps.clearConfirm')"
            :destructive="true"
            @confirm="clearStats"
          >
            <button class="secondary app-action-btn" type="button" :disabled="clearing">
              <Icon name="trash" />
              <span>{{ t('apps.clear') }}</span>
            </button>
          </ConfirmAction>
        </div>
      </div>

      <div v-if="apps.length" class="history-table-scroll">
        <table class="history-table app-rank-table">
          <caption class="history-caption">{{ t('history.tableLabel', { timezone: data.timezone }) }}</caption>
          <thead>
            <tr>
              <th scope="col">{{ t('apps.app') }}</th>
              <th scope="col">{{ t('apps.upload') }}</th>
              <th scope="col">{{ t('apps.download') }}</th>
              <th scope="col">{{ t('apps.total') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="(item, index) in apps" :key="item.id">
              <tr
                class="app-rank-row"
                :class="{ expanded: !!expanded[item.id] }"
                tabindex="0"
                role="button"
                :aria-expanded="!!expanded[item.id]"
                @click="toggleExpand(item.id)"
                @keydown.enter.prevent="toggleExpand(item.id)"
                @keydown.space.prevent="toggleExpand(item.id)"
              >
                <th scope="row">
                  <div class="app-rank-title">
                    <span class="app-rank-num">{{ String(index + 1).padStart(2, '0') }}</span>
                    <span class="app-rank-name">{{ appName(item) }}</span>
                    <span class="app-cat-badge" :data-cat="item.category">{{ categoryLabel(item.category) }}</span>
                    <span v-if="item.domains.length > 1 || (item.domains.length === 1 && item.domains[0].domain !== item.name)" class="app-domain-count">
                      {{ item.domains.length > 1 ? t('apps.domainsCount', { count: item.domains.length }) : item.domains[0].domain }}
                    </span>
                    <div
                      v-if="isRoutable(item.id) && (gateways.length > 0 || activeRoutes[item.id])"
                      class="app-route-inline-wrap"
                      @click.stop
                      @keydown.stop
                    >
                      <SelectControl
                        :model-value="activeRoutes[item.id] || ''"
                        :options="routeOptions"
                        size="xs"
                        :active="!!activeRoutes[item.id]"
                        :disabled="savingRouteId === item.id"
                        :aria-label="`${appName(item)} · ${t('apps.routeButton')}`"
                        @update:model-value="val => changeInlineRoute(item.id, val)"
                      />
                    </div>
                  </div>
                  <div class="app-share-track">
                    <i :style="{ width: shareWidth(item.total, totalBytes) }"></i>
                  </div>
                </th>
                <td>{{ bytes(item.upload) }}</td>
                <td>{{ bytes(item.download) }}</td>
                <td class="history-usage">
                  <b>{{ bytes(item.total) }}</b>
                  <small class="history-reference">{{ share(item.total, totalBytes) }}</small>
                </td>
              </tr>
              <tr
                v-for="dom in expanded[item.id] ? item.domains : []"
                :key="`${item.id}:${dom.domain}`"
                class="app-domain-row"
              >
                <th scope="row">
                  <span class="app-domain-branch">↳</span>
                  <code>{{ dom.domain }}</code>
                </th>
                <td>{{ bytes(dom.upload) }}</td>
                <td>{{ bytes(dom.download) }}</td>
                <td class="history-usage">
                  <span>{{ bytes(dom.total) }}</span>
                  <small class="history-reference">{{ share(dom.total, item.total) }}</small>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <p v-if="apps.length" class="history-swipe">{{ t('history.swipe') }}</p>
      <p v-else class="history-empty">{{ t('apps.empty') }}</p>
      <footer>{{ t('apps.footnote') }}</footer>
    </template>

    <AppRouteModal
      :open="routeModalOpen"
      :initial-data="routesView"
      @close="routeModalOpen = false"
      @updated="applyRoutes"
      @toast="emit('toast', $event)"
    />
  </section>
</template>

