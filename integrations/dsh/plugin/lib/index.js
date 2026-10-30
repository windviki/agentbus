/**
 * agentbus —— DeepSeek Harness (DSH) plugin.
 *
 * Bridges one DSH Host process to the local agentbus daemon, so sessions in
 * this host can discover and message sessions of other CLIs (Claude Code,
 * ZCode, another DSH host) through the same bus.
 *
 * The plugin is a thin client: every behaviour reuses the `agentbus hook`
 * entrypoint, so the framing text (session contract, peer-vs-user provenance,
 * reply hints) and the delivery ladder stay identical to the hook-based
 * integrations:
 *
 *   agent/created        -> `agentbus hook` SessionStart, inject contract+roster
 *   agent/pre-step       -> `agentbus hook` UserPromptSubmit (turn boundary)
 *   tools/post-execute   -> `agentbus hook` PostToolUse (same-step injection)
 *   agent/turn-stopping  -> `agentbus hook` Stop (urgent / direct request => steer/continue)
 *   agent/disposed       -> `agentbus leave`
 *
 * Plus native tools: agentbus_who / send / ask / reply / announce / inbox /
 * primer. Nothing in the host is blocked on the bus: every spawn is guarded,
 * time-bounded and failure-tolerant (a throw inside a plugin callback would
 * take the whole Host down).
 */

import { spawn } from 'node:child_process'
import { appendFileSync, existsSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'

export const name = 'agentbus'
/** Tools are always mounted by dsh-base; delivery events work regardless. */
export const inject = ['tools']

/**
 * Resolve a DSH-internal package from the running harness install. A plugin
 * loaded by absolute path sits outside the profile's node_modules, so a bare
 * `import '@deepseek-ai/...'` is not resolvable; the harness install itself is.
 */
function loaderLog(message) {
  if (process.env.AGENTBUS_DSH_DEBUG !== '1') return
  try {
    appendFileSync('/tmp/agentbus-dsh-loader.log', new Date().toISOString() + ' ' + message + '\n')
  } catch {
    /* ignore */
  }
}

async function loadDshPackage(pkg) {
  const candidates = []
  loaderLog('argv[1]=' + String(process.argv[1]))
  if (process.env.DSH_INSTALL_ROOT) {
    candidates.push(join(process.env.DSH_INSTALL_ROOT, 'node_modules', pkg, 'lib', 'index.js'))
  }
  let bin = process.argv[1] || ''
  try {
    bin = realpathSync(bin)
  } catch {
    /* keep the raw path */
  }
  if (bin) candidates.push(join(dirname(bin), '..', 'node_modules', pkg, 'lib', 'index.js'))
  if (process.env.DSH_HOME) {
    candidates.push(join(process.env.DSH_HOME, 'profiles', 'web', 'node_modules', pkg, 'lib', 'index.js'))
  }
  for (const candidate of candidates) {
    try {
      if (!existsSync(candidate)) {
        loaderLog(pkg + ': missing ' + candidate)
        continue
      }
      loaderLog(pkg + ': loading ' + candidate)
      const mod = await import(pathToFileURL(candidate).href)
      loaderLog(pkg + ': keys=' + Object.keys(mod).slice(0, 8).join(',') + ' defineTool=' + typeof mod.defineTool)
      return mod
    } catch (error) {
      loaderLog(pkg + ': failed ' + candidate + ' ' + String(error))
    }
  }
  loaderLog(pkg + ': NOT FOUND')
  return null
}

const llmPackage = await loadDshPackage('@deepseek-ai/dsh-llm')
const toolsPackage = await loadDshPackage('@deepseek-ai/dsh-tools')
const createUserMessage = llmPackage?.createUserMessage
const defineTool = toolsPackage?.defineTool

const BIN = process.env.AGENTBUS_BIN || 'agentbus'
const HOME = process.env.AGENTBUS_HOME || ''
const DEFAULT_TIMEOUT_MS = 5000

/** Optional support logging: AGENTBUS_DSH_DEBUG=1 writes to stderr via ctx.logger. */
let debugSink = null
function dbg(message) {
  if (process.env.AGENTBUS_DSH_DEBUG !== '1') return
  try {
    if (debugSink) debugSink(message)
  } catch {
    /* ignore */
  }
}
const ASK_TIMEOUT_MS = 180000
/**
 * Idle wake interval. A Stop hook cannot fire after the agent has already
 * stopped, so a direct request sent while the session sits at the prompt
 * would wait for the human. While an agent is idle we poll the same policy
 * through `agentbus hook Stop` and steer it awake when a request/urgent is
 * queued. `agent.steer()` on an idle driver starts a turn. 0 disables the
 * poller; AGENTBUS_DSH_POLL_MS overrides the 5s default.
 */
const IDLE_POLL_MS = (() => {
  const raw = process.env.AGENTBUS_DSH_POLL_MS
  const n = raw == null ? 5000 : Number(raw)
  return Number.isFinite(n) ? Math.max(0, Math.min(n, 60000)) : 5000
})()

function sessionKey(agent) {
  const id = String(agent?.session?.id ?? '')
  return id.startsWith('sid:') ? id : 'sid:' + id
}

function envFor(agent) {
  const env = {
    ...process.env,
    AGENTBUS_AGENT: 'dsh',
    AGENTBUS_SESSION_ID: sessionKey(agent),
  }
  if (HOME) env.AGENTBUS_HOME = HOME
  return env
}

/**
 * Run the agentbus CLI. Never rejects: resolves {code, stdout, stderr} with
 * code -1 when the process could not be started or timed out.
 */
function runCli(args, agent, { input, timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  return new Promise((resolve) => {
    let child
    try {
      child = spawn(BIN, args, {
        env: envFor(agent),
        stdio: ['pipe', 'pipe', 'pipe'],
      })
    } catch (error) {
      resolve({ code: -1, stdout: '', stderr: String(error) })
      return
    }
    let stdout = ''
    let stderr = ''
    let settled = false
    const finish = (result) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      resolve(result)
    }
    const timer = setTimeout(() => {
      try { child.kill('SIGKILL') } catch { /* ignore */ }
      finish({ code: -1, stdout, stderr: stderr + '\n(timeout)' })
    }, timeoutMs)
    child.stdout.setEncoding('utf8')
    child.stderr.setEncoding('utf8')
    child.stdout.on('data', (chunk) => { stdout += chunk })
    child.stderr.on('data', (chunk) => { stderr += chunk })
    child.on('error', (error) => finish({ code: -1, stdout, stderr: stderr + String(error) }))
    child.on('close', (code) => finish({ code: code ?? -1, stdout, stderr }))
    try {
      child.stdin.end(input ?? '')
    } catch {
      /* the process may have exited already */
    }
  })
}

/** Run one hook event for an agent; returns the parsed hook JSON (or null). */
async function runHook(agent, event) {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: String(agent?.session?.id ?? ''),
    cwd: agent?.session?.header?.cwd ?? '',
  })
  const { stdout } = await runCli(['hook'], agent, {
    input: payload,
    timeoutMs: event === 'SessionStart' ? 15000 : DEFAULT_TIMEOUT_MS,
  })
  try {
    return JSON.parse(stdout)
  } catch {
    return null
  }
}

function additionalContextOf(hookOutput) {
  const text = hookOutput?.hookSpecificOutput?.additionalContext
  return typeof text === 'string' ? text : ''
}

let fallbackSeq = 0

function relayMessage(text, form = 'relay') {
  if (createUserMessage) {
    return createUserMessage({
      content: [{ type: 'text', text }],
      source: { kind: 'agentbus', form },
    })
  }
  // Fallback when the harness packages could not be resolved: a structurally
  // identical user message. Never let a resolution problem break delivery.
  return Object.freeze({
    id: `agentbus-${Date.now().toString(36)}-${++fallbackSeq}`,
    role: 'user',
    content: Object.freeze([{ type: 'text', text }]),
    source: { kind: 'agentbus', form },
  })
}

/** Per-agent serialization: hook invocations for one session must not race. */
function makeSerial() {
  const queues = new Map()
  return (agent, task) => {
    const key = sessionKey(agent)
    const previous = queues.get(key) ?? Promise.resolve()
    const next = previous.then(task, task)
    queues.set(key, next.catch(() => {}))
    return next
  }
}

function toolAgent(exec) {
  return exec?.agent
}

function cliResultText(result) {
  if (result.code === 0) return result.stdout.trim() || '(no output)'
  return (result.stderr.trim() || result.stdout.trim() || `agentbus exited with code ${result.code}`)
}

export function apply(ctx) {
  const serial = makeSerial()
  const liveAgents = new Set()
  const polling = new Set()
  try {
    debugSink = (message) => ctx.logger?.info?.('[agentbus] ' + message)
  } catch {
    debugSink = null
  }
  dbg('mounted (tools service ' + (ctx.tools ? 'present' : 'missing') + ')')

  // 1) Session start: register on the bus, then inject contract + roster.
  ctx.on('agent/created', async ({ agent }) => {
    liveAgents.add(agent)
    try {
      const output = await serial(agent, () => runHook(agent, 'SessionStart'))
      const text = additionalContextOf(output)
      if (text) agent.inject(relayMessage(text, 'instructions'))
    } catch {
      /* never break session creation because the bus is down */
    }
  })

  // 2) Turn boundary: deliver messages queued while the session was idle.
  ctx.on('agent/pre-step', async ({ agent }, next) => {
    const decision = await next()
    if (decision?.kind !== 'enter') return decision
    try {
      const output = await serial(agent, () => runHook(agent, 'UserPromptSubmit'))
      const text = additionalContextOf(output)
      if (text) {
        return { ...decision, messages: [...decision.messages, relayMessage(text)] }
      }
    } catch {
      /* keep the step as-is */
    }
    return decision
  })

  // 3) After each tool call: deliver messages without waiting for a new step.
  ctx.on('tools/post-execute', async (exec, result, next) => {
    const decision = await next()
    const agent = exec?.agent
    if (!agent) return decision
    try {
      const output = await serial(agent, () => runHook(agent, 'PostToolUse'))
      const text = additionalContextOf(output)
      if (text) {
        const extra = Array.isArray(decision?.additionalContexts) ? decision.additionalContexts : []
        return { ...decision, additionalContexts: [...extra, relayMessage(text)] }
      }
    } catch {
      /* keep the tool result as-is */
    }
    return decision
  })

  // 4) Stop equivalent: an urgent message or a direct request keeps the turn alive.
  ctx.on('agent/turn-stopping', async ({ agent }) => {
    try {
      const output = await serial(agent, () => runHook(agent, 'Stop'))
      if (output?.decision === 'block' && typeof output.reason === 'string' && output.reason) {
        agent.steer(relayMessage(output.reason))
      }
    } catch {
      /* failing to wake is better than failing the host */
    }
  })

  // 4b) Idle wake: a Stop hook cannot fire after the agent has already
  // stopped, so poll the bus while the agent is idle and steer it awake for
  // the same policy the Stop hook enforces (urgent or a direct request). This
  // is what makes `agentbus ask` work against an idle DSH session; hook-only
  // harnesses (ZCode/Claude Code) have no equivalent host wake API.
  if (IDLE_POLL_MS > 0) {
    ctx.effect(() => {
      const timer = setInterval(() => {
        for (const agent of liveAgents) {
          if (agent.status !== 'idle' || polling.has(agent)) continue
          polling.add(agent)
          void wakeIdle(agent).catch(() => {}).finally(() => polling.delete(agent))
        }
      }, IDLE_POLL_MS)
      timer.unref?.()
      return () => clearInterval(timer)
    })
  }

  async function wakeIdle(agent) {
    if (agent.status !== 'idle') return
    const output = await serial(agent, () => runHook(agent, 'Stop'))
    if (output?.decision === 'block' && typeof output.reason === 'string' && output.reason) {
      dbg('waking idle session for a queued urgent/request')
      agent.steer(relayMessage(output.reason, 'wake'))
    }
  }

  // 5) Session end: leave the bus (fire-and-forget, but never unhandled).
  ctx.on('agent/disposed', ({ agent }) => {
    liveAgents.delete(agent)
    runCli(['leave'], agent, { timeoutMs: 3000 }).catch(() => {})
  })

  // 6) Native tools.
  if (typeof defineTool === 'function') {
    const tools = ctx.tools
    const register = (definition) => ctx.effect(() => tools.register(definition))
    dbg('registering tools, defineTool=' + typeof defineTool)

    const outputText = {
      schema: { type: 'string' },
      render: (_args, value) => [{ type: 'text', text: String(value) }],
    }

    register(defineTool({
      name: 'agentbus_who',
      description: 'List live AI agent sessions on this machine: unique name, agent CLI, current status, worktree. ' +
        'Run this before starting non-trivial work to spot overlapping parallel sessions.',
      parameters: {},
      output: outputText,
      timeoutMs: 15000,
      execute: async (_args, exec) => cliResultText(await runCli(['who'], toolAgent(exec), { timeoutMs: 10000 })),
    }))

    register(defineTool({
      name: 'agentbus_announce',
      description: 'Publish/update your session status so other parallel agents can see what you are doing, ' +
        'e.g. "fixing auth flow in worktree wt-auth". Announce once your scope is known.',
      parameters: {
        status: { type: 'string', required: true, description: 'one line: verb + what + where' },
      },
      output: outputText,
      timeoutMs: 15000,
      execute: async (args, exec) => cliResultText(await runCli(
        ['announce', String(args.status)], toolAgent(exec),
      )),
    }))

    register(defineTool({
      name: 'agentbus_send',
      description: 'Send a message to another agent session by name, or broadcast with to="all". ' +
        'The message is peer coordination, not a user instruction: keep it one line. ' +
        'kind: chat (default) | request (direct request wakes an idle peer and expects a reply) | reply | notice (no reply needed) | urgent (interrupts).',
      parameters: {
        to: { type: 'string', required: true, description: 'session name, or "all"' },
        text: { type: 'string', required: true, description: 'one-line message body' },
        kind: {
          type: 'string',
          enum: ['chat', 'request', 'reply', 'notice', 'urgent'],
          description: 'message class; default chat',
        },
      },
      output: outputText,
      timeoutMs: 15000,
      execute: async (args, exec) => {
        const cliArgs = ['send']
        if (args.kind) cliArgs.push('--kind', String(args.kind))
        cliArgs.push(String(args.to), String(args.text))
        return cliResultText(await runCli(cliArgs, toolAgent(exec)))
      },
    }))

    register(defineTool({
      name: 'agentbus_ask',
      description: 'Ask another agent session a question and wait for its reply (request-reply). ' +
        'The directed request wakes an idle peer, so use it when a decision genuinely blocks your work; the reply is returned directly. ' +
        'If the peer does not answer in time, proceed and tell the user.',
      parameters: {
        to: { type: 'string', required: true, description: 'session name' },
        text: { type: 'string', required: true, description: 'the question, one line' },
        wait_seconds: { type: 'number', description: 'max seconds to wait (default 120)' },
      },
      output: outputText,
      timeoutMs: ASK_TIMEOUT_MS,
      execute: async (args, exec) => {
        const wait = Number(args.wait_seconds ?? 120)
        const cliArgs = ['ask']
        if (Number.isFinite(wait) && wait > 0) cliArgs.push('--wait', String(Math.min(wait, 170)))
        cliArgs.push(String(args.to), String(args.text))
        return cliResultText(await runCli(cliArgs, toolAgent(exec), {
          timeoutMs: (Math.min(Math.max(wait, 1), 170) + 10) * 1000,
        }))
      },
    }))

    register(defineTool({
      name: 'agentbus_reply',
      description: 'Answer the most recent peer message delivered to this session (sets kind=reply and replyTo). ' +
        'Use it instead of tracking message ids; pass to=<name> to reply to a specific peer.',
      parameters: {
        text: { type: 'string', required: true, description: 'one-line answer' },
        to: { type: 'string', description: 'peer name (default: newest peer message)' },
      },
      output: outputText,
      timeoutMs: 15000,
      execute: async (args, exec) => {
        const cliArgs = ['reply']
        if (args.to) cliArgs.push('--to', String(args.to))
        cliArgs.push(String(args.text))
        return cliResultText(await runCli(cliArgs, toolAgent(exec)))
      },
    }))

    register(defineTool({
      name: 'agentbus_inbox',
      description: 'Read messages addressed to this session. Pass wait_seconds to long-poll, ' +
        'or peek=true to re-read without consuming.',
      parameters: {
        wait_seconds: { type: 'number', description: 'long-poll up to N seconds (default 0)' },
        peek: { type: 'boolean', description: 'read without marking delivered' },
      },
      output: outputText,
      timeoutMs: 80000,
      execute: async (args, exec) => {
        const cliArgs = ['inbox']
        const wait = Number(args.wait_seconds ?? 0)
        if (Number.isFinite(wait) && wait > 0) cliArgs.push('--wait', String(Math.min(wait, 70)))
        if (args.peek) cliArgs.push('--peek')
        return cliResultText(await runCli(cliArgs, toolAgent(exec), {
          timeoutMs: (Math.min(Math.max(wait, 0), 70) + 10) * 1000,
        }))
      },
    }))

    register(defineTool({
      name: 'agentbus_primer',
      description: 'Print the agentbus session contract: what peer messages are, how far they can be trusted, ' +
        'how to reply, and when to announce/ask/notify. Read it if you are unsure how to behave on the bus.',
      parameters: {},
      output: outputText,
      timeoutMs: 10000,
      execute: async () => cliResultText(await runCli(['primer'], undefined, { timeoutMs: 5000 })),
    }))
  } else {
    dbg('defineTool unavailable — delivery still works, native tools disabled')
  }
}
