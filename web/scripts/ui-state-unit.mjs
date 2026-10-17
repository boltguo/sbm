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

// Traffic apps: a routes poll that started before an inline save must not undo it.
{
  const polls = []
  const view = routes => ({ gateways: [{ id: 'gw', name: 'GW', location: '', enabled: true }], apps: [], routes })
  const api = async url => {
    if (url === '/api/egress/app-routes') {
      const poll = deferred()
      polls.push(poll)
      return poll.promise
    }
    return { apps: [], totalBytes: 0, totalUpload: 0, totalDownload: 0, timezone: 'UTC' }
  }
  const put = async () => view({ openai: 'gw' })
  const apps = mount('components/TrafficApps.vue', { '../api': { api, put, post: async () => ({}), guard: () => action => action }, '../i18n': i18n }, { unit: 'GB' })
  const loading = apps.load(true)
  await apps.changeInlineRoute('openai', 'gw')
  polls[0].resolve(view({}))
  await loading
  await tick()
  assert.deepEqual({ ...apps.routesView.value.routes }, { openai: 'gw' }, 'a stale poll replaced the saved route')
  const fresh = apps.load(true)
  polls[1].resolve(view({ youtube: 'direct' }))
  await fresh
  await tick()
  assert.deepEqual({ ...apps.routesView.value.routes }, { youtube: 'direct' }, 'a poll after the save must still apply')
}

// Routing modal: selections wait until applied, then go out in one request.
{
  const view = routes => ({ gateways: [{ id: 'gw', name: 'GW', location: '', enabled: true }], apps: [], routes })
  const fetches = []
  const puts = []
  let failNext = false
  const api = async () => {
    const fetch = deferred()
    fetches.push(fetch)
    return fetch.promise
  }
  const put = async (_url, body) => {
    puts.push(structuredClone(body))
    if (failNext) {
      failNext = false
      throw new Error('apply failed')
    }
    return view({ youtube: 'direct' })
  }
  const emitted = []
  const modal = mount('components/AppRouteModal.vue', { '../api': { api, put, guard: () => action => action }, '../i18n': i18n }, { open: false, initialData: view({ openai: 'gw' }) }, emitted)

  modal.stageRoute('youtube', 'direct')
  modal.stageRoute('openai', '')
  modal.stageRoute('telegram', 'gw')
  modal.stageRoute('telegram', '')
  assert.equal(puts.length, 0, 'choosing a route must not apply it yet')
  assert.deepEqual({ ...modal.pending.value }, { youtube: 'direct', openai: '' }, 'reverting a choice must drop it')
  assert.equal(modal.targetOf('openai'), '')
  assert.equal(modal.targetOf('youtube'), 'direct')

  failNext = true
  await assert.rejects(modal.applyChanges())
  assert.equal(modal.pendingCount.value, 2, 'a failed apply must keep the choices')
  assert.equal(modal.savingId.value, '')

  const opened = modal.fetchRoutes()
  await modal.applyChanges()
  fetches[0].resolve(view({ openai: 'gw' }))
  await opened
  assert.deepEqual(puts.at(-1), { changes: { youtube: 'direct', openai: '' } }, 'all choices go out in one request')
  assert.equal(puts.length, 2)
  assert.deepEqual({ ...modal.view.value.routes }, { youtube: 'direct' }, 'the fetch started on open replaced the applied routes')
  assert.equal(modal.pendingCount.value, 0)
  assert.ok(emitted.some(([name]) => name === 'updated') && emitted.some(([name]) => name === 'close'))
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

console.log('UI state regressions (routes, staged routing, subscription QR, date picker): passed')
