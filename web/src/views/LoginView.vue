<script setup lang="ts">
import { ref } from 'vue'
import { post, setCSRF } from '../api'
import { t, toggleLocale } from '../i18n'
import Icon from '../components/Icon.vue'
import PasswordInput from '../components/PasswordInput.vue'

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
    <div class="login-actions">
      <button class="app-header-control locale-control" @click="toggleLocale">{{ t('language') }}</button>
      <a class="app-header-control github-control" href="https://github.com/boltguo/sbm" target="_blank" rel="noopener noreferrer" title="GitHub" aria-label="GitHub"><Icon name="github"/></a>
    </div>
    <div class="login-grid" aria-hidden="true"></div>
    <form class="login-card" @submit.prevent="login">
      <div class="brand-mark large login-card-brand"><span>SB</span><b>M</b></div>
      <h2>{{ t('login.title') }}</h2>
      <p>{{ t('login.help') }}</p>
      <label>{{ t('login.username') }}<input v-model="username" autocomplete="username" required></label>
      <label>{{ t('login.password') }}<PasswordInput v-model="password" :aria-label="t('login.password')" autocomplete="current-password" autofocus required /></label>
      <p v-if="error" class="form-error">{{ error }}</p>
      <button class="primary full" :disabled="loading">{{ loading ? t('login.loading') : t('login.submit') }}<span>↗</span></button>
    </form>
  </main>
</template>
