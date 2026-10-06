import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'
const source = readFileSync(new URL('../src/components/PanelUpdate.vue', import.meta.url), 'utf8')
const compiled = compileScript(parse(source).descriptor, { id: 'panel-update' })
const code = ts.transpileModule(compiled.content, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
function fixture() {
  let status = { state: 'idle' }, failPoll = false, lostResponse = false, reloads = 0, posts = 0, intervals = 0, unmount, finishPost
  let delayedPost = false
  const notices = []
  const context = vm.createContext({
    exports: {}, AbortSignal,
    clearInterval() {},
    window: { setInterval() { return ++intervals }, location: { reload() { reloads++ } } },
    require(name) {
      if (name === 'vue') return { ...Vue, onMounted() {}, onBeforeUnmount(fn) { unmount = fn } }
      if (name === '../api') return {
        api: async url => {
          if (url === '/api/update') return { updateAvailable: true, latestVersion: 'v2.1.1', canInstall: true }
          if (failPoll) throw new Error('temporarily disconnected')
          return structuredClone(status)
        },
        post: async () => { posts++; status = { state: 'running', phase: 'queued', targetVersion: 'v2.1.1' }; if (delayedPost) await new Promise(resolve => { finishPost = resolve }); if (lostResponse) throw new Error('lost launch response'); return structuredClone(status) },
        errorMessage: e => e.message,
      }
      if (name === '../i18n') return { t: key => key }
      return {}
    },
  })
  vm.runInContext(code, context)
  const view = context.exports.default.setup({}, { emit: (_event, text) => notices.push(text), expose() {} })
  return { view, notices, unmount: () => unmount(), status: s => { status = s }, fail: b => { failPoll = b }, lost: () => { lostResponse = true }, delay: () => { delayedPost = true }, finish: () => finishPost(), counters: () => ({ reloads, posts, intervals }) }
}
const f = fixture()
await f.view.check(); assert.equal(f.view.open.value, true)
await f.view.install(); await f.view.install()
assert.equal(f.counters().posts, 1, 'double-click launched multiple jobs')
f.fail(true); await f.view.readProgress(true)
assert.equal(f.view.reconnecting.value, true)
assert.equal(f.view.job.value.state, 'running', 'temporary restart connection loss became a failure')
f.fail(false); f.status({ state: 'succeeded' }); await f.view.readProgress(true)
assert.equal(f.counters().reloads, 1, 'successful update did not reload')
f.unmount()
const failure = fixture(); await failure.view.install()
failure.status({ state: 'failed', rolledBack: true }); await failure.view.readProgress(true)
assert.equal(failure.view.job.value.rolledBack, true)
assert.equal(failure.counters().reloads, 0, 'failed update was displayed as success')
failure.unmount()
const restored = fixture(); restored.status({ state: 'running', phase: 'restarting' }); await restored.view.readProgress()
assert.equal(restored.view.open.value, true, 'reopened page lost the running job')
assert.equal(restored.counters().intervals, 1)
restored.view.open.value = false; await restored.view.readProgress(true)
assert.equal(restored.view.open.value, false, 'background poll forced a closed dialog open again')
restored.unmount()
const lost = fixture(); lost.lost(); await lost.view.install()
assert.equal(lost.view.job.value.state, 'running', 'lost response lost an already launched job')
assert.equal(lost.notices.length, 0)
lost.unmount()
const navigated = fixture(); navigated.delay()
const pending = navigated.view.install()
navigated.unmount(); navigated.finish(); await pending
assert.equal(navigated.counters().intervals, 0, 'late launch response created a timer after leaving the page')
console.log('Panel update polling, reconnect, resume, rollback and lost response regressions: passed')
