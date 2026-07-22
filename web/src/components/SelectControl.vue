<script setup lang="ts">
import { computed } from 'vue'
import { SelectContent, SelectIcon, SelectItem, SelectItemIndicator, SelectItemText, SelectPortal, SelectRoot, SelectTrigger, SelectValue, SelectViewport } from 'reka-ui'

const props = defineProps<{ modelValue?: string; options: Array<{ value: string; label: string }> }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const selectedLabel = computed(() => props.options.find(option => option.value === props.modelValue)?.label || '')
function update(value: unknown) { emit('update:modelValue', String(value ?? '')) }
</script>

<template>
  <SelectRoot :model-value="modelValue" @update:model-value="update">
    <SelectTrigger class="flex min-h-11 w-full items-center justify-between border border-[var(--line)] bg-white/70 px-3 text-left outline-none focus:border-[var(--ink)] focus:shadow-[inset_3px_0_var(--signal)]">
      <SelectValue>{{ selectedLabel }}</SelectValue>
      <SelectIcon class="text-[var(--muted)]" aria-hidden="true">⌄</SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent position="popper" :side-offset="4" class="z-[70] min-w-[var(--reka-select-trigger-width)] border border-[var(--line)] bg-[var(--paper)] p-1 shadow-xl">
        <SelectViewport>
          <SelectItem v-for="option in options" :key="option.value" :value="option.value" class="relative flex cursor-default select-none items-center px-8 py-2 text-sm outline-none data-[highlighted]:bg-[var(--signal)]">
            <SelectItemIndicator class="absolute left-2">✓</SelectItemIndicator>
            <SelectItemText>{{ option.label }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
