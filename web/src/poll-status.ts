import { computed, ref } from 'vue'

export function usePollStatus() {
  const lastSuccess = ref(0)
  const failure = ref(false)
  const now = ref(Date.now())
  const stale = computed(() => failure.value && (!lastSuccess.value || now.value - lastSuccess.value >= 15000))
  const tick = () => { now.value = Date.now() }
  const succeeded = () => { tick(); lastSuccess.value = now.value; failure.value = false }
  const failed = () => { tick(); failure.value = true }
  return { stale, tick, succeeded, failed }
}
