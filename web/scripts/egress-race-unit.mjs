import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('../src/views/EgressView.vue', import.meta.url), 'utf8')
const compiled = compileScript(parse(source).descriptor, { id: 'egress-race' })
const code = ts.transpileModule(compiled.content, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
let server = [{ id: 'exit', marker: 'OLD', publicKey: 'public', enabled: true, position: 0, trafficQuota: { amount: 1 }, reset: {} }]
let resolveOld
let first = true
let unmount
const api = async () => {
  const result = structuredClone(server)
  if (first) { first = false; return new Promise(resolve => { resolveOld = () => resolve(result) }) }
  return result
}
const context = vm.createContext({
  exports: {}, Intl, JSON, clearInterval() {},
  require(name) {
    if (name === 'vue') return { ...Vue, onMounted() {}, onBeforeUnmount(fn) { unmount = fn } }
    if (name === '../api') return {
      api, guard: notify => action => async (...args) => { try { return await action(...args) } catch (error) { throw error } },
      put: async (_url, input) => { server = [{ ...server[0], ...structuredClone(input) }]; return { geoDetected: true } },
      post: async () => ({}), del: async () => {},
    }
    if (name === '../i18n') return { dateLocale: () => 'en-US', t: key => key }
    return {}
  },
})
vm.runInContext(code, context)
const view = context.exports.default.setup({}, { emit() {}, expose() {} })
const delayed = view.load()
view.items.value = structuredClone(server)
view.edit(view.items.value[0])
view.form.value.marker = 'NEW'
await view.save()
assert.equal(view.items.value[0].marker, 'NEW')
resolveOld(); await delayed
assert.equal(view.items.value[0].marker, 'NEW', 'delayed GET replaced a successful save')
view.edit(view.items.value[0]); view.form.value.trafficQuota.amount = 10
await view.save()
assert.equal(server[0].marker, 'NEW', 'quota edit rewrote an old marker')
assert.equal(server[0].trafficQuota.amount, 10)
unmount()
console.log('Egress delayed GET and subsequent save regression: passed')
