<script setup lang="ts">
import { AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogOverlay, AlertDialogPortal, AlertDialogRoot, AlertDialogTitle, AlertDialogTrigger } from 'reka-ui'
import { t } from '../i18n'

withDefaults(defineProps<{ title: string; message: string; destructive?: boolean }>(), { destructive: false })
defineEmits<{ confirm: [] }>()
</script>

<template>
  <AlertDialogRoot>
    <AlertDialogTrigger as-child><slot /></AlertDialogTrigger>
    <AlertDialogPortal>
      <AlertDialogOverlay class="fixed inset-0 z-[60] bg-[#151914]/35 backdrop-blur-[2px]" />
      <AlertDialogContent class="fixed left-1/2 top-1/2 z-[61] w-[calc(100%-32px)] max-w-md -translate-x-1/2 -translate-y-1/2 border border-[var(--line)] bg-[var(--paper)] p-6 shadow-2xl outline-none">
        <AlertDialogTitle class="font-[var(--display)] text-2xl font-extrabold">{{ title }}</AlertDialogTitle>
        <AlertDialogDescription class="mt-3 text-sm leading-6 text-[var(--muted)]">{{ message }}</AlertDialogDescription>
        <div class="mt-6 flex justify-end gap-2">
          <AlertDialogCancel class="secondary">{{ t('protocol.cancel') }}</AlertDialogCancel>
          <AlertDialogAction class="primary" :class="destructive ? '!border-[var(--red)] !bg-[var(--red)] !text-white' : ''" @click="$emit('confirm')">{{ t('confirm') }}</AlertDialogAction>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
