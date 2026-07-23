<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { ToastProvider, ToastRoot, ToastTitle, ToastViewport } from 'reka-ui'
import { api, post, setCSRF } from './api'
import Icon from './components/Icon.vue'
import LoginView from './views/LoginView.vue'
import DashboardView from './views/DashboardView.vue'
import ProtocolsView from './views/ProtocolsView.vue'
import ServerView from './views/ServerView.vue'
import SettingsView from './views/SettingsView.vue'
import { t, toggleLocale } from './i18n'

type Page = 'dashboard' | 'protocols' | 'server' | 'settings'
const ready = ref(false)
const username = ref('')
const page = ref<Page>('dashboard')
const toast = ref('')
const toastOpen = ref(false)

async function showToast(message: string) { toastOpen.value = false; toast.value = message; await nextTick(); toastOpen.value = true }
async function check() { try { const me = await api<{ username: string; csrfToken: string }>('/api/me'); username.value = me.username; setCSRF(me.csrfToken) } catch { username.value = '' } finally { ready.value = true } }
async function logout() { try { await post('/api/logout') } catch {} username.value = ''; page.value = 'dashboard' }
async function navigate(target: Page) { page.value = target; await nextTick(); window.scrollTo({ top: 0, behavior: 'auto' }) }
onMounted(check)
</script>

<template>
  <div v-if="!ready" class="loading-screen"><div class="brand-mark"><span>SB</span><b>M</b></div></div>
  <LoginView v-else-if="!username" @logged-in="name => username = name" />
  <div v-else class="shell">
    <ToastProvider :duration="2800" swipe-direction="right">
    <header class="app-header">
      <div class="app-header-brand" aria-hidden="true"><div class="brand-mark"><span>SB</span><b>M</b></div></div>
      <div class="app-header-actions">
        <button class="app-header-control locale-control" @click="toggleLocale">{{ t('language') }}</button>
        <a class="app-header-control github-control" href="https://github.com/boltguo/sbm" target="_blank" rel="noopener noreferrer" title="GitHub" aria-label="GitHub"><Icon name="github"/></a>
        <button class="app-header-control logout-control" :title="t('logout')" :aria-label="t('logout')" @click="logout"><Icon name="logout"/><span>{{ t('logout') }}</span></button>
      </div>
    </header>
    <aside class="sidebar">
      <div class="brand-mark"><span>SB</span><b>M</b></div>
      <nav><button :class="{ active: page === 'dashboard' }" :aria-current="page === 'dashboard' ? 'page' : undefined" @click="navigate('dashboard')"><Icon name="grid"/><span>{{ t('overview') }}</span></button><button :class="{ active: page === 'protocols' }" :aria-current="page === 'protocols' ? 'page' : undefined" @click="navigate('protocols')"><Icon name="route"/><span>{{ t('protocols') }}</span></button><button :class="{ active: page === 'server' }" :aria-current="page === 'server' ? 'page' : undefined" @click="navigate('server')"><Icon name="server"/><span>{{ t('server') }}</span></button><button :class="{ active: page === 'settings' }" :aria-current="page === 'settings' ? 'page' : undefined" @click="navigate('settings')"><Icon name="sliders"/><span>{{ t('settings') }}</span></button></nav>
    </aside>
    <main class="content"><DashboardView v-if="page === 'dashboard'" @toast="showToast"/><ProtocolsView v-else-if="page === 'protocols'" @toast="showToast"/><ServerView v-else-if="page === 'server'"/><SettingsView v-else @toast="showToast" @logged-out="logout"/></main>
    <nav class="mobile-nav"><button :class="{ active: page === 'dashboard' }" :aria-current="page === 'dashboard' ? 'page' : undefined" @click="navigate('dashboard')"><Icon name="grid"/><span>{{ t('overview') }}</span></button><button :class="{ active: page === 'protocols' }" :aria-current="page === 'protocols' ? 'page' : undefined" @click="navigate('protocols')"><Icon name="route"/><span>{{ t('protocols') }}</span></button><button :class="{ active: page === 'server' }" :aria-current="page === 'server' ? 'page' : undefined" @click="navigate('server')"><Icon name="server"/><span>{{ t('server') }}</span></button><button :class="{ active: page === 'settings' }" :aria-current="page === 'settings' ? 'page' : undefined" @click="navigate('settings')"><Icon name="sliders"/><span>{{ t('settings') }}</span></button></nav>
      <ToastRoot v-model:open="toastOpen" class="toast data-[state=open]:animate-[toast-in_.18s_ease-out] data-[state=closed]:animate-[toast-out_.15s_ease-in]"><Icon name="check"/><ToastTitle>{{ toast }}</ToastTitle></ToastRoot>
      <ToastViewport class="fixed bottom-7 right-7 z-[80] m-0 flex w-[min(390px,calc(100vw-32px))] list-none flex-col gap-2 outline-none max-sm:bottom-20 max-sm:right-4" />
    </ToastProvider>
  </div>
</template>
