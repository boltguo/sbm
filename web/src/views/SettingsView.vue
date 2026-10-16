<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, guard, post, put } from '../api'
import type { HealthCheck, HealthStatus, ServerStatus, Settings } from '../types'
import Icon from '../components/Icon.vue'
import SelectControl from '../components/SelectControl.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import PasswordInput from '../components/PasswordInput.vue'
import { dateLocale, t } from '../i18n'

const emit = defineEmits<{ toast: [message: string]; loggedOut: [] }>()
const settings = ref<Settings | null>(null)
const server = ref<ServerStatus | null>(null)
const password = ref({ currentPassword: '', newPassword: '', confirm: '' })
let timer = 0
let disposed = false
const timezones = ['Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Los_Angeles', 'UTC', 'Local']
const resetOptions = computed(() => [{ value: 'none', label: t('settings.noReset') }, { value: 'monthly', label: t('settings.monthly') }])
const outboundOptions = computed(() => [
  { value: 'auto', label: t('settings.outboundAuto') },
  { value: 'prefer_ipv4', label: t('settings.outboundPreferIPv4') },
  { value: 'prefer_ipv6', label: t('settings.outboundPreferIPv6') },
  { value: 'ipv4_only', label: t('settings.outboundIPv4Only') },
  { value: 'ipv6_only', label: t('settings.outboundIPv6Only') },
])
const timezoneOptions = timezones.map(value => ({ value, label: value }))
const billingOptions = computed(() => [
  { value: 'bidirectional', label: t('settings.billingBidirectional') },
  { value: 'single', label: t('settings.billingSingle') },
])
const unitOptions = [
  { value: 'GB', label: 'GB' },
  { value: 'GiB', label: 'GiB' },
]
const quotaPreview = computed(() => {
  const quota = settings.value?.trafficQuota
  if (!quota) return { providerStop: 0 }
  const amount = Number.isFinite(quota.amount) ? Math.max(0, quota.amount) : 0
  const headroom = Math.min(50, Math.max(0, quota.headroomPercent || 0))
  const providerStop = amount * (100 - headroom) / 100
  return { providerStop }
})
const quotaAmount = (value: number) => `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(value)} ${settings.value?.trafficQuota.unit || 'GB'}`
const passedChecks = computed(() => server.value?.health.checks.filter(check => check.status === 'ok').length ?? 0)
const dateTime = (value: string, timezone?: string) => new Intl.DateTimeFormat(dateLocale(), { dateStyle: 'medium', timeStyle: 'short', timeZone: timezone && timezone !== 'Local' ? timezone : undefined }).format(new Date(value))
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
  if (check.nextResetAt) return t('server.detail.nextReset', { time: dateTime(check.nextResetAt, check.timezone), timezone: check.timezone || 'Local' })
  return ''
}
const safe = guard(message => emit('toast', message))
const loadServer = async () => {
  try {
    const next = await api<ServerStatus>('/api/server', { signal: AbortSignal.timeout(10000) })
    if (!disposed) server.value = next
  } catch {}
}
const load = safe(async () => {
  settings.value = await api<Settings>('/api/settings')
  loadServer()
})
const saveTraffic = safe(async () => {
  if (!settings.value) return
  await put('/api/settings/traffic', {
    trafficQuota: settings.value.trafficQuota,
    vnstatInterface: settings.value.vnstatInterface,
    reset: settings.value.reset,
  })
  loadServer()
  emit('toast', t('settings.trafficSaved'))
})
const saveOutbound = safe(async () => {
  if (!settings.value) return
  await put('/api/settings/outbound', {
    outboundStrategy: settings.value.outboundStrategy,
  })
  loadServer()
  emit('toast', t('settings.outboundSaved'))
})
const token = safe(async () => { const result = await post<{ subscriptionURL: string }>('/api/settings/token'); if (settings.value) settings.value.subscriptionURL = result.subscriptionURL; emit('toast', t('settings.tokenDone')) })
const changePassword = safe(async () => { if (password.value.newPassword !== password.value.confirm) { emit('toast', t('settings.passwordMismatch')); return }; const size = new TextEncoder().encode(password.value.newPassword).length; if (size < 12 || size > 72) { emit('toast', t('settings.passwordLength')); return }; await post('/api/settings/password', { currentPassword: password.value.currentPassword, newPassword: password.value.newPassword }); emit('toast', t('settings.passwordDone')); emit('loggedOut') })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('copied')) })
onMounted(() => { load(); timer = window.setInterval(loadServer, 10000) })
onBeforeUnmount(() => { disposed = true; clearInterval(timer) })
</script>

<template>
  <div v-if="settings" class="page settings-page">
    <header class="page-head"><div><span class="eyebrow">SYSTEM PARAMETERS</span><h1>{{ t('settings.title') }}</h1><p>{{ t('settings.help') }}</p></div></header>
    <div class="settings-stack">
      <form class="settings-section traffic-settings" @submit.prevent="saveTraffic">
        <div class="section-intro"><span class="eyebrow">PROVIDER PLAN</span><h2>{{ t('settings.traffic') }}</h2><p>{{ t('settings.trafficHelp') }}</p></div>
        <div class="settings-fields">
          <label>{{ t('settings.quotaAmount') }}<input v-model.number="settings.trafficQuota.amount" type="number" min="0" step="0.01" required></label>
          <label>{{ t('settings.quotaUnit') }}<SelectControl v-model="settings.trafficQuota.unit" :options="unitOptions" /></label>
          <label class="span-two">{{ t('network.interface') }}<input v-model.trim="settings.vnstatInterface" maxlength="64" :placeholder="t('network.autoInterface')"><small>{{ t('network.autoInterface') }}</small></label>
          <label>{{ t('settings.billingMode') }}<SelectControl v-model="settings.trafficQuota.billingMode" :options="billingOptions" /></label>
          <label>{{ t('settings.headroom') }}<span class="input-suffix"><input v-model.number="settings.trafficQuota.headroomPercent" type="number" min="0" max="50" step="1" required><b>%</b></span></label>
          <aside class="quota-preview">
            <div><small>{{ t('settings.planAllowance') }}</small><strong>{{ quotaAmount(settings.trafficQuota.amount) }}</strong></div>
            <div><small>{{ t('settings.estimatedProviderStop') }}</small><strong>{{ quotaAmount(quotaPreview.providerStop) }}</strong></div>

          </aside>
          <label>{{ t('settings.autoReset') }}<SelectControl v-model="settings.reset.mode" :options="resetOptions" /></label>
          <label v-if="settings.reset.mode === 'monthly'">{{ t('settings.day') }}<input v-model.number="settings.reset.day" type="number" min="1" max="28"></label>
          <label v-if="settings.reset.mode === 'monthly'">{{ t('settings.timezone') }}<SelectControl v-model="settings.reset.timezone" :options="timezoneOptions" /></label>
          <div class="section-save-row"><button class="primary">{{ t('settings.saveTraffic') }}</button></div>
        </div>
      </form>
      <form class="settings-section" @submit.prevent="saveOutbound"><div class="section-intro"><h2>{{ t('settings.outbound') }}</h2><p>{{ t('settings.outboundHelp') }}</p></div><div class="settings-fields"><label class="span-two">{{ t('settings.outboundStrategy') }}<SelectControl v-model="settings.outboundStrategy" :options="outboundOptions" /></label><aside class="quota-guide"><strong>{{ t('settings.outboundGuideTitle') }}</strong><p>{{ t('settings.outboundGuidePrefer') }}</p><small>{{ t('settings.outboundGuideOnly') }}</small></aside><div class="section-save-row"><button class="primary">{{ t('settings.saveOutbound') }}</button></div></div></form>
    </div>
    <section class="settings-section"><div class="section-intro"><h2>{{ t('settings.subscription') }}</h2><p>{{ t('settings.subscriptionHelp') }}</p></div><div class="settings-fields full-fields"><div class="copy-field"><code>{{ settings.subscriptionURL }}</code><button @click="copy(settings.subscriptionURL)"><Icon name="copy"/>{{ t('settings.copy') }}</button></div><ConfirmAction :title="t('settings.regenerate')" :message="t('settings.tokenConfirm')" destructive @confirm="token"><button class="secondary danger-outline">{{ t('settings.regenerate') }}</button></ConfirmAction></div></section>
    <form class="settings-section" @submit.prevent="changePassword"><input type="text" name="username" value="admin" autocomplete="username" hidden readonly><div class="section-intro"><h2>{{ t('settings.password') }}</h2><p>{{ t('settings.passwordHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.currentPassword') }}<PasswordInput v-model="password.currentPassword" :aria-label="t('settings.currentPassword')" autocomplete="current-password" required /></label><label>{{ t('settings.newPassword') }}<PasswordInput v-model="password.newPassword" :aria-label="t('settings.newPassword')" autocomplete="new-password" maxlength="72" required /></label><label>{{ t('settings.confirmPassword') }}<PasswordInput v-model="password.confirm" :aria-label="t('settings.confirmPassword')" autocomplete="new-password" maxlength="72" required /></label><button class="secondary self-end">{{ t('settings.changePassword') }}</button></div></form>
    <section v-if="server" class="doctor-panel" :aria-label="t('server.doctor')">
      <header class="doctor-head">
        <div><span class="eyebrow">SERVICE DIAGNOSTICS</span><h2>{{ t('server.doctor') }}</h2><p>{{ t('server.somePassed', { passed: passedChecks, total: server.health.checks.length }) }}</p></div>
        <span class="health-chip" :data-status="server.health.overall">{{ overallStatusLabel(server.health.overall) }}</span>
      </header>
      <div class="health-grid">
        <article v-for="check in server.health.checks" :key="check.id" class="health-check" :data-status="check.status">
          <div><span class="health-dot"></span><strong>{{ checkTitle(check) }}</strong><small>{{ statusLabel(check.status) }}</small></div>
          <p>{{ checkReason(check) }}</p>
          <code v-if="checkDetail(check)">{{ checkDetail(check) }}</code>
        </article>
      </div>
    </section>
    <section class="machine-info"><span>HOST</span><strong>{{ settings.domain }}</strong><span>PANEL</span><strong>TCP/{{ settings.panelPort }}</strong><span>SYSTEM</span><strong>{{ server ? [server.os, server.architecture].filter(Boolean).join(' · ') || 'LOCAL' : 'LOCAL' }}</strong></section>
  </div>
  <div v-else class="loading">{{ t('settings.loading') }}</div>
</template>
