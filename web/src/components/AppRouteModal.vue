<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, guard, put } from '../api'
import type { AppRouteRuleView, AppRoutesView } from '../types'
import { t } from '../i18n'
import ConfirmAction from './ConfirmAction.vue'
import Icon from './Icon.vue'
import Modal from './Modal.vue'
import SelectControl from './SelectControl.vue'

const props = defineProps<{
  open: boolean
  initialData?: AppRoutesView | null
}>()

const emit = defineEmits<{
  close: []
  toast: [message: string]
  updated: [view: AppRoutesView]
}>()

const localView = ref<AppRoutesView | null>(null)
const loading = ref(false)
const savingId = ref('')
const selectedCat = ref('all')
// Selections wait here until applied together, so several edits cost one
// sing-box restart instead of one each.
const pending = ref<Record<string, string>>({})
// Bumped around every save so the fetch started on open cannot overwrite it.
let revision = 0

const view = computed(() => localView.value ?? props.initialData ?? null)
const gateways = computed(() => view.value?.gateways ?? [])
const routes = computed(() => view.value?.routes ?? {})

const allApps = computed<AppRouteRuleView[]>(() => {
  const base = view.value?.apps ?? []
  const siteItems: AppRouteRuleView[] = []
  for (const [key, target] of Object.entries(routes.value)) {
    if (key.startsWith('site:') && target) {
      const domain = key.slice(5)
      siteItems.push({
        id: key,
        name: domain,
        category: 'site',
        domainCount: 1,
        hasCidrs: false,
        target,
      })
    }
  }
  return [...base, ...siteItems]
})

const activeCount = computed(() => Object.keys(routes.value).length)
const pendingCount = computed(() => Object.keys(pending.value).length)
const targetOf = (id: string) => (id in pending.value ? pending.value[id] : routes.value[id] || '')

const categories = computed(() => {
  const seen = new Set<string>()
  const order: string[] = []
  for (const app of allApps.value) {
    if (!seen.has(app.category)) {
      seen.add(app.category)
      order.push(app.category)
    }
  }
  return order
})

const groupedApps = computed(() => {
  const filtered = allApps.value.filter(app => {
    const target = routes.value[app.id] || ''
    if (selectedCat.value === 'active') return target !== ''
    if (selectedCat.value === 'all') return app.category !== 'site' || target !== ''
    return app.category === selectedCat.value
  })
  const groups: { category: string; items: AppRouteRuleView[] }[] = []
  const map = new Map<string, AppRouteRuleView[]>()
  for (const app of filtered) {
    let list = map.get(app.category)
    if (!list) {
      list = []
      map.set(app.category, list)
      groups.push({ category: app.category, items: list })
    }
    list.push(app)
  }
  return groups
})

const appName = (app: { id: string; name: string }) => {
  const key = `apps.name.${app.id}`
  const localized = t(key)
  return localized !== key ? localized : app.name
}

const categoryLabel = (category: string) => {
  const key = `apps.cat.${category}`
  const localized = t(key)
  return localized !== key ? localized : category
}

// A route to a disabled gateway is skipped (traffic follows the node), and a
// route to a paused gateway is refused; both deserve a visible note.
const targetNotice = (id: string) => {
  const gateway = gateways.value.find(gw => gw.id === targetOf(id))
  if (!gateway) return ''
  if (!gateway.enabled) return t('apps.routeTargetDisabled')
  return gateway.paused ? t('apps.routeTargetPaused') : ''
}

// YouTube's stream URLs only work from the IP that requested them. When
// YouTube and Google services leave through different exits, a browser that
// reuses a Google connection for a YouTube request can stall or get 403s.
const coupledRoutes: Record<string, string> = { youtube: 'ecosystem:google' }
const splitNotice = (id: string) => {
  const other = coupledRoutes[id]
  return other && targetOf(id) !== targetOf(other) ? t('apps.routeSplitYouTube') : ''
}

const safe = guard(message => emit('toast', message))

async function fetchRoutes() {
  const current = revision
  loading.value = true
  try {
    const next = await api<AppRoutesView>('/api/egress/app-routes')
    if (current !== revision) return
    localView.value = next
    emit('updated', next)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  isOpen => {
    if (isOpen) {
      pending.value = {}
      if (props.initialData) {
        localView.value = props.initialData
      }
      fetchRoutes().catch(() => {})
    }
  },
  { immediate: true },
)

watch(
  () => props.initialData,
  next => {
    if (next) localView.value = next
  },
)

function stageRoute(appId: string, target: string) {
  const next = { ...pending.value }
  if (target === (routes.value[appId] || '')) delete next[appId]
  else next[appId] = target
  pending.value = next
}

const applyChanges = safe(async () => {
  const changes = { ...pending.value }
  if (!Object.keys(changes).length) return
  savingId.value = '__apply__'
  revision++
  try {
    const next = await put<AppRoutesView>('/api/egress/app-routes', { changes })
    localView.value = next
    pending.value = {}
    emit('updated', next)
    emit('toast', t('apps.routeSaved'))
    emit('close')
  } finally {
    revision++
    savingId.value = ''
  }
})

const routeOptions = computed(() => [
  { value: '', label: t('apps.routeDefault') },
  { value: 'direct', label: t('apps.routeDirect') },
  ...gateways.value.map(gw => ({
    value: gw.id,
    label: `${gw.name}${!gw.enabled ? t('apps.routeDisabledSuffix') : gw.paused ? t('apps.routePausedSuffix') : ''}`,
  })),
])

const resetAll = safe(async () => {
  savingId.value = '__all__'
  revision++
  try {
    const next = await put<AppRoutesView>('/api/egress/app-routes', { routes: {} })
    localView.value = next
    pending.value = {}
    if (selectedCat.value === 'active') selectedCat.value = 'all'
    emit('updated', next)
    emit('toast', t('apps.routeSaved'))
  } finally {
    revision++
    savingId.value = ''
  }
})
</script>

<template>
  <Modal
    v-if="open"
    :title="t('apps.routeTitle')"
    :description="t('apps.routeHelp')"
    wide
    @close="emit('close')"
  >
    <div class="app-route-modal">
      <p class="app-route-help">{{ t('apps.routeHelp') }}</p>
      <div class="app-route-toolbar">
        <div class="app-route-cats" role="group">
          <button
            type="button"
            class="app-route-cat-btn"
            :class="{ active: selectedCat === 'all' }"
            :aria-pressed="selectedCat === 'all'"
            @click="selectedCat = 'all'"
          >
            {{ t('apps.routeFilterAll') }}
          </button>
          <button
            v-if="activeCount > 0 || selectedCat === 'active'"
            type="button"
            class="app-route-cat-btn"
            :class="{ active: selectedCat === 'active' }"
            :aria-pressed="selectedCat === 'active'"
            @click="selectedCat = 'active'"
          >
            {{ t('apps.routeFilterActive', { count: activeCount }) }}
          </button>
          <button
            v-for="cat in categories"
            :key="cat"
            type="button"
            class="app-route-cat-btn"
            :class="{ active: selectedCat === cat }"
            :aria-pressed="selectedCat === cat"
            @click="selectedCat = cat"
          >
            {{ categoryLabel(cat) }}
          </button>
        </div>
        <ConfirmAction
          v-if="activeCount > 0"
          :title="t('apps.routeResetAll')"
          :message="t('apps.routeResetConfirm')"
          destructive
          @confirm="resetAll"
        >
          <button type="button" class="secondary app-action-btn" :disabled="!!savingId">
            <Icon name="refresh" />
            <span>{{ t('apps.routeResetAll') }}</span>
          </button>
        </ConfirmAction>
      </div>

      <div v-if="loading && !view" class="loading">{{ t('apps.loading') }}</div>
      <div v-else class="app-route-groups">
        <section v-for="group in groupedApps" :key="group.category" class="app-route-group">
          <header class="app-route-group-head">
            <span class="app-cat-badge" :data-cat="group.category">{{ categoryLabel(group.category) }}</span>
            <small>{{ group.items.length }}</small>
          </header>
          <div class="app-route-list">
            <div
              v-for="app in group.items"
              :key="app.id"
              class="app-route-item"
              :class="{ overridden: !!targetOf(app.id), pending: app.id in pending }"
            >
              <div class="app-route-info">
                <strong>{{ appName(app) }}</strong>
                <div class="app-route-meta">
                  <small>{{ t('apps.routeRulesCount', { count: app.domainCount }) }}</small>
                  <small v-if="app.hasCidrs" class="app-cidr-pill">{{ t('apps.routeHasCidrs') }}</small>
                  <small v-if="app.id in pending" class="app-route-pending-pill">{{ t('apps.routePendingTag') }}</small>
                </div>
                <small v-if="targetNotice(app.id)" class="traffic-source warning" role="status">{{ targetNotice(app.id) }}</small>
                <small v-if="splitNotice(app.id)" class="traffic-source warning">{{ splitNotice(app.id) }}</small>
              </div>
              <div class="app-route-control">
                <SelectControl
                  :model-value="targetOf(app.id)"
                  :options="routeOptions"
                  size="sm"
                  :active="!!targetOf(app.id)"
                  :disabled="!!savingId"
                  :aria-label="appName(app)"
                  @update:model-value="val => stageRoute(app.id, val)"
                />
              </div>
            </div>
          </div>
        </section>
      </div>

      <div class="modal-actions app-route-actions">
        <button type="button" class="secondary" :disabled="!!savingId" @click="emit('close')">{{ t('protocol.cancel') }}</button>
        <button type="button" class="primary" :disabled="!pendingCount || !!savingId" @click="applyChanges">
          {{ savingId === '__apply__' ? t('apps.routeApplying') : pendingCount ? t('apps.routeApplyCount', { count: pendingCount }) : t('apps.routeApply') }}
        </button>
      </div>
    </div>
  </Modal>
</template>

