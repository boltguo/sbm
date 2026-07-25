<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
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
const timezoneOptions = timezones.map(value => ({ value, label: value }))
const safe = guard(message => emit('toast', message))
const load = safe(async () => { settings.value = await api<Settings>('/api/settings'); quotaGB.value = Math.round(settings.value.totalBytes / 1024 ** 3 * 100) / 100 })
const save = safe(async () => { if (!settings.value) return; settings.value.totalBytes = Math.round(quotaGB.value * 1024 ** 3); await put('/api/settings', { totalBytes: settings.value.totalBytes, reset: settings.value.reset }); emit('toast', t('settings.saved')) })
const token = safe(async () => { const result = await post<{ subscriptionURL: string }>('/api/settings/token'); if (settings.value) settings.value.subscriptionURL = result.subscriptionURL; emit('toast', t('settings.tokenDone')) })
const changePassword = safe(async () => { if (password.value.newPassword !== password.value.confirm) { emit('toast', t('settings.passwordMismatch')); return }; await post('/api/settings/password', { currentPassword: password.value.currentPassword, newPassword: password.value.newPassword }); emit('toast', t('settings.passwordDone')); emit('loggedOut') })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('copied')) })
onMounted(load)
</script>

<template>
  <div v-if="settings" class="page settings-page">
    <header class="page-head"><div><span class="eyebrow">SYSTEM PARAMETERS</span><h1>{{ t('settings.title') }}</h1><p>{{ t('settings.help') }}</p></div></header>
    <form class="settings-stack" @submit.prevent="save">
      <section class="settings-section"><div class="section-intro"><h2>{{ t('settings.traffic') }}</h2><p>{{ t('settings.trafficHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.total') }}<input v-model.number="quotaGB" type="number" min="0" step="0.1"></label><label>{{ t('settings.autoReset') }}<SelectControl v-model="settings.reset.mode" :options="resetOptions" /></label><label v-if="settings.reset.mode === 'monthly'">{{ t('settings.day') }}<input v-model.number="settings.reset.day" type="number" min="1" max="28"></label><label v-if="settings.reset.mode === 'monthly'">{{ t('settings.timezone') }}<SelectControl v-model="settings.reset.timezone" :options="timezoneOptions" /></label><aside class="quota-guide"><strong>{{ t('settings.quotaGuideTitle') }}</strong><p>{{ t('settings.quotaGuideBidirectional') }}</p><p>{{ t('settings.quotaGuideEgress') }}</p><small>{{ t('settings.quotaGuideNote') }}</small></aside></div></section>
      <div class="save-row"><button class="primary">{{ t('settings.save') }}</button></div>
    </form>
    <section class="settings-section"><div class="section-intro"><h2>{{ t('settings.subscription') }}</h2><p>{{ t('settings.subscriptionHelp') }}</p></div><div class="settings-fields full-fields"><div class="copy-field"><code>{{ settings.subscriptionURL }}</code><button @click="copy(settings.subscriptionURL)"><Icon name="copy"/>{{ t('settings.copy') }}</button></div><ConfirmAction :title="t('settings.regenerate')" :message="t('settings.tokenConfirm')" destructive @confirm="token"><button class="secondary danger-outline">{{ t('settings.regenerate') }}</button></ConfirmAction></div></section>
    <form class="settings-section" @submit.prevent="changePassword"><input type="text" name="username" value="admin" autocomplete="username" hidden readonly><div class="section-intro"><h2>{{ t('settings.password') }}</h2><p>{{ t('settings.passwordHelp') }}</p></div><div class="settings-fields"><label>{{ t('settings.currentPassword') }}<PasswordInput v-model="password.currentPassword" :aria-label="t('settings.currentPassword')" autocomplete="current-password" required /></label><label>{{ t('settings.newPassword') }}<PasswordInput v-model="password.newPassword" :aria-label="t('settings.newPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><label>{{ t('settings.confirmPassword') }}<PasswordInput v-model="password.confirm" :aria-label="t('settings.confirmPassword')" autocomplete="new-password" minlength="12" maxlength="128" required /></label><button class="secondary self-end">{{ t('settings.changePassword') }}</button></div></form>
    <section class="machine-info"><span>HOST</span><strong>{{ settings.domain }}</strong><span>PANEL</span><strong>TCP/{{ settings.panelPort }}</strong><span>DATA</span><strong>JSON / LOCAL</strong></section>
  </div>
  <div v-else class="loading">{{ t('settings.loading') }}</div>
</template>
