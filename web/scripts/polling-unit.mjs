import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

const transpile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
function fixture(name) {
  let now = 100000, offline = false, delayed, unmount
  let snapshot = name === 'EgressView' ? [] : { subscriptionURL: 'https://node.example.com/sub/demo', subscriptionName: 'Demo', estimatedProviderUsedBytes: 900, quotaExceeded: true }
  class Clock extends Date { static now() { return now } }
  const poll = vm.createContext({ exports: {}, Date: Clock, require: () => Vue })
  vm.runInContext(transpile(readFileSync(new URL('../src/poll-status.ts', import.meta.url), 'utf8')), poll)
  const context = vm.createContext({
    exports: {}, Date: Clock, Intl, JSON, AbortSignal, clearInterval() {},
    require(module) {
      if (module === 'vue') return { ...Vue, onMounted() {}, onBeforeUnmount(fn) { unmount = fn } }
      if (module === '../poll-status') return poll.exports
      if (module === '../i18n') return { t: key => key, dateLocale: () => 'en-US' }
      if (module === '../qr') return { createQrCard: async () => 'demo', downloadQrCard() {} }
      if (module === '../api') return {
        api: async () => {
          if (offline) throw new Error('offline')
          const next = structuredClone(snapshot)
          if (delayed) { const wait = delayed; delayed = undefined; return wait(next) }
          return next
        },
        post: async () => { snapshot = { ...snapshot, estimatedProviderUsedBytes: 0, quotaExceeded: false }; return {} },
        guard: () => action => action,
      }
      return {}
    },
  })
  const source = readFileSync(new URL(`../src/views/${name}.vue`, import.meta.url), 'utf8')
  vm.runInContext(transpile(compileScript(parse(source).descriptor, { id: name }).content), context)
  const view = context.exports.default.setup({}, { emit() {}, expose() {} })
  return { view, offline: value => { offline = value }, advance: n => { now += n }, unmount: () => unmount(), delay: fn => { delayed = fn } }
}

const race = fixture('DashboardView')
await race.view.load()
let finish
race.delay(next => new Promise(resolve => { finish = () => resolve(next) }))
const old = race.view.load()
await race.view.reset()
finish(); await old
assert.equal(race.view.data.value.estimatedProviderUsedBytes, 0, 'old GET replaced reset result')
assert.equal(race.view.data.value.quotaExceeded, false)
race.delay(next => new Promise(resolve => { finish = () => resolve(next) }))
const late = race.view.load()
race.unmount(); finish(); await late
assert.equal(race.view.data.value.estimatedProviderUsedBytes, 0)

for (const name of ['DashboardView', 'EgressView', 'ServerView']) {
  const f = fixture(name)
  await f.view.load()
  f.offline(true)
  await f.view.load().catch(() => {})
  assert.equal(f.view.stale.value, false, `${name} treated a brief outage as stale`)
  f.advance(16000)
  await f.view.load().catch(() => {})
  assert.equal(f.view.stale.value, true, `${name} concealed an outage`)
  f.offline(false); await f.view.load()
  assert.equal(f.view.stale.value, false, `${name} did not recover`)
  f.unmount()
}
console.log('Dashboard request order and polling outage/recovery regressions: passed')
