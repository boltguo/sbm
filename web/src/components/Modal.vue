<script setup lang="ts">
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import Icon from './Icon.vue'
import { t } from '../i18n'

defineProps<{ title: string; description?: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <DialogRoot :open="true" @update:open="open => !open && emit('close')">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-50 bg-[#151914]/35 backdrop-blur-[2px]" />
      <DialogContent
        class="fixed bottom-0 left-1/2 z-50 max-h-[90vh] w-full -translate-x-1/2 overflow-y-auto border border-[var(--line)] bg-[var(--paper)] shadow-[0_12px_45px_rgba(20,25,20,.18)] outline-none sm:bottom-auto sm:top-1/2 sm:-translate-y-1/2"
        :class="wide ? 'sm:max-w-[760px]' : 'sm:max-w-[520px]'"
      >
        <header class="flex items-center justify-between border-b border-[var(--line)] px-6 py-5">
          <div><span class="eyebrow">CONFIGURATION</span><DialogTitle class="mt-1 font-[var(--display)] text-3xl font-extrabold">{{ title }}</DialogTitle><DialogDescription class="sr-only">{{ description || title }}</DialogDescription></div>
          <DialogClose class="icon-button" :aria-label="t('close')"><Icon name="x"/></DialogClose>
        </header>
        <slot />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
