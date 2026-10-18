import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

const transpile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const deferred = () => {
  let resolve, reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const tick = () => new Promise(resolve => setTimeout(resolve, 0))
const i18n = { t: key => key, dateLocale: () => 'en-US', locale: { value: 'en' } }

function mount(file, mocks, props = {}, emitted = []) {
  const context = vm.createContext({
    exports: {}, Date, Intl, JSON, AbortSignal, URLSearchParams, structuredClone,
    setInterval: () => 0, clearInterval() {}, window: { setInterval: () => 0, clearInterval() {} },
    require(name) {
      if (name === 'vue') return { ...Vue, onMounted() {}, onBeforeUnmount() {} }
      return mocks[name] ?? {}
    },
  })
  const source = readFileSync(new URL(`../src/${file}`, import.meta.url), 'utf8')
  vm.runInContext(transpile(compileScript(parse(source).descriptor, { id: 'ui-state' }).content), context)
  return context.exports.default.setup(Vue.reactive(props), { emit: (...args) => emitted.push(args), expose() {} })
}

// Dashboard: quick format switches must leave the QR on the selected format.
{
  const renders = []
  const createQrCard = url => {
    const render = deferred()
    renders.push(render)
    return render.promise
  }
  const snapshot = { subscriptionURL: 'https://node.example/sub/token', subscriptionName: 'Node' }
  const dashboard = mount('views/DashboardView.vue', {
    '../api': { api: async () => structuredClone(snapshot), post: async () => ({}), guard: () => action => action },
    '../i18n': i18n,
    '../qr': { createQrCard, downloadQrCard() {} },
    '../poll-status': { usePollStatus: () => ({ stale: Vue.ref(false), tick() {}, succeeded() {}, failed() {} }) },
  })
  const first = dashboard.load()
  await tick()
  renders[0].resolve('qr:auto')
  await first
  const clash = dashboard.selectSubFormat('clash')
  const singbox = dashboard.selectSubFormat('singbox')
  renders[2].resolve('qr:singbox')
  await singbox
  renders[1].resolve('qr:clash')
  await clash
  assert.equal(dashboard.qr.value, 'qr:singbox', 'a slower earlier render replaced the selected format QR')
  assert.equal(dashboard.subFormat.value, 'singbox')
}

// Date picker: "today" follows the data's zone, and the panel stays in 1970–9999.
{
  const zoned = timeZone => {
    const parts = new Intl.DateTimeFormat('en-US', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).formatToParts(new Date())
    const part = type => parts.find(item => item.type === type).value
    return `${part('year')}-${part('month')}-${part('day')}`
  }
  const props = Vue.reactive({ modelValue: '', type: 'date', timeZone: 'Pacific/Kiritimati' })
  const picker = mount('components/DatePickerControl.vue', { '../i18n': i18n }, props)
  assert.equal(picker.todayISO.value, zoned('Pacific/Kiritimati'))
  const ahead = picker.todayISO.value
  props.timeZone = 'Pacific/Pago_Pago'
  assert.equal(picker.todayISO.value, zoned('Pacific/Pago_Pago'))
  assert.notEqual(picker.todayISO.value, ahead, 'zones 25 hours apart never share a date')
  props.timeZone = 'Not/AZone'
  assert.match(picker.todayISO.value, /^\d{4}-\d{2}-\d{2}$/, 'an unknown zone falls back to the browser')

  picker.viewYear.value = 1970
  picker.viewMonth.value = 1
  const days = picker.calendarDays.value
  assert.ok(days.filter(cell => cell.iso.startsWith('1969-')).every(cell => cell.disabled), 'December 1969 must not be selectable')
  assert.ok(days.filter(cell => cell.iso.startsWith('1970-')).every(cell => !cell.disabled))
  picker.stepMonth(-1)
  assert.equal(`${picker.viewYear.value}-${picker.viewMonth.value}`, '1970-1', 'stepping before January 1970 must stay put')
  assert.deepEqual([...picker.yearGrid.value.filter(item => item.disabled).map(item => item.year)], ['1968', '1969'])
  picker.viewYear.value = 9999
  picker.viewMonth.value = 12
  picker.stepMonth(1)
  assert.equal(`${picker.viewYear.value}-${picker.viewMonth.value}`, '9999-12', 'stepping past December 9999 must stay put')
  assert.ok(picker.calendarDays.value.filter(cell => cell.iso.startsWith('10000-')).every(cell => cell.disabled))
}

console.log('UI state regressions (subscription QR, date picker): passed')
