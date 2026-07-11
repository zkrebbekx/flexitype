// Guards against the "component used in a template but never imported" bug
// (Vue silently renders unknown components as nothing in production).
// vue-tsc's strictTemplates would catch this too, but it also rejects
// legitimate fallthrough attributes, so we check just this one class here.
//
// Runs in CI via `npm run check:components`. Exits non-zero on any use of
// a PascalCase component with no matching import in the same SFC.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

const SRC = new URL('../src', import.meta.url).pathname

// Components resolved globally by Vue or the router — never imported per-SFC.
const GLOBAL = new Set([
  'RouterLink',
  'RouterView',
  'Transition',
  'TransitionGroup',
  'Teleport',
  'KeepAlive',
  'Suspense',
  'Component',
])

function vueFiles(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...vueFiles(p))
    else if (name.endsWith('.vue')) out.push(p)
  }
  return out
}

function templateBlock(src) {
  const start = src.indexOf('<template')
  if (start === -1) return ''
  const open = src.indexOf('>', start)
  const end = src.lastIndexOf('</template>')
  return open === -1 || end === -1 ? '' : src.slice(open + 1, end)
}

let failures = 0
for (const file of vueFiles(SRC)) {
  const src = readFileSync(file, 'utf8')
  const scriptEnd = src.indexOf('</script>')
  const script = scriptEnd === -1 ? src : src.slice(0, scriptEnd)
  const template = templateBlock(src)

  const used = new Set()
  for (const m of template.matchAll(/<([A-Z][A-Za-z0-9]*)[\s/>]/g)) used.add(m[1])

  for (const name of used) {
    if (GLOBAL.has(name)) continue
    // Matches `import Name from …`, `import { Name }`, `Name as`, `, Name`.
    const imported = new RegExp(`\\b${name}\\b`).test(
      script.match(/import[\s\S]*?from\s+['"][^'"]+['"]/g)?.join('\n') ?? '',
    )
    if (!imported) {
      console.error(`${file}: <${name}> is used in the template but never imported`)
      failures++
    }
  }
}

if (failures) {
  console.error(`\n${failures} unresolved component reference(s) found.`)
  process.exit(1)
}
console.log('component imports OK')
