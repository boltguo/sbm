<script setup lang="ts">
import { ref } from 'vue'
import Icon from './Icon.vue'
import { t } from '../i18n'

defineOptions({ inheritAttrs: false })
defineProps<{ modelValue?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const visible = ref(false)

function update(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <span class="password-input">
    <input v-bind="$attrs" :value="modelValue" :type="visible ? 'text' : 'password'" @input="update">
    <button
      type="button"
      class="password-toggle"
      :title="visible ? t('password.hide') : t('password.show')"
      :aria-label="visible ? t('password.hide') : t('password.show')"
      :aria-pressed="visible"
      @mousedown.prevent
      @click.stop="visible = !visible"
    >
      <Icon :name="visible ? 'eye-off' : 'eye'" />
    </button>
  </span>
</template>
