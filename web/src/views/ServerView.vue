<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import { usePollStatus } from '../poll-status'
import { t } from '../i18n'
import type { Dashboard, TrafficUnit } from '../types'
import TrafficApps from '../components/TrafficApps.vue'
import TrafficHistory from '../components/TrafficHistory.vue'

type TrafficTab = 'history' | 'apps'

const emit = defineEmits<{ toast: [message: string] }>()
const data = ref<Dashboard | null>(null)
const activeTab = ref<TrafficTab>('history')
let timer = 0
let revision = 0
let disposed = false
let pending = 0
const { stale, tick, succeeded, failed } = usePollStatus()

const unit = computed<TrafficUnit>(() => data.value?.trafficQuota?.unit || 'GB')

const load = async () => {
  tick()
  if (pending) return
  const request = ++revision
  pending++
  try {
    const next = await api<Dashboard>('/api/dashboard', { signal: AbortSignal.timeout(10000) })
    if (disposed || request !== revision) return
    data.value = next
    succeeded()
  } catch {
    if (!disposed && request === revision) failed()
  } finally {
    pending--
  }
}
onMounted(() => { load(); timer = window.setInterval(load, 15000) })
onBeforeUnmount(() => { disposed = true; ++revision; clearInterval(timer) })
</script>

<template>
  <div class="page server-page traffic-page">
    <header class="page-head">
      <div>
        <span class="eyebrow">TRAFFIC / ANALYTICS</span>
        <h1>{{ t('traffic.title') }}</h1>
        <p>{{ t('traffic.help') }}</p>
      </div>
      <div class="history-switch traffic-tab-switch" role="group" :aria-label="t('traffic.title')">
        <button type="button" :aria-pressed="activeTab === 'history'" @click="activeTab = 'history'">{{ t('traffic.tabHistory') }}</button>
        <button type="button" :aria-pressed="activeTab === 'apps'" @click="activeTab = 'apps'">{{ t('traffic.tabApps') }}</button>
      </div>
    </header>
    <div v-if="stale" class="alert" role="status">{{ t('api.stale') }}</div>
    <TrafficHistory v-if="activeTab === 'history'" :unit="unit" />
    <TrafficApps v-else :unit="unit" @toast="message => emit('toast', message)" />
  </div>
</template>
