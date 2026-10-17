<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { Calendar, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from '@lucide/vue'
import { locale } from '../i18n'

const props = withDefaults(
  defineProps<{
    modelValue: string
    type?: 'date' | 'month' | 'year'
    disabled?: boolean
    ariaLabel?: string
    // Zone the data is grouped in; "today" follows it rather than the browser.
    timeZone?: string
  }>(),
  {
    type: 'date',
    disabled: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: [value: string]
}>()

// The traffic views only accept these years.
const MIN_YEAR = 1970
const MAX_YEAR = 9999
const inRange = (year: number) => year >= MIN_YEAR && year <= MAX_YEAR

const pad2 = (n: number) => String(n).padStart(2, '0')

function isoDate(date: Date, timeZone?: string) {
  if (timeZone) {
    try {
      const parts = new Intl.DateTimeFormat('en-US', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).formatToParts(date)
      const part = (type: string) => parts.find(p => p.type === type)?.value
      return `${part('year')}-${part('month')}-${part('day')}`
    } catch {
      // Unknown zone: fall back to the browser's own.
    }
  }
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`
}

const open = ref(false)
// Refreshed on every open so a page left open past midnight moves "today".
const clock = ref(Date.now())
const todayISO = computed(() => isoDate(new Date(clock.value), props.timeZone))
const viewYear = ref(Number(todayISO.value.slice(0, 4)))
const viewMonth = ref(Number(todayISO.value.slice(5, 7)))

function syncFromValue(val: string) {
  const parts = (val || '').split('-').map(Number)
  viewYear.value = parts[0] && inRange(parts[0]) ? parts[0] : Number(todayISO.value.slice(0, 4))
  viewMonth.value = parts[1] && parts[1] >= 1 && parts[1] <= 12 ? parts[1] : Number(todayISO.value.slice(5, 7))
}

watch(
  () => [props.modelValue, open.value] as const,
  ([val, isOpen]) => {
    if (!isOpen) return
    clock.value = Date.now()
    syncFromValue(val)
  },
  { immediate: true },
)

const currentMonthISO = computed(() => todayISO.value.slice(0, 7))
const currentYearISO = computed(() => todayISO.value.slice(0, 4))

const weekdays = computed(() =>
  locale.value === 'zh-CN'
    ? ['一', '二', '三', '四', '五', '六', '日']
    : ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'],
)

const monthLabels = computed(() =>
  locale.value === 'zh-CN'
    ? ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月']
    : ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'],
)

const calendarDays = computed(() => {
  const y = viewYear.value
  const m = viewMonth.value
  const firstDay = new Date(y, m - 1, 1)
  // Monday-first offset (0 = Monday, 6 = Sunday)
  const startOffset = (firstDay.getDay() + 6) % 7
  const daysInMonth = new Date(y, m, 0).getDate()
  const prevMonthDays = new Date(y, m - 1, 0).getDate()

  const cells: Array<{
    iso: string
    day: number
    outside: boolean
    disabled: boolean
    isToday: boolean
    isSelected: boolean
  }> = []

  // Leading days from previous month
  const prevY = m === 1 ? y - 1 : y
  const prevM = m === 1 ? 12 : m - 1
  for (let i = startOffset - 1; i >= 0; i--) {
    const d = prevMonthDays - i
    const iso = `${prevY}-${pad2(prevM)}-${pad2(d)}`
    cells.push({
      iso,
      day: d,
      outside: true,
      disabled: !inRange(prevY),
      isToday: iso === todayISO.value,
      isSelected: iso === props.modelValue,
    })
  }

  // Current month days
  for (let d = 1; d <= daysInMonth; d++) {
    const iso = `${y}-${pad2(m)}-${pad2(d)}`
    cells.push({
      iso,
      day: d,
      outside: false,
      disabled: false,
      isToday: iso === todayISO.value,
      isSelected: iso === props.modelValue,
    })
  }

  // Trailing days to complete 42 cells (6 rows)
  const nextY = m === 12 ? y + 1 : y
  const nextM = m === 12 ? 1 : m + 1
  const remaining = 42 - cells.length
  for (let d = 1; d <= remaining; d++) {
    const iso = `${nextY}-${pad2(nextM)}-${pad2(d)}`
    cells.push({
      iso,
      day: d,
      outside: true,
      disabled: !inRange(nextY),
      isToday: iso === todayISO.value,
      isSelected: iso === props.modelValue,
    })
  }

  return cells
})

const yearStart = computed(() => Math.floor(viewYear.value / 12) * 12)
const yearGrid = computed(() =>
  Array.from({ length: 12 }, (_, idx) => {
    const y = String(yearStart.value + idx)
    return {
      year: y,
      disabled: !inRange(yearStart.value + idx),
      isCurrent: y === currentYearISO.value,
      isSelected: y === props.modelValue,
    }
  }),
)

function stepMonth(delta: number) {
  let m = viewMonth.value + delta
  let y = viewYear.value
  while (m < 1) {
    m += 12
    y -= 1
  }
  while (m > 12) {
    m -= 12
    y += 1
  }
  if (!inRange(y)) return
  viewYear.value = y
  viewMonth.value = m
}

function stepYear(delta: number) {
  viewYear.value = Math.min(MAX_YEAR, Math.max(MIN_YEAR, viewYear.value + delta))
}

function commit(val: string) {
  open.value = false
  if (val !== props.modelValue) {
    emit('update:modelValue', val)
    emit('change', val)
  }
}

function selectMonthCell(m: number) {
  commit(`${viewYear.value}-${pad2(m)}`)
}

function selectQuickToday() {
  if (props.type === 'date') commit(todayISO.value)
  else if (props.type === 'month') commit(currentMonthISO.value)
  else commit(currentYearISO.value)
}

const navLabels = computed(() =>
  locale.value === 'zh-CN'
    ? { prevYear: '上一年', nextYear: '下一年', prevMonth: '上个月', nextMonth: '下个月', prevYears: '前 12 年', nextYears: '后 12 年' }
    : { prevYear: 'Previous year', nextYear: 'Next year', prevMonth: 'Previous month', nextMonth: 'Next month', prevYears: 'Previous 12 years', nextYears: 'Next 12 years' },
)

const quickLabel = computed(() => {
  if (locale.value === 'zh-CN') {
    return props.type === 'date' ? '今天' : props.type === 'month' ? '本月' : '今年'
  }
  return props.type === 'date' ? 'Today' : props.type === 'month' ? 'This month' : 'This year'
})
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger
      type="button"
      class="sbm-datepicker-trigger"
      :disabled="disabled"
      :aria-label="ariaLabel"
    >
      <span class="sbm-datepicker-value">{{ modelValue || '—' }}</span>
      <Calendar class="icon sbm-datepicker-icon" :stroke-width="1.8" aria-hidden="true" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="end"
        :side-offset="4"
        class="sbm-datepicker-popover"
      >
        <!-- Date (YYYY-MM-DD) Picker -->
        <template v-if="type === 'date'">
          <div class="sbm-dp-head">
            <div class="sbm-dp-nav-group">
              <button type="button" class="sbm-dp-nav" :aria-label="navLabels.prevYear" :disabled="viewYear <= MIN_YEAR" @click="stepYear(-1)">
                <ChevronsLeft class="icon" :stroke-width="1.8" />
              </button>
              <button type="button" class="sbm-dp-nav" :aria-label="navLabels.prevMonth" :disabled="viewYear <= MIN_YEAR && viewMonth === 1" @click="stepMonth(-1)">
                <ChevronLeft class="icon" :stroke-width="1.8" />
              </button>
            </div>
            <strong class="sbm-dp-title">{{ viewYear }} / {{ pad2(viewMonth) }}</strong>
            <div class="sbm-dp-nav-group">
              <button type="button" class="sbm-dp-nav" :aria-label="navLabels.nextMonth" :disabled="viewYear >= MAX_YEAR && viewMonth === 12" @click="stepMonth(1)">
                <ChevronRight class="icon" :stroke-width="1.8" />
              </button>
              <button type="button" class="sbm-dp-nav" :aria-label="navLabels.nextYear" :disabled="viewYear >= MAX_YEAR" @click="stepYear(1)">
                <ChevronsRight class="icon" :stroke-width="1.8" />
              </button>
            </div>
          </div>
          <div class="sbm-dp-weekdays">
            <span v-for="w in weekdays" :key="w">{{ w }}</span>
          </div>
          <div class="sbm-dp-days">
            <button
              v-for="cell in calendarDays"
              :key="cell.iso"
              type="button"
              class="sbm-dp-day"
              :class="{
                'is-outside': cell.outside,
                'is-today': cell.isToday,
                'is-selected': cell.isSelected,
              }"
              :disabled="cell.disabled"
              @click="commit(cell.iso)"
            >
              {{ cell.day }}
            </button>
          </div>
        </template>

        <!-- Month (YYYY-MM) Picker -->
        <template v-else-if="type === 'month'">
          <div class="sbm-dp-head">
            <button type="button" class="sbm-dp-nav" :aria-label="navLabels.prevYear" :disabled="viewYear <= MIN_YEAR" @click="stepYear(-1)">
              <ChevronLeft class="icon" :stroke-width="1.8" />
            </button>
            <strong class="sbm-dp-title">{{ viewYear }}</strong>
            <button type="button" class="sbm-dp-nav" :aria-label="navLabels.nextYear" :disabled="viewYear >= MAX_YEAR" @click="stepYear(1)">
              <ChevronRight class="icon" :stroke-width="1.8" />
            </button>
          </div>
          <div class="sbm-dp-grid">
            <button
              v-for="(label, idx) in monthLabels"
              :key="idx"
              type="button"
              class="sbm-dp-cell"
              :class="{
                'is-today': `${viewYear}-${pad2(idx + 1)}` === currentMonthISO,
                'is-selected': `${viewYear}-${pad2(idx + 1)}` === modelValue,
              }"
              @click="selectMonthCell(idx + 1)"
            >
              {{ label }}
            </button>
          </div>
        </template>

        <!-- Year (YYYY) Picker -->
        <template v-else>
          <div class="sbm-dp-head">
            <button type="button" class="sbm-dp-nav" :aria-label="navLabels.prevYears" :disabled="yearStart <= MIN_YEAR" @click="stepYear(-12)">
              <ChevronLeft class="icon" :stroke-width="1.8" />
            </button>
            <strong class="sbm-dp-title">{{ yearStart }} – {{ yearStart + 11 }}</strong>
            <button type="button" class="sbm-dp-nav" :aria-label="navLabels.nextYears" :disabled="yearStart + 11 >= MAX_YEAR" @click="stepYear(12)">
              <ChevronRight class="icon" :stroke-width="1.8" />
            </button>
          </div>
          <div class="sbm-dp-grid">
            <button
              v-for="item in yearGrid"
              :key="item.year"
              type="button"
              class="sbm-dp-cell"
              :class="{
                'is-today': item.isCurrent,
                'is-selected': item.isSelected,
              }"
              :disabled="item.disabled"
              @click="commit(item.year)"
            >
              {{ item.year }}
            </button>
          </div>
        </template>

        <div class="sbm-dp-foot">
          <button type="button" class="sbm-dp-quick" @click="selectQuickToday">
            {{ quickLabel }}
          </button>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

