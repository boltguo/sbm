<script setup lang="ts">
import { ref } from 'vue'
import { errorMessage, post } from '../api'
import Modal from './Modal.vue'
import { dateLocale, t } from '../i18n'

const props = defineProps<{ name: string; endpoint: string; unit: string; usedBytes: number; calibrated: boolean }>()
const emit = defineEmits<{ close: []; done: [message: string] }>()
const unitBytes = props.unit === 'GiB' ? 1024 ** 3 : 1000 ** 3
const amount = ref(Math.round(props.usedBytes / unitBytes * 100) / 100)
const busy = ref(false)
const error = ref('')
const measured = () => `${new Intl.NumberFormat(dateLocale(), { maximumFractionDigits: 2 }).format(props.usedBytes / unitBytes)} ${props.unit}`

async function submit(clear: boolean) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await post(props.endpoint, clear ? { clear: true } : { usedBytes: Math.round(Math.max(0, amount.value || 0) * unitBytes) })
    emit('done', t(clear ? 'network.calibrateCleared' : 'network.calibrateDone'))
  } catch (reason) {
    error.value = errorMessage(reason)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :title="t('network.calibrateTitle')" :description="t('network.calibrateHelp')" @close="emit('close')">
    <form class="form-grid" @submit.prevent="submit(false)">
      <div class="alert subtle">{{ t('network.calibrateHelp') }}</div>
      <p>{{ t('network.calibrateMeasured', { name, amount: measured() }) }}</p>
      <label>{{ t('network.calibrateUsed') }}<span class="input-suffix"><input v-model.number="amount" type="number" min="0" step="0.01" required><b>{{ unit }}</b></span></label>
      <p v-if="error" class="traffic-source warning" role="alert">{{ error }}</p>
      <div class="modal-actions">
        <button v-if="calibrated" type="button" class="secondary" :disabled="busy" @click="submit(true)">{{ t('network.calibrateClear') }}</button>
        <button type="button" class="secondary" :disabled="busy" @click="emit('close')">{{ t('protocol.cancel') }}</button>
        <button class="primary" :disabled="busy">{{ t('network.calibrateSave') }}</button>
      </div>
    </form>
  </Modal>
</template>
