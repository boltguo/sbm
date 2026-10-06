<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { api, del, guard, post, put } from '../api'
import type { EgressGateway, GatewayInput } from '../types'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import PasswordInput from '../components/PasswordInput.vue'
import SelectControl from '../components/SelectControl.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import EgressGuide from '../components/EgressGuide.vue'
import { dateLocale, t } from '../i18n'

const emit = defineEmits<{ toast: [message: string] }>()
const items = ref<EgressGateway[]>([])
const loaded = ref(false)
const busy = ref(false)
const form = ref<GatewayInput | null>(null)
const editing = ref<EgressGateway | null>(null)
const publicKey = ref('')
let timer = 0
let keyRevision = 0
let listRevision = 0
const safe = guard(message => emit('toast', message))
const billingOptions = computed(() => [{ value: 'single', label: t('settings.billingSingle') }, { value: 'bidirectional', label: t('settings.billingBidirectional') }])
const resetOptions = computed(() => [{ value: 'none', label: t('settings.noReset') }, { value: 'monthly', label: t('settings.monthly') }])
const unitOptions = [{ value: 'GB', label: 'GB' }, { value: 'GiB', label: 'GiB' }]
const amount = (bytes: number, unit: string) => `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(bytes / (unit === 'GiB' ? 1024 ** 3 : 1000 ** 3))} ${unit}`
const date = (value?: string) => !value || value.startsWith('0001') ? t('settings.noReset') : new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
async function load() {
  const revision = ++listRevision
  const next = await api<EgressGateway[]>('/api/egress')
  if (revision !== listRevision) return
  items.value = next; loaded.value = true
}
const refresh = safe(load)
function input(g: GatewayInput): GatewayInput {
  const { enabled, marker, position, server, serverPort, privateKey, peerPublicKey, locationOverride, trafficQuota, reset } = g
  return JSON.parse(JSON.stringify({ enabled, marker, position, server, serverPort, privateKey, peerPublicKey, locationOverride, trafficQuota, reset }))
}
function edit(g: EgressGateway) { ++listRevision; editing.value = g; form.value = input(g); publicKey.value = g.publicKey; keyRevision++ }
function close() { form.value = null; editing.value = null; publicKey.value = ''; keyRevision++ }
const derive = safe(async () => {
  const privateKey = form.value?.privateKey
  const revision = ++keyRevision
  publicKey.value = ''
  if (!privateKey) return
  const keys = await post<{ publicKey: string }>('/api/egress/public-key', { privateKey })
  if (revision === keyRevision && form.value?.privateKey === privateKey) publicKey.value = keys.publicKey
})
const generate = safe(async () => {
  const revision = ++keyRevision
  const keys = await post<{ privateKey: string; publicKey: string }>('/api/egress/keypair')
  if (form.value && revision === keyRevision) { form.value.privateKey = keys.privateKey; publicKey.value = keys.publicKey }
})
const create = safe(async () => {
  ++listRevision
  editing.value = null; publicKey.value = ''; keyRevision++
  form.value = { enabled: false, marker: '', position: items.value.length ? Math.max(...items.value.map(g => g.position)) + 1 : 0, server: '', serverPort: 51820, privateKey: '', peerPublicKey: '', locationOverride: '', trafficQuota: { amount: 0, unit: 'GB', billingMode: 'single', headroomPercent: 10 }, reset: { mode: 'none', day: 1, timezone: 'UTC' } }
  await generate()
})
const run = safe(async (action: () => Promise<unknown>) => {
  if (busy.value) return
  busy.value = true
  ++listRevision
  try { await action(); await load() } finally { busy.value = false }
})
const save = () => run(async () => {
  if (!form.value) return
  const result = editing.value ? await put<{ geoDetected: boolean }>(`/api/egress/${editing.value.id}`, input(form.value)) : await post<{ geoDetected: boolean }>('/api/egress', input(form.value))
  close(); emit('toast', result.geoDetected ? t('egress.saved') : t('egress.savedGeoFailed'))
})
const toggle = (g: EgressGateway) => run(async () => { await put(`/api/egress/${g.id}`, { ...input(g), enabled: !g.enabled }); emit('toast', t('egress.saved')) })
const remove = (g: EgressGateway) => run(async () => { await del(`/api/egress/${g.id}`); emit('toast', t('egress.deleted')) })
const detect = (g: EgressGateway) => run(async () => { const result = await post<{ geoDetected: boolean }>(`/api/egress/${g.id}/geo`); emit('toast', result.geoDetected ? t('egress.geoDone') : t('egress.geoFailed')) })
const reset = (g: EgressGateway) => run(async () => { await post(`/api/egress/${g.id}/reset`); emit('toast', t('egress.resetDone')) })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('copied')) })
onMounted(() => { refresh(); timer = window.setInterval(() => { if (!busy.value && !form.value) load().catch(() => {}) }, 5000) })
onBeforeUnmount(() => { ++listRevision; ++keyRevision; clearInterval(timer) })
</script>

<template>
  <div class="page egress-page">
    <header class="page-head"><div><span class="eyebrow">WIREGUARD / IPv4</span><h1>{{ t('egress.title') }}</h1><p>{{ t('egress.help') }}</p></div><button class="primary" :disabled="busy" @click="create"><Icon name="plus"/>{{ t('egress.add') }}</button></header>
    <div class="egress-flow"><span>{{ t('egress.client') }}</span><b>→</b><span>{{ t('egress.entry') }}</span><b>→</b><strong>{{ t('egress.gateway') }}</strong><b>→</b><span>Internet</span></div>
    <p class="egress-source">{{ t('egress.estimate') }} · {{ t('egress.warningOnly') }}</p>
    <EgressGuide/>
    <div v-if="!loaded" class="loading">{{ t('egress.loading') }}</div>
    <div v-else-if="!items.length" class="egress-empty"><Icon name="route"/><h2>{{ t('egress.empty') }}</h2><p>{{ t('egress.emptyHelp') }}</p><button class="secondary" @click="create">{{ t('egress.add') }}</button></div>
    <div v-else class="protocol-grid egress-grid">
      <article v-for="g in items" :key="g.id" class="protocol-card gateway-card" :class="{ disabled: !g.enabled }">
        <div class="protocol-top"><div class="protocol-glyph">WG</div><div><span class="pill">{{ g.enabled ? t('egress.enabled') : t('egress.disabled') }}</span><h2>{{ g.name }}</h2></div><SwitchRoot :model-value="g.enabled" :disabled="busy" class="relative h-6 w-11 shrink-0 rounded-full border border-[var(--ink)] bg-transparent p-[3px] data-[state=checked]:bg-[var(--signal)]" :aria-label="`${t(g.enabled ? 'protocol.disable' : 'protocol.enable')} ${g.name}`" @update:model-value="toggle(g)"><SwitchThumb class="block size-4 rounded-full bg-[var(--muted)] transition-transform data-[state=checked]:translate-x-[18px] data-[state=checked]:bg-[var(--ink)]" /></SwitchRoot></div>
        <div class="endpoint"><span>{{ t('egress.exitIP') }}</span><code>{{ g.server || '—' }}</code><b>UDP/{{ g.serverPort }}</b></div>
        <dl><div><dt>{{ t('egress.detected') }}</dt><dd>{{ [g.geo.countryCode, g.geo.region, g.geo.city].filter(Boolean).join(' / ') || t('egress.unknown') }}</dd></div><div v-if="g.locationOverride"><dt>{{ t('egress.override') }}</dt><dd>{{ g.locationOverride }}</dd></div><div><dt>{{ t('egress.aAddress') }}</dt><dd>{{ g.tunnelAddress }}</dd></div><div><dt>{{ t('egress.bAddress') }}</dt><dd>{{ g.peerAddress }}</dd></div><div><dt>{{ t('settings.planAllowance') }}</dt><dd>{{ g.trafficQuota.amount ? `${g.trafficQuota.amount} ${g.trafficQuota.unit}` : t('settings.unlimited') }} · {{ t(g.trafficQuota.billingMode === 'bidirectional' ? 'settings.billingBidirectional' : 'settings.billingSingle') }}</dd></div><div><dt>{{ t('settings.autoReset') }}</dt><dd>{{ g.reset.mode === 'monthly' ? `${t('egress.resetDay', { day: g.reset.day })} · ${g.reset.timezone}` : t('settings.noReset') }}</dd></div></dl>
        <div class="gateway-usage" :class="{ warning: g.usage.warning }"><strong>{{ amount(g.usage.estimatedProviderUsedBytes, g.trafficQuota.unit) }}</strong><span v-if="g.usage.providerAllowanceBytes"> / {{ amount(g.usage.providerAllowanceBytes, g.trafficQuota.unit) }}</span><small>{{ t(`egress.sample.${g.usage.sampleHealth.status}`) }}</small><p v-if="g.usage.persistenceHealth?.status === 'interrupted'" class="traffic-source warning" role="status">{{ t('egress.persistenceFailed') }}</p><p v-if="g.usage.sampleHealth.partial" class="traffic-source warning">{{ t('egress.partialUsage') }}</p><div class="progress-track"><i :style="{ width: `${g.usage.providerProgress}%` }"></i></div><p v-if="g.usage.warning">{{ t('egress.quotaWarning') }}</p><p v-else-if="g.usage.providerAllowanceBytes">{{ t('egress.remaining', { amount: amount(g.usage.providerRemainingBytes, g.trafficQuota.unit) }) }}</p><small>{{ t('egress.nextReset', { date: date(g.usage.nextResetAt) }) }}</small></div>
        <div class="card-actions gateway-actions"><button :disabled="busy" @click="edit(g)"><Icon name="edit"/>{{ t('egress.edit') }}</button><button :disabled="busy || !g.server" :title="t('egress.redetect')" @click="detect(g)"><Icon name="refresh"/>{{ t('egress.redetect') }}</button><ConfirmAction :title="t('egress.resetTraffic')" :message="t('egress.resetConfirm')" @confirm="reset(g)"><button :disabled="busy" :aria-label="t('egress.resetTraffic')"><Icon name="refresh"/></button></ConfirmAction><ConfirmAction :title="g.name" :message="t('egress.deleteConfirm')" destructive @confirm="remove(g)"><button :disabled="busy" class="destructive" :aria-label="`${t('egress.delete')} ${g.name}`"><Icon name="trash"/></button></ConfirmAction></div>
      </article>
    </div>
    <Modal v-if="form" :title="editing ? t('egress.edit') : t('egress.add')" :description="t('egress.setupHelp')" wide @close="close">
      <form class="form-grid two gateway-form" @submit.prevent="save">
        <div class="alert subtle span-two">{{ t('egress.setupHelp') }}</div>
        <label>{{ t('egress.marker') }}<input v-model.trim="form.marker" maxlength="80" :placeholder="t('egress.markerPlaceholder')"></label>
        <label>{{ t('egress.position') }}<input v-model.number="form.position" type="number" step="1" required></label>
        <label>{{ t('egress.exitIP') }}<input v-model.trim="form.server" :required="form.enabled" placeholder="203.0.113.10"></label>
        <label>{{ t('egress.port') }}<input v-model.number="form.serverPort" type="number" min="1" max="65535" required></label>
        <label class="span-two">{{ t('egress.privateKey') }}<PasswordInput v-model="form.privateKey" :aria-label="t('egress.privateKey')" autocomplete="off" :required="form.enabled" @update:model-value="publicKey = ''; keyRevision++" @focusout="derive" /></label>
        <div class="span-two gateway-key-actions"><button type="button" class="secondary" @click="generate">{{ t('egress.generateKeys') }}</button><small>{{ t('egress.keyHelp') }}</small></div>
        <div class="span-two"><label>{{ t('egress.publicKey') }}</label><div class="copy-field"><code>{{ publicKey || t('egress.noPublicKey') }}</code><button type="button" :disabled="!publicKey" @click="copy(publicKey)"><Icon name="copy"/>{{ t('settings.copy') }}</button></div></div>
        <label class="span-two">{{ t('egress.peerKey') }}<input v-model.trim="form.peerPublicKey" autocomplete="off" :required="form.enabled"></label>
        <div v-if="editing" class="gateway-addresses span-two"><span>{{ t('egress.aAddress') }} <code>{{ editing.tunnelAddress }}</code></span><span>{{ t('egress.bAddress') }} <code>{{ editing.peerAddress }}</code></span></div>
        <label class="span-two">{{ t('egress.override') }}<input v-model.trim="form.locationOverride" maxlength="80" placeholder="JP-Tokyo"><small>{{ t('egress.overrideHelp') }}</small></label>
        <label>{{ t('settings.quotaAmount') }}<input v-model.number="form.trafficQuota.amount" type="number" min="0" step="0.01" required></label>
        <label>{{ t('settings.quotaUnit') }}<SelectControl v-model="form.trafficQuota.unit" :options="unitOptions" /></label>
        <label>{{ t('settings.billingMode') }}<SelectControl v-model="form.trafficQuota.billingMode" :options="billingOptions" /></label>
        <label>{{ t('settings.headroom') }}<span class="input-suffix"><input v-model.number="form.trafficQuota.headroomPercent" type="number" min="0" max="50" step="1" required><b>%</b></span></label>
        <label>{{ t('settings.autoReset') }}<SelectControl v-model="form.reset.mode" :options="resetOptions" /></label>
        <label v-if="form.reset.mode === 'monthly'">{{ t('settings.day') }}<input v-model.number="form.reset.day" type="number" min="1" max="28" step="1" required></label>
        <label v-if="form.reset.mode === 'monthly'" class="span-two">{{ t('settings.timezone') }}<input v-model.trim="form.reset.timezone" placeholder="Asia/Shanghai" required></label>
        <label class="gateway-enabled span-two"><input v-model="form.enabled" type="checkbox">{{ t('egress.enableForm') }}</label>
        <div class="modal-actions span-two"><button type="button" class="secondary" :disabled="busy" @click="close">{{ t('protocol.cancel') }}</button><button class="primary" :disabled="busy">{{ busy ? t('egress.saving') : t('egress.save') }}</button></div>
      </form>
    </Modal>
  </div>
</template>
