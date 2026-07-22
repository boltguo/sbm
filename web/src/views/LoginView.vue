<script setup lang="ts">
import { ref } from 'vue'
import { post, setCSRF } from '../api'
import { t, toggleLocale } from '../i18n'

const emit = defineEmits<{ loggedIn: [username: string] }>()
const username = ref('admin')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function login() {
  loading.value = true; error.value = ''
  try {
    const result = await post<{ username: string; csrfToken: string }>('/api/login', { username: username.value, password: password.value })
    setCSRF(result.csrfToken); emit('loggedIn', result.username)
  } catch (e) { error.value = (e as Error).message }
  finally { loading.value = false }
}
</script>

<template>
  <main class="login-page">
    <button class="locale-switch login-locale" @click="toggleLocale">{{ t('language') }}</button>
    <div class="login-grid" aria-hidden="true"></div>
    <form class="login-card" @submit.prevent="login">
      <div class="brand-mark large login-card-brand"><span>SB</span><b>M</b></div>
      <h2>{{ t('login.title') }}</h2>
      <p>{{ t('login.help') }}</p>
      <label>{{ t('login.username') }}<input v-model="username" autocomplete="username" required></label>
      <label>{{ t('login.password') }}<input v-model="password" type="password" autocomplete="current-password" autofocus required></label>
      <p v-if="error" class="form-error">{{ error }}</p>
      <button class="primary full" :disabled="loading">{{ loading ? t('login.loading') : t('login.submit') }}<span>↗</span></button>
    </form>
  </main>
</template>
