<script setup lang="ts">
import { computed } from 'vue'
import {
  SelectContent,
  SelectIcon,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectViewport,
} from 'reka-ui'
import { Check, ChevronDown } from '@lucide/vue'

const EMPTY_TOKEN = '__sbm_empty__'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    options: Array<{ value: string; label: string }>
    disabled?: boolean
    size?: 'default' | 'sm' | 'xs'
    active?: boolean
    ariaLabel?: string
  }>(),
  {
    modelValue: '',
    disabled: false,
    size: 'default',
    active: false,
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const encode = (value?: string) => (value === '' || value === undefined ? EMPTY_TOKEN : value)
const decode = (value: unknown) => {
  const str = String(value ?? '')
  return str === EMPTY_TOKEN ? '' : str
}

const normalizedOptions = computed(() =>
  props.options.map(option => ({
    raw: option.value,
    value: encode(option.value),
    label: option.label,
  })),
)

const selectedLabel = computed(
  () => props.options.find(option => option.value === (props.modelValue ?? ''))?.label || '',
)

function update(value: unknown) {
  emit('update:modelValue', decode(value))
}
</script>

<template>
  <SelectRoot :model-value="encode(modelValue)" :disabled="disabled" @update:model-value="update">
    <SelectTrigger
      class="sbm-select-trigger"
      :class="[`size-${size}`, { 'is-active': active }]"
      :aria-label="ariaLabel"
    >
      <SelectValue class="sbm-select-value">{{ selectedLabel }}</SelectValue>
      <SelectIcon class="sbm-select-icon" aria-hidden="true">
        <ChevronDown class="icon" :stroke-width="1.8" />
      </SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent position="popper" :side-offset="4" class="sbm-select-content">
        <SelectViewport class="sbm-select-viewport">
          <SelectItem
            v-for="option in normalizedOptions"
            :key="option.value"
            :value="option.value"
            class="sbm-select-item"
          >
            <SelectItemIndicator class="sbm-select-indicator">
              <Check class="icon" :stroke-width="2" />
            </SelectItemIndicator>
            <SelectItemText>{{ option.label }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
