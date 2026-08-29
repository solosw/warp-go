#!/usr/bin/env node
// Minimal ACP agent for protocol smoke tests (JSON-RPC over stdio).
const readline = require('readline')

let nextNotif = 1
const sessions = new Map()

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n')
}

function respond(id, result) {
  send({ jsonrpc: '2.0', id, result })
}

function error(id, message) {
  send({ jsonrpc: '2.0', id, error: { code: -32000, message } })
}

function extractPromptText(prompt) {
  if (!Array.isArray(prompt)) return String(prompt || '')
  return prompt.map((b) => {
    if (!b) return ''
    if (typeof b === 'string') return b
    if (b.type === 'text') return b.text || ''
    return b.text || ''
  }).join('')
}

function notifyUpdate(sessionId, update) {
  send({
    jsonrpc: '2.0',
    method: 'session/update',
    params: { sessionId, update },
  })
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
rl.on('line', async (line) => {
  line = line.trim()
  if (!line) return
  let msg
  try { msg = JSON.parse(line) } catch { return }
  const { id, method, params } = msg
  try {
    if (method === 'initialize') {
      respond(id, {
        protocolVersion: 1,
        agentCapabilities: { loadSession: false },
        agentInfo: { name: 'mock-agent', version: '0.0.1' },
      })
      return
    }
    if (method === 'session/new') {
      const sessionId = 's' + (sessions.size + 1)
      sessions.set(sessionId, {})
      respond(id, { sessionId })
      // Advertise slash commands after session is created.
      notifyUpdate(sessionId, {
        sessionUpdate: 'available_commands_update',
        availableCommands: [
          {
            name: 'echo',
            description: 'Echo remaining arguments',
            input: { hint: 'text to echo' },
          },
          {
            name: 'help',
            description: 'Show mock agent help',
          },
        ],
      })
      return
    }
    if (method === 'session/prompt') {
      const sessionId = params.sessionId
      if (!sessions.has(sessionId)) {
        error(id, 'unknown session')
        return
      }
      const promptText = extractPromptText(params.prompt)
      // Slash command handling for smoke tests.
      if (promptText.startsWith('/echo')) {
        const rest = promptText.slice(5).trim()
        notifyUpdate(sessionId, {
          sessionUpdate: 'agent_message_chunk',
          content: { type: 'text', text: rest || '(empty)' },
        })
        respond(id, { stopReason: 'end_turn', usage: { totalTokens: 12000, inputTokens: 9000, outputTokens: 3000 } })
        return
      }
      if (promptText.startsWith('/help')) {
        notifyUpdate(sessionId, {
          sessionUpdate: 'agent_message_chunk',
          content: { type: 'text', text: 'commands: /echo /help' },
        })
        respond(id, { stopReason: 'end_turn', usage: { totalTokens: 12000, inputTokens: 9000, outputTokens: 3000 } })
        return

      /* thought+usage */
      notifyUpdate(sessionId, {
        sessionUpdate: 'agent_thought_chunk',
        content: { type: 'text', text: 'thinking: consider options' },
      })
      notifyUpdate(sessionId, {
        sessionUpdate: 'agent_thought_chunk',
        content: { type: 'text', text: ' then decide' },
      })
      notifyUpdate(sessionId, {
        sessionUpdate: 'usage_update',
        used: 12000,
        size: 200000,
        cost: { amount: 0.02, currency: 'USD' },
      })

      }
      // Stream assistant chunks as notifications first.
      notifyUpdate(sessionId, {
        sessionUpdate: 'agent_message_chunk',
        content: { type: 'text', text: 'Hello ' },
      })
      notifyUpdate(sessionId, {
        sessionUpdate: 'agent_message_chunk',
        content: { type: 'text', text: 'from mock agent.' },
      })
      // Optional permission request as a client RPC.
      const permId = nextNotif++
      send({
        jsonrpc: '2.0',
        id: permId,
        method: 'session/request_permission',
        params: {
          sessionId,
          toolCall: {
            toolCallId: 'call_1',
            title: 'demo tool',
            kind: 'other',
            status: 'pending',
          },
          options: [
            { kind: 'allow_once', name: 'Allow', optionId: 'allow' },
            { kind: 'reject_once', name: 'Reject', optionId: 'reject' },
          ],
        },
      })
      // Wait briefly for permission response; ignore content.
      await new Promise((r) => setTimeout(r, 50))
      notifyUpdate(sessionId, {
        sessionUpdate: 'agent_message_chunk',
        content: { type: 'text', text: ' Done.' },
      })
      respond(id, { stopReason: 'end_turn', usage: { totalTokens: 12000, inputTokens: 9000, outputTokens: 3000 } })
      return
    }
    if (method === 'session/cancel') {
      respond(id, {})
      return
    }
    if (id !== undefined) error(id, 'Method not found: ' + method)
  } catch (e) {
    if (id !== undefined) error(id, String(e))
  }
})
