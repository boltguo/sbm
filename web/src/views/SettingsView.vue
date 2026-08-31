<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, guard, post, put } from '../api'
import type { Settings } from '../types'
import Icon from '../components/Icon.vue'
import SelectControl from '../components/SelectControl.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import PasswordInput from '../components/PasswordInput.vue'
import { dateLocale, t } from '../i18n'

const emit = defineEmits<{ toast: [message: string]; loggedOut: [] }>()
const settings = ref<Settings | null>(null)
const password = ref({ currentPassword: '', newPassword: '', confirm: '' })
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
  if (!quota) return { providerStop: 0, proxyStop: 0 }
  const amount = Number.isFinite(quota.amount) ? Math.max(0, quota.amount) : 0
  const headroom = Math.min(50, Math.max(0, quota.headroomPercent || 0))
  const providerStop = amount * (100 - headroom) / 100
  const proxyStop = providerStop / (quota.billingMode === 'bidirectional' ? 2 : 1)
  return { providerStop, proxyStop }
})
const quotaAmount = (value: number) => `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(value)} ${settings.value?.trafficQuota.unit || 'GB'}`
const safe = guard(message => emit('toast', message))
const load = safe(async () => { settings.value = await api<Settings>('/api/settings') })
const saveTraffic = safe(async () => {
  if (!settings.value) return
  await put('/api/settings/traffic', {
    trafficQuota: settings.value.trafficQuota,
    reset: settings.value.reset,
  })
  emit('toast', t('settings.trafficSaved'))
})
const saveOutbound = safe(async () => {
  if (!settings.value) return
  await put('/api/settings/outbound', {
    outboundStrategy: settings.value.outboundStrategy,
  })
  emit('toast', t('settings.outboundSaved'))
})
const token = safe(async () => { const result = await post<{ subscriptionURL: string }>('/api/settings/token'); if (settings.value) settings.value.subscriptionURL = result.subscriptionURL; emit('toast', t('settings.tokenDone')) })
const changePassword = safe(async () => { if (password.value.newPassword !== password.value.confirm) { emit('toast', t('settings.passwordMismatch')); return }; await post('/api/settings/password', { currentPassword: password.value.currentPassword, newPassword: password.value.newPassword }); emit('toast', t('settings.passwordDone')); emit('loggedOut') })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('copied')) })
onMounted(load)
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
          <label>{{ t('settings.billingMode') }}<SelectControl v-model="settings.trafficQuota.billingMode" :options="billingOptions" /></label>
          <label>{{ t('settings.headroom') }}<span class="input-suffix"><input v-model.number="settings.trafficQuota.headroomPercent" type="number" min="0" max="50" step="1" required><b>%</b></span></label>
          <aside class="quota-preview">
            <div><small>{{ t('settings.planAllowance') }}</small><strong>{{ quotaAmount(settings.trafficQuota.amount) }}</strong></div>
            <div><small>{{ t('settings.estimatedProviderStop') }}</small><strong>{{ quotaAmount(quotaPreview.providerStop) }}</strong></div>
            <div><small>{{ t('settings.proxyStop') }}</small><strong>{{ settings.trafficQuota.amount ? quotaAmount(quotaPreview.proxyStop) : t('settings.unlimited') }}</strong></div>
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
    <form class="settings-section" @submit.prevent="changePassword"><input type="text" name="username" value="admin" autocomplete="username" hidden readonly><div class="section-intro"><h2>{{ t('settings.password') }}</h2><p>{{ t('settings.passwordHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.currentPassword') }}<PasswordInput v-model="password.currentPassword" :aria-label="t('settings.currentPassword')" autocomplete="current-password" required /></label><label>{{ t('settings.newPassword') }}<PasswordInput v-model="password.newPassword" :aria-label="t('settings.newPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><label>{{ t('settings.confirmPassword') }}<PasswordInput v-model="password.confirm" :aria-label="t('settings.confirmPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><button class="secondary self-end">{{ t('settings.changePassword') }}</button></div></form>
    <section class="machine-info"><span>HOST</span><strong>{{ settings.domain }}</strong><span>PANEL</span><strong>TCP/{{ settings.panelPort }}</strong><span>DATA</span><strong>JSON / LOCAL</strong></section>
  </div>
  <div v-else class="loading">{{ t('settings.loading') }}</div>
</template>
