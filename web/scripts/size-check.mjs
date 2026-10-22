// Bundle budget check (FE-11): initial JS ≤ 120 KB gzipped; any route ≤ 250 KB gzipped
// including its chart chunk. Reads dist/index.html and the Vite manifest-less output.
import { readFileSync, readdirSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { join } from 'node:path'

const dist = new URL('../dist/', import.meta.url).pathname
const assets = join(dist, 'assets')
const gz = (f) => gzipSync(readFileSync(join(assets, f))).length
const kb = (n) => (n / 1024).toFixed(1) + ' KB'

const html = readFileSync(join(dist, 'index.html'), 'utf8')
const initial = [...html.matchAll(/assets\/([^"']+\.js)/g)].map((m) => m[1])
const imports = (f) => [...readFileSync(join(assets, f), 'utf8').matchAll(/["']\.\/([\w.-]+\.js)["']/g)].map((m) => m[1])

// initial = entry + its static imports (modulepreload links are in index.html too)
const initialSet = new Set(initial)
let initialSize = 0
for (const f of initialSet) initialSize += gz(f)

let failed = false
console.log(`initial JS: ${kb(initialSize)} (budget 120 KB) — ${[...initialSet].join(', ')}`)
if (initialSize > 120 * 1024) failed = true

for (const f of readdirSync(assets).filter((f) => f.endsWith('.js') && !initialSet.has(f))) {
  const seen = new Set()
  const walk = (x) => {
    if (seen.has(x) || initialSet.has(x)) return
    seen.add(x)
    imports(x).forEach(walk)
  }
  walk(f)
  let total = 0
  for (const x of seen) total += gz(x)
  const over = total > 250 * 1024
  if (over) failed = true
  console.log(`${over ? 'FAIL' : 'ok  '} ${f}: ${kb(total)} with deps`)
}
if (failed) {
  console.error('bundle budget exceeded')
  process.exit(1)
}
