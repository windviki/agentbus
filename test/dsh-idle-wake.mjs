// DSH idle-wake test: an idle DSH agent must be steered out of the prompt when
// a direct request arrives. The host API (`agent.steer`) is what makes this
// possible; a pure hook-based harness cannot be woken after it has stopped.
//
// Usage: go build -o agentbus . && node test/dsh-idle-wake.mjs
// or:    ./test/dsh-idle-wake.sh
import { execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const bin = process.env.AGENTBUS_BIN || join(root, 'agentbus')
if (!existsSync(bin)) {
  console.error(`FAIL binary not found: ${bin}\n  run: go build -o agentbus .`)
  process.exit(2)
}

const home = mkdtempSync(join(tmpdir(), 'ab-dsh-wake-'))
Object.assign(process.env, {
  AGENTBUS_HOME: home,
  AGENTBUS_BIN: bin,
  AGENTBUS_DSH_POLL_MS: '200',
  AGENTBUS_WEB: '0',
})

const env = { ...process.env }
const cli = (args, input) => execFileSync(bin, args, { env, input, encoding: 'utf8' })

const handlers = new Map()
const cleanups = []
const injected = []
const steered = []
const agent = {
  session: { id: 'mock-dsh-1', header: { cwd: '/tmp' } },
  status: 'idle',
  inject: (m) => injected.push(m),
  steer: (m) => { steered.push(m); agent.status = 'running' },
}
const ctx = {
  on: (name, fn) => handlers.set(name, fn),
  effect: (fn) => { const cleanup = fn(); if (typeof cleanup === 'function') cleanups.push(cleanup) },
  logger: { info: () => {} },
  tools: { register: () => () => {} },
}

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

try {
  cli(['register', '--session', 'sid:mock-sender', '--name', 'mock-sender', '--agent', 'dsh', '--cwd', '/tmp'])
  const { apply } = await import(pathToFileURL(join(root, 'integrations/dsh/plugin/lib/index.js')).href)
  apply(ctx)
  await handlers.get('agent/created')({ agent })
  await wait(300)

  if (!injected.length) throw new Error('SessionStart hook did not inject the roster')
  let mock = null
  for (let i = 0; i < 20 && !mock; i++) {
    await wait(100)
    const who = JSON.parse(cli(['who', '--json']))
    mock = (who.sessions || []).find((s) => s.id === 'sid:mock-dsh-1')
  }
  if (!mock) throw new Error('mock DSH session was not registered')

  cli(['send', '--session', 'sid:mock-sender', '--kind', 'request', mock.name, 'dsh-idle-wake: reply with ack'])

  const deadline = Date.now() + 5000
  while (!steered.length && Date.now() < deadline) await wait(100)
  if (!steered.length) throw new Error('idle agent was not steered for the direct request')

  const payload = JSON.stringify(steered[0])
  if (!payload.includes('dsh-idle-wake: reply with ack')) throw new Error('wake payload lost the request text')
  if (!payload.includes('direct request')) throw new Error('wake payload is not a direct-request wake: ' + payload)
  console.log(`PASS idle DSH wake: ${mock.name} steered with the direct request`)
  console.log('     ' + payload.slice(0, 180) + '…')
} catch (error) {
  console.error('FAIL', error)
  process.exitCode = 1
} finally {
  for (const cleanup of cleanups) { try { cleanup() } catch { /* ignore */ } }
  try { cli(['stop']) } catch { /* daemon may not have started */ }
  rmSync(home, { recursive: true, force: true })
}
