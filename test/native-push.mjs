// Real binary + daemon against mock Claude and Kimi native transports.
import { execFileSync, spawn } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { createServer as createTcpServer } from 'node:net'
import { createServer as createHttpServer } from 'node:http'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const bin = process.env.AGENTBUS_BIN || join(root, 'agentbus')
const home = mkdtempSync(join(tmpdir(), 'agentbus-native-'))
const socketPath = join(home, 'claude.sock')
const baseEnv = { ...process.env, AGENTBUS_HOME: home, AGENTBUS_WEB: '0', AGENTBUS_PRIMER: '0' }
const claudeFrames = []
const kimiRequests = []
let kimiServer
let claudeServer

const cli = (args, { input, env = {} } = {}) => execFileSync(bin, args, {
  env: { ...baseEnv, ...env }, input, encoding: 'utf8', timeout: 15000,
})
const hook = (session, event, env = {}) => cli(['hook'], {
  input: JSON.stringify({ hook_event_name: event, session_id: session, cwd: '/tmp', client_type: env.CLIENT_TYPE }),
  env: { AGENTBUS_SESSION_ID: session, ...env },
})
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
async function until(fn, label, timeout = 5000) {
  const deadline = Date.now() + timeout
  while (!fn()) {
    if (Date.now() > deadline) throw new Error(`timeout: ${label}`)
    await wait(25)
  }
}

try {
  claudeServer = createTcpServer((socket) => {
    let pending = ''
    socket.setEncoding('utf8')
    socket.on('data', (chunk) => {
      pending += chunk
      for (;;) {
        const nl = pending.indexOf('\n')
        if (nl < 0) break
        const line = pending.slice(0, nl)
        pending = pending.slice(nl + 1)
        if (!line) continue
        const frame = JSON.parse(line)
        if (frame.type === 'user') claudeFrames.push(frame)
      }
    })
  })
  await new Promise((resolve) => claudeServer.listen(socketPath, resolve))

  kimiServer = createHttpServer((req, res) => {
    let body = ''
    req.on('data', (chunk) => { body += chunk })
    req.on('end', () => {
      if (req.method === 'POST') kimiRequests.push({ path: req.url, body: JSON.parse(body || '{}'), auth: req.headers.authorization })
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ code: 0, msg: 'success', data: {} }))
    })
  })
  await new Promise((resolve) => kimiServer.listen(0, '127.0.0.1', resolve))
  const kimiURL = `http://127.0.0.1:${kimiServer.address().port}`

  cli(['register', '--session', 'sid:sender', '--name', 'sender', '--agent', 'dsh', '--cwd', '/tmp', '--print-name'])

  const claudeEnv = {
    AGENTBUS_NAME: 'claude-target', AGENTBUS_AGENT: 'claude-code',
    CLAUDE_CODE_MESSAGING_SOCKET: socketPath, CLAUDE_CODE_MESSAGING_TOKEN: 'native-test-token',
  }
  hook('claude-target', 'SessionStart', claudeEnv)
  hook('claude-target', 'UserPromptSubmit', claudeEnv)
  cli(['send', '--session', 'sid:sender', '--kind', 'request', 'claude-target', 'claude native request'])
  await until(() => claudeFrames.length === 1, 'Claude native request')
  let content = claudeFrames[0].message.content
  if (!content.includes('claude native request') || !content.includes('/tmp')) throw new Error('Claude frame lost body/context')
  if (claudeFrames[0].session_id !== 'claude-target') throw new Error('Claude frame targeted the wrong session')
  if (hook('claude-target', 'PostToolUse', claudeEnv) !== '') throw new Error('Claude hook duplicated native delivery')
  hook('claude-target', 'Stop', claudeEnv)
  cli(['send', '--session', 'sid:sender', 'claude-target', 'idle chat stays queued'])
  await wait(300)
  if (claudeFrames.length !== 1) throw new Error('ordinary chat woke an idle Claude session')
  cli(['send', '--session', 'sid:sender', '--kind', 'request', 'claude-target', 'wake with queued chat'])
  await until(() => claudeFrames.length === 2, 'Claude idle request')
  content = claudeFrames[1].message.content
  if (!content.includes('idle chat stays queued') || !content.includes('wake with queued chat')) throw new Error('idle wake did not flush the batch')
  console.log('PASS Claude native socket: working delivery, idle policy, queued batch, no hook duplicate')

  const kimiEnv = {
    CLIENT_TYPE: 'kimi_code_cli', AGENTBUS_NAME: 'kimi-target', AGENTBUS_AGENT: 'kimi',
    AGENTBUS_KIMI_URL: kimiURL, AGENTBUS_KIMI_TOKEN: 'kimi-test-token',
  }
  hook('kimi-target', 'SessionStart', kimiEnv)
  hook('kimi-target', 'TurnStarted', kimiEnv)
  cli(['send', '--session', 'sid:sender', 'kimi-target', 'kimi working steer'])
  await until(() => kimiRequests.length === 1, 'Kimi working steer')
  if (kimiRequests[0].path !== '/api/v1/sessions/kimi-target/steer') throw new Error(`wrong Kimi working endpoint: ${kimiRequests[0].path}`)
  if (kimiRequests[0].auth !== 'Bearer kimi-test-token') throw new Error('Kimi token was not forwarded')
  hook('kimi-target', 'Stop', kimiEnv)
  cli(['notify', '--session', 'sid:sender', 'kimi-target', 'idle notice stays queued'])
  await wait(300)
  if (kimiRequests.length !== 1) throw new Error('ordinary notice woke an idle Kimi session')
  const sent = JSON.parse(cli(['send', '--json', '--session', 'sid:sender', '--kind', 'request', 'kimi-target', 'kimi idle request']))
  await until(() => kimiRequests.length === 2, 'Kimi idle prompt')
  if (kimiRequests[1].path !== '/api/v1/sessions/kimi-target/prompts') throw new Error(`wrong Kimi idle endpoint: ${kimiRequests[1].path}`)
  if (!kimiRequests[1].body.content[0].text.includes('idle notice stays queued')) throw new Error('Kimi idle prompt lost queued notice')
  const receipt = JSON.parse(cli(['receipt', '--json', sent.msg_id]))
  if (receipt.receipts['kimi-target'].state !== 'accepted') throw new Error(`Kimi receipt not accepted: ${JSON.stringify(receipt)}`)
  console.log('PASS Kimi Server API: steer, idle prompt, idempotent prompt id, delivery receipt')

  hook('codex-target', 'SessionStart', { AGENTBUS_NAME: 'codex-target', AGENTBUS_AGENT: 'codex' })
  const watcherInput = JSON.stringify({ hook_event_name: 'UserPromptSubmit', session_id: 'codex-target', cwd: '/tmp' })
  const watcher = spawn(bin, ['hook', '--async-watch'], {
    env: { ...baseEnv, AGENTBUS_SESSION_ID: 'codex-target', AGENTBUS_NAME: 'codex-target', AGENTBUS_AGENT: 'codex' },
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  let watcherOut = ''
  let watcherErr = ''
  watcher.stdout.setEncoding('utf8')
  watcher.stderr.setEncoding('utf8')
  watcher.stdout.on('data', (chunk) => { watcherOut += chunk })
  watcher.stderr.on('data', (chunk) => { watcherErr += chunk })
  watcher.stdin.end(watcherInput)
  await wait(150)
  cli(['send', '--session', 'sid:sender', 'codex-target', 'codex async message'])
  const code = await new Promise((resolve) => watcher.on('close', resolve))
  if (code !== 0 || !watcherOut.includes('codex async message') || !watcherOut.includes('additionalContext')) {
    throw new Error(`Codex async watcher failed (${code}): ${watcherOut} ${watcherErr}`)
  }
  console.log('PASS Codex async hook: active turn receives context without a tool boundary')
  console.log('ALL NATIVE PUSH TESTS PASSED')
} catch (error) {
  console.error(error)
  try { console.error(cli(['who', '--all', '--json'])) } catch { /* daemon unavailable */ }
  try { console.error(readFileSync(join(home, 'state.json'), 'utf8')) } catch { /* no state yet */ }
  try { console.error(readFileSync(join(home, 'daemon.log'), 'utf8')) } catch { /* no daemon log */ }
  process.exitCode = 1
} finally {
  try { cli(['stop']) } catch { /* already stopped */ }
  if (claudeServer) await new Promise((resolve) => claudeServer.close(resolve))
  if (kimiServer) await new Promise((resolve) => kimiServer.close(resolve))
  rmSync(home, { recursive: true, force: true })
}
