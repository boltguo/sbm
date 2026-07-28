<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { api, guard, post, put } from '../api'
import type { Settings } from '../types'
import Icon from '../components/Icon.vue'
import SelectControl from '../components/SelectControl.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import PasswordInput from '../components/PasswordInput.vue'
import { t } from '../i18n'

const emit = defineEmits<{ toast: [message: string]; loggedOut: [] }>()
const settings = ref<Settings | null>(null)
const quotaGB = ref(0)
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
const safe = guard(message => emit('toast', message))
const load = safe(async () => { settings.value = await api<Settings>('/api/settings'); quotaGB.value = Math.round(settings.value.totalBytes / 1024 ** 3 * 100) / 100 })
const saveTraffic = safe(async () => {
  if (!settings.value) return
  settings.value.totalBytes = Math.round(quotaGB.value * 1024 ** 3)
  await put('/api/settings/traffic', {
    totalBytes: settings.value.totalBytes,
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
const saveWireGuard = safe(async () => {
  if (!settings.value) return
  const exit = settings.value.wireGuardExit
  const expected = {
    ...exit,
    label: exit.label.trim(),
    server: exit.server.trim(),
    privateKey: exit.privateKey.trim(),
    peerPublicKey: exit.peerPublicKey.trim(),
  }
  let result: { outboundStrategy: Settings['outboundStrategy']; wireGuardLocalPublicKey: string }
  try {
    result = await put('/api/settings/wireguard', { wireGuardExit: expected })
  } catch (error) {
    if ((error as Error & { status?: number }).status !== undefined) throw error
    const recovered = await waitForWireGuardSettings(expected)
    if (!recovered) throw error
    emit('toast', t('settings.wireGuardSaved'))
    return
  }
  settings.value.outboundStrategy = result.outboundStrategy
  settings.value.wireGuardLocalPublicKey = result.wireGuardLocalPublicKey
  emit('toast', t('settings.wireGuardSaved'))
})
async function waitForWireGuardSettings(expected: Settings['wireGuardExit']): Promise<boolean> {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await new Promise(resolve => window.setTimeout(resolve, 500))
    try {
      const current = await api<Settings>('/api/settings')
      const actual = current.wireGuardExit
      if (
        actual.enabled === expected.enabled &&
        actual.label === expected.label &&
        actual.server === expected.server &&
        actual.serverPort === expected.serverPort &&
        actual.privateKey === expected.privateKey &&
        actual.peerPublicKey === expected.peerPublicKey
      ) {
        settings.value = current
        quotaGB.value = Math.round(current.totalBytes / 1024 ** 3 * 100) / 100
        return true
      }
    } catch {
      // The proxy is restarting; retry until the direct node reconnects.
    }
  }
  return false
}
const generateWireGuardKeypair = safe(async () => {
  if (!settings.value) return
  const keys = await post<{ privateKey: string; publicKey: string }>('/api/settings/wireguard/keypair')
  settings.value.wireGuardExit.privateKey = keys.privateKey
  settings.value.wireGuardLocalPublicKey = keys.publicKey
  emit('toast', t('settings.wireGuardKeysGenerated'))
})
function setWireGuardEnabled(value: boolean) {
  if (!settings.value) return
  settings.value.wireGuardExit.enabled = value
}
const token = safe(async () => { const result = await post<{ subscriptionURL: string }>('/api/settings/token'); if (settings.value) settings.value.subscriptionURL = result.subscriptionURL; emit('toast', t('settings.tokenDone')) })
const changePassword = safe(async () => { if (password.value.newPassword !== password.value.confirm) { emit('toast', t('settings.passwordMismatch')); return }; await post('/api/settings/password', { currentPassword: password.value.currentPassword, newPassword: password.value.newPassword }); emit('toast', t('settings.passwordDone')); emit('loggedOut') })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('copied')) })
onMounted(load)
</script>

<template>
  <div v-if="settings" class="page settings-page">
    <header class="page-head"><div><span class="eyebrow">SYSTEM PARAMETERS</span><h1>{{ t('settings.title') }}</h1><p>{{ t('settings.help') }}</p></div></header>
    <div class="settings-stack">
      <form class="settings-section" @submit.prevent="saveTraffic"><div class="section-intro"><h2>{{ t('settings.traffic') }}</h2><p>{{ t('settings.trafficHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.total') }}<input v-model.number="quotaGB" type="number" min="0" step="0.1"></label><label>{{ t('settings.autoReset') }}<SelectControl v-model="settings.reset.mode" :options="resetOptions" /></label><label v-if="settings.reset.mode === 'monthly'">{{ t('settings.day') }}<input v-model.number="settings.reset.day" type="number" min="1" max="28"></label><label v-if="settings.reset.mode === 'monthly'">{{ t('settings.timezone') }}<SelectControl v-model="settings.reset.timezone" :options="timezoneOptions" /></label><aside class="quota-guide"><strong>{{ t('settings.quotaGuideTitle') }}</strong><p>{{ t('settings.quotaGuideBidirectional') }}</p><p>{{ t('settings.quotaGuideEgress') }}</p><small>{{ t('settings.quotaGuideNote') }}</small></aside><div class="section-save-row"><button class="primary">{{ t('settings.saveTraffic') }}</button></div></div></form>
      <form class="settings-section" @submit.prevent="saveOutbound"><div class="section-intro"><h2>{{ t('settings.outbound') }}</h2><p>{{ t('settings.outboundHelp') }}</p></div><div class="settings-fields"><label class="span-two">{{ t('settings.outboundStrategy') }}<SelectControl v-model="settings.outboundStrategy" :options="outboundOptions" /></label><aside class="quota-guide"><strong>{{ t('settings.outboundGuideTitle') }}</strong><p>{{ t('settings.outboundGuidePrefer') }}</p><small>{{ t('settings.outboundGuideOnly') }}</small></aside><div class="section-save-row"><button class="primary">{{ t('settings.saveOutbound') }}</button></div></div></form>
      <form class="settings-section wireguard-section" :class="{ active: settings.wireGuardExit.enabled }" @submit.prevent="saveWireGuard">
        <div class="section-intro"><span class="eyebrow">OPTIONAL EGRESS</span><h2>{{ t('settings.wireGuard') }}</h2><p>{{ t('settings.wireGuardHelp') }}</p></div>
        <div class="settings-fields wireguard-fields">
          <div class="wireguard-switch-row span-two">
            <div><strong>{{ settings.wireGuardExit.enabled ? t('settings.wireGuardOn') : t('settings.wireGuardOff') }}</strong><small>{{ t('settings.wireGuardSwitchHelp') }}</small></div>
            <SwitchRoot :model-value="settings.wireGuardExit.enabled" class="relative h-7 w-12 rounded-full border border-[var(--ink)] bg-transparent p-[3px] data-[state=checked]:bg-[var(--signal)]" :aria-label="t('settings.wireGuardToggle')" @update:model-value="setWireGuardEnabled"><SwitchThumb class="block size-5 rounded-full bg-[var(--muted)] transition-transform data-[state=checked]:translate-x-[20px] data-[state=checked]:bg-[var(--ink)]" /></SwitchRoot>
          </div>
          <label>{{ t('settings.wireGuardLabel') }}<input v-model.trim="settings.wireGuardExit.label" maxlength="40" placeholder="GCP" :required="settings.wireGuardExit.enabled"></label>
          <label>{{ t('settings.wireGuardServer') }}<input v-model.trim="settings.wireGuardExit.server" inputmode="decimal" placeholder="203.0.113.10" :required="settings.wireGuardExit.enabled"></label>
          <label>{{ t('settings.wireGuardPort') }}<input v-model.number="settings.wireGuardExit.serverPort" type="number" min="1" max="65535" :required="settings.wireGuardExit.enabled"></label>
          <div class="wireguard-key-field span-two">
            <label for="wireguard-private-key">{{ t('settings.wireGuardPrivateKey') }}</label>
            <div class="wireguard-key-control"><PasswordInput id="wireguard-private-key" v-model="settings.wireGuardExit.privateKey" :aria-label="t('settings.wireGuardPrivateKey')" autocomplete="off" :required="settings.wireGuardExit.enabled" /><button type="button" class="secondary" @click="generateWireGuardKeypair">{{ t('settings.wireGuardGenerate') }}</button></div>
          </div>
          <div class="wireguard-key-field span-two">
            <label>{{ t('settings.wireGuardLocalPublicKey') }}</label>
            <div class="copy-field wireguard-public-key"><code>{{ settings.wireGuardLocalPublicKey || t('settings.wireGuardGenerateFirst') }}</code><button type="button" :disabled="!settings.wireGuardLocalPublicKey" @click="copy(settings.wireGuardLocalPublicKey)"><Icon name="copy"/>{{ t('settings.copy') }}</button></div>
          </div>
          <div class="wireguard-key-field span-two"><label for="wireguard-peer-public-key">{{ t('settings.wireGuardPeerPublicKey') }}</label><PasswordInput id="wireguard-peer-public-key" v-model="settings.wireGuardExit.peerPublicKey" :aria-label="t('settings.wireGuardPeerPublicKey')" autocomplete="off" :required="settings.wireGuardExit.enabled" /></div>
          <div class="section-save-row"><button class="primary">{{ t('settings.saveWireGuard') }}</button></div>
        </div>
      </form>
    </div>
    <section class="settings-section"><div class="section-intro"><h2>{{ t('settings.subscription') }}</h2><p>{{ t('settings.subscriptionHelp') }}</p></div><div class="settings-fields full-fields"><div class="copy-field"><code>{{ settings.subscriptionURL }}</code><button @click="copy(settings.subscriptionURL)"><Icon name="copy"/>{{ t('settings.copy') }}</button></div><ConfirmAction :title="t('settings.regenerate')" :message="t('settings.tokenConfirm')" destructive @confirm="token"><button class="secondary danger-outline">{{ t('settings.regenerate') }}</button></ConfirmAction></div></section>
    <form class="settings-section" @submit.prevent="changePassword"><input type="text" name="username" value="admin" autocomplete="username" hidden readonly><div class="section-intro"><h2>{{ t('settings.password') }}</h2><p>{{ t('settings.passwordHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.currentPassword') }}<PasswordInput v-model="password.currentPassword" :aria-label="t('settings.currentPassword')" autocomplete="current-password" required /></label><label>{{ t('settings.newPassword') }}<PasswordInput v-model="password.newPassword" :aria-label="t('settings.newPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><label>{{ t('settings.confirmPassword') }}<PasswordInput v-model="password.confirm" :aria-label="t('settings.confirmPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><button class="secondary self-end">{{ t('settings.changePassword') }}</button></div></form>
    <section class="machine-info"><span>HOST</span><strong>{{ settings.domain }}</strong><span>PANEL</span><strong>TCP/{{ settings.panelPort }}</strong><span>DATA</span><strong>JSON / LOCAL</strong></section>
  </div>
  <div v-else class="loading">{{ t('settings.loading') }}</div>
</template>
