<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, guard, post } from '../api'
import type { AppTrafficResponse, AppUsage, TrafficUnit } from '../types'
import { dateLocale, t } from '../i18n'
import ConfirmAction from './ConfirmAction.vue'
import Icon from './Icon.vue'

const props = defineProps<{ unit: TrafficUnit }>()
const emit = defineEmits<{ toast: [message: string] }>()

const data = ref<AppTrafficResponse | null>(null)
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
let timer = 0

const validSelection = computed(() =>
  period.value === 'day'
    ? /^(?:19[7-9]\d|[2-9]\d{3})-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])$/.test(day.value)
    : /^(?:19[7-9]\d|[2-9]\d{3})-(?:0[1-9]|1[0-2])$/.test(month.value),
)

const apps = computed(() => data.value?.apps ?? [])
const totalBytes = computed(() => data.value?.totalBytes ?? 0)

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
  if (initialized.value) {
    query.set('date', period.value === 'day' ? day.value : month.value)
  }
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

const safe = guard(message => emit('toast', message))
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
        <label class="history-month">
          <span>{{ period === 'day' ? t('apps.selectDay') : t('apps.selectMonth') }}</span>
          <input
            v-if="period === 'day'"
            v-model="day"
            :disabled="!initialized"
            type="date"
            min="1970-01-01"
            max="9999-12-31"
            required
            @change="changeDate"
          />
          <input
            v-else
            v-model="month"
            :disabled="!initialized"
            type="month"
            min="1970-01"
            max="9999-12"
            required
            @change="changeDate"
          />
        </label>
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
        <ConfirmAction
          v-if="apps.length > 0"
          :title="t('apps.clear')"
          :message="t('apps.clearConfirm')"
          :destructive="true"
          @confirm="clearStats"
        >
          <button class="secondary app-clear-btn" type="button" :disabled="clearing">
            <Icon name="trash" />
            <span>{{ t('apps.clear') }}</span>
          </button>
        </ConfirmAction>
      </div>
    </header>

    <p v-if="error" class="history-error" role="alert">{{ error }}</p>
    <span v-if="loading && data" class="history-loading" role="status">{{ t('apps.loading') }}</span>
    <p v-if="!data && !error" class="history-empty">{{ t('apps.loading') }}</p>

    <template v-if="data">
      <p v-if="totalBytes > 0" class="history-note">
        {{ t('apps.proxyTotal', { amount: bytes(totalBytes) }) }} · ↑ {{ bytes(data.totalUpload) }} / ↓ {{ bytes(data.totalDownload) }}
      </p>

      <div v-if="apps.length" class="history-table-scroll">
        <table class="history-table app-rank-table">
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
  </section>
</template>

