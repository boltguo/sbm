<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, errorMessage, post } from '../api'
import type { PanelUpdateJob, UpdateStatus } from '../types'
import { t } from '../i18n'
import Icon from './Icon.vue'
import Modal from './Modal.vue'

const emit = defineEmits<{ toast: [message: string] }>()
const release = ref<UpdateStatus | null>(null)
const job = ref<PanelUpdateJob | null>(null)
const open = ref(false)
const checking = ref(false)
const starting = ref(false)
const reconnecting = ref(false)
let timer = 0
let disposed = false
let polling = false
const running = computed(() => starting.value || job.value?.state === 'running')
const label = computed(() => running.value ? t('update.running') : release.value?.updateAvailable ? t('dashboard.updateFound', { version: release.value.latestVersion }) : t('dashboard.checkUpdate'))
const progress = computed(() => reconnecting.value ? t('update.reconnecting') : t(`update.phase.${job.value?.phase || 'queued'}`))

async function check(notify = true) {
  if (running.value) { open.value = true; return }
  checking.value = true
  try {
    release.value = await api<UpdateStatus>('/api/update')
    if (notify) {
      if (release.value.updateAvailable) open.value = true
      else emit('toast', t('dashboard.updateCurrent'))
    }
  } catch {
    if (notify) emit('toast', t('dashboard.updateFailed'))
  } finally { checking.value = false }
}

function watchJob() {
  if (disposed) return
  if (!timer) timer = window.setInterval(() => { void readProgress(true) }, 2000)
}

async function readProgress(watching = false) {
  if (polling) return
  polling = true
  try {
    const next = await api<PanelUpdateJob>('/api/update/status', { signal: AbortSignal.timeout(5000) })
    if (disposed) return
    reconnecting.value = false
    if (next.state === 'running') {
      if (job.value?.state !== 'running') open.value = true
      job.value = next
      watchJob()
    } else if (watching || next.state === 'failed') {
      job.value = next
      clearInterval(timer); timer = 0
      if (next.state === 'succeeded') window.location.reload()
    }
  } catch {
    // The panel's HTTPS listener briefly goes away during its restart. Keep
    // the progress view alive; a failed request is not an update failure.
    if (watching) reconnecting.value = true
  } finally { polling = false }
}

async function install() {
  if (running.value) return
  starting.value = true
  try {
    job.value = await post<PanelUpdateJob>('/api/update')
    watchJob()
  } catch (error) {
    // If the launch response was lost, recover the persisted job rather than
    // blindly submitting another install request.
    await readProgress()
    if (job.value?.state !== 'running') emit('toast', errorMessage(error))
  } finally { starting.value = false }
}

onMounted(() => { void check(false); void readProgress() })
onBeforeUnmount(() => { disposed = true; clearInterval(timer) })
</script>

<template>
  <button class="version-check" :class="{ checking: checking || running }" :disabled="checking" :title="label" :aria-label="label" @click="check()"><Icon name="refresh"/><span v-if="release?.updateAvailable" class="version-update-dot"></span></button>
  <Modal v-if="open" :title="t('update.title')" @close="open = false">
    <div class="panel-update-content" aria-live="polite">
      <template v-if="running">
        <strong>{{ job?.targetVersion || release?.latestVersion }}</strong>
        <p>{{ progress }}</p>
        <small>{{ t('update.waitHelp') }}</small>
      </template>
      <template v-else-if="job?.state === 'failed'">
        <strong>{{ t('update.failed') }}</strong>
        <p>{{ job.rolledBack ? t('update.rolledBack') : t('update.failedHelp') }}</p>
        <button class="primary" @click="job = null; check()">{{ t('update.retry') }}</button>
      </template>
      <template v-else>
        <strong>{{ t('dashboard.updateFound', { version: release?.latestVersion || '' }) }}</strong>
        <p>{{ t('update.installHelp') }}</p>
        <button class="primary" :disabled="!release?.canInstall" @click="install()"><Icon name="refresh"/>{{ t('update.install') }}</button>
        <small v-if="!release?.canInstall">{{ t('update.unavailable') }}</small>
      </template>
    </div>
  </Modal>
</template>
