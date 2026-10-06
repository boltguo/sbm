import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import vm from 'node:vm'
import ts from 'typescript'
const root = fileURLToPath(new URL('../src/', import.meta.url))
const source = readFileSync(`${root}egress-i18n.ts`, 'utf8')
const js = ts.transpile(source.replace('export const', 'globalThis.messages ='), { target: ts.ScriptTarget.ES2022 })
const ctx = vm.createContext({})
vm.runInContext(js, ctx)
const { messages } = ctx
const chinese = Object.keys(messages['zh-CN']).sort()
const english = Object.keys(messages.en).sort()
if (JSON.stringify(chinese) !== JSON.stringify(english)) throw new Error('Egress translations differ between languages')
for (const file of ['App.vue', ...['views', 'components'].flatMap(dir => readdirSync(`${root}${dir}`).filter(n => n.endsWith('.vue')).map(n => `${dir}/${n}`))]) {
  const text = readFileSync(`${root}${file}`, 'utf8')
  for (const [, key] of text.matchAll(/['"](egress\.[a-zA-Z]+(?:\.[a-zA-Z]+)?)['"]/g)) {
    if (!messages.en[key]) throw new Error(`Missing Egress translation: ${key}`)
  }
}
for (const key of chinese) {
  const placeholders = text => [...text.matchAll(/\{([^}]+)\}/g)].map(m => m[1]).sort().join(',')
  if (placeholders(messages.en[key]) !== placeholders(messages['zh-CN'][key])) throw new Error(`Placeholder mismatch: ${key}`)
}
console.log(`Egress i18n: ${chinese.length} keys match in both languages`)
