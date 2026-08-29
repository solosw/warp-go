import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  CancelAcpPrompt,
  CloseAcpSession,
  CreateAcpSession,
  GetAcpAgents,
  RespondAcpPermission,
  SaveAcpAgents,
  SendAcpPrompt,
  SetAcpSessionMode,
} from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { config } from '../../wailsjs/go/models'

export type AcpRole = 'user' | 'assistant' | 'system'

export interface AcpMessage {
  id: string
  kind: 'message'
  role: AcpRole
  content: string
  streaming?: boolean
}

export interface AcpThoughtItem {
  id: string
  kind: 'thought'
  content: string
  streaming?: boolean
  expanded?: boolean
}

export interface AcpToolItem {
  id: string
  kind: 'tool'
  toolCallId: string
  name: string
  toolKind?: string
  status: string
  input?: string
  output?: string
  expanded?: boolean
}

export interface AcpPlanTodo {
  content: string
  status: string
  priority?: string
}

export interface AcpPlanItem {
  id: string
  kind: 'plan'
  entries: AcpPlanTodo[]
  expanded?: boolean
}

export type AcpTimelineItem = AcpMessage | AcpToolItem | AcpPlanItem | AcpThoughtItem

export interface AcpPermissionOption {
  optionId: string
  name: string
  kind?: string
}

export interface AcpPermissionReq {
  requestId: string
  toolName: string
  toolInput: string
  prompt: string
  options: AcpPermissionOption[]
}

export interface AcpCommand {
  name: string
  description?: string
  inputHint?: string
}

export interface AcpSessionMode {
  id: string
  name?: string
  description?: string
}

export interface AcpTab {
  id: string
  title: string
  mode: 'local' | 'remote' | string
  agent: string
  cwd: string
  status: string
  error?: string
  items: AcpTimelineItem[]
  permission?: AcpPermissionReq | null
  commands: AcpCommand[]
  modes: AcpSessionMode[]
  currentModeId: string
  usage?: {
    used: number
    size: number
    cost?: number
    currency?: string
  } | null
}

let msgCounter = 0
function nextId(prefix = 'm') {
  msgCounter += 1
  return `${prefix}-${Date.now()}-${msgCounter}`
}

function unwrapEvent(ev: any): any {
  if (ev == null) return ev
  if (Array.isArray(ev)) {
    if (ev.length === 1) return unwrapEvent(ev[0])
    return ev
  }
  if (typeof ev === 'object') {
    if (ev.data && typeof ev.data === 'object' && (ev.data.type || ev.data.Type)) return unwrapEvent(ev.data)
    if (ev.payload && typeof ev.payload === 'object') return unwrapEvent(ev.payload)
  }
  return ev
}

function readField(ev: any, ...keys: string[]) {
  for (const k of keys) {
    if (ev && ev[k] !== undefined && ev[k] !== null && ev[k] !== '') return ev[k]
  }
  return ''
}

function normalizeCommands(raw: any): AcpCommand[] {
  if (!raw) return []
  if (typeof raw === 'string') {
    try { raw = JSON.parse(raw) } catch { return [] }
  }
  if (!Array.isArray(raw)) return []
  return raw.map((c: any) => ({
    name: String(c?.name ?? c?.Name ?? '').trim().replace(/^\//, ''),
    description: String(c?.description ?? c?.Description ?? ''),
    inputHint: String(c?.inputHint ?? c?.InputHint ?? c?.input?.hint ?? ''),
  })).filter((c: AcpCommand) => !!c.name)
}

function normalizeOptions(raw: any): AcpPermissionOption[] {
  if (!raw) return []
  if (typeof raw === 'string') {
    try { raw = JSON.parse(raw) } catch { return [] }
  }
  if (!Array.isArray(raw)) return []
  return raw.map((o: any) => ({
    optionId: String(o?.optionId ?? o?.OptionID ?? o?.optionID ?? o?.id ?? '').trim(),
    name: String(o?.name ?? o?.Name ?? o?.optionId ?? o?.OptionID ?? '').trim(),
    kind: String(o?.kind ?? o?.Kind ?? ''),
  })).filter((o: AcpPermissionOption) => !!o.optionId)
}

function normalizePlanEntries(raw: any): AcpPlanTodo[] {
  if (!raw) return []
  if (typeof raw === 'string') {
    try { raw = JSON.parse(raw) } catch { return [] }
  }
  if (!Array.isArray(raw)) return []
  return raw.map((e: any) => ({
    content: String(e?.content ?? e?.Content ?? '').trim(),
    status: String(e?.status ?? e?.Status ?? 'pending').trim() || 'pending',
    priority: String(e?.priority ?? e?.Priority ?? '').trim() || undefined,
  })).filter((e: AcpPlanTodo) => !!e.content)
}

function normalizeModes(raw: any): AcpSessionMode[] {
  if (!raw) return []
  if (typeof raw === 'string') {
    try { raw = JSON.parse(raw) } catch { return [] }
  }
  if (!Array.isArray(raw)) return []
  return raw.map((m: any) => ({
    id: String(m?.id ?? m?.ID ?? '').trim(),
    name: String(m?.name ?? m?.Name ?? '').trim() || undefined,
    description: String(m?.description ?? m?.Description ?? '').trim() || undefined,
  })).filter((m: AcpSessionMode) => !!m.id)
}

function isRejectish(o: AcpPermissionOption) {
  const k = (o.kind || '').toLowerCase()
  const id = o.optionId.toLowerCase()
  const n = o.name.toLowerCase()
  return k.includes('reject') || k.includes('deny') || id === 'reject' || id === 'deny' || n.includes('reject') || n.includes('拒绝') || n.includes('cancel')
}

function isAllowish(o: AcpPermissionOption) {
  const k = (o.kind || '').toLowerCase()
  const id = o.optionId.toLowerCase()
  const n = o.name.toLowerCase()
  return k.includes('allow') || id === 'allow' || n.includes('allow') || n.includes('允许')
}

export const useAcpStore = defineStore('acp', () => {
  const tabs = ref<AcpTab[]>([])
  const activeTabId = ref<string | null>(null)
  const agents = ref<config.AcpAgentConfig[]>([])
  const error = ref<string | null>(null)
  const showSettings = ref(false)
  let counter = 0
  const unsubscribers = new Map<string, () => void>()

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) || null)

  async function loadAgents() {
    try {
      agents.value = (await GetAcpAgents()) || []
    } catch (e: any) {
      error.value = '加载 ACP Agent 失败: ' + (e?.message || e)
    }
  }

  async function saveAgents(next: config.AcpAgentConfig[]) {
    agents.value = next
    await SaveAcpAgents(next as any)
  }

  function endStreaming(tab: AcpTab) {
    for (const it of tab.items) {
      if ((it.kind === 'message' || it.kind === 'thought') && it.streaming) {
        it.streaming = false
      }
    }
  }

  function pushMessage(tab: AcpTab, role: AcpRole, content: string, streaming = false) {
    if (!content) return
    if (role === 'assistant') {
      const last = tab.items[tab.items.length - 1]
      if (last && last.kind === 'message' && last.role === 'assistant' && last.streaming) {
        last.content += content
        return
      }
      // Thought chunks finish before the answer; close any open thought bubble.
      for (const it of tab.items) {
        if (it.kind === 'thought' && it.streaming) it.streaming = false
      }
      tab.items.push({
        id: nextId('a'),
        kind: 'message',
        role: 'assistant',
        content,
        streaming: true,
      })
      return
    }
    tab.items.push({
      id: nextId(role === 'user' ? 'u' : 's'),
      kind: 'message',
      role,
      content,
      streaming,
    })
  }

  function pushThought(tab: AcpTab, content: string) {
    if (!content) return
    const last = tab.items[tab.items.length - 1]
    if (last && last.kind === 'thought' && last.streaming) {
      last.content += content
      return
    }
    // Close any open assistant stream so following answer starts fresh.
    endStreaming(tab)
    tab.items.push({
      id: nextId('th'),
      kind: 'thought',
      content,
      streaming: true,
      expanded: true,
    })
  }

  function applyUsage(tab: AcpTab, ev: any) {
    const used = Number(readField(ev, 'usageUsed', 'UsageUsed', 'used', 'Used') || 0)
    const size = Number(readField(ev, 'usageSize', 'UsageSize', 'size', 'Size') || 0)
    const costRaw = readField(ev, 'usageCost', 'UsageCost', 'cost', 'Cost')
    const currency = String(readField(ev, 'usageCurrency', 'UsageCurrency', 'currency', 'Currency') || '')
    const prev = tab.usage || { used: 0, size: 0 }
    const costNum = costRaw === '' || costRaw == null ? prev.cost : Number(costRaw)
    tab.usage = {
      used: used > 0 ? used : prev.used,
      size: size > 0 ? size : prev.size,
      cost: typeof costNum === 'number' && !Number.isNaN(costNum) ? costNum : prev.cost,
      currency: currency || prev.currency,
    }
  }

    function upsertPlan(tab: AcpTab, entries: AcpPlanTodo[]) {
    // Keep a single live plan card: update the latest plan item in-place.
    for (let i = tab.items.length - 1; i >= 0; i--) {
      const it = tab.items[i]
      if (it.kind === 'plan') {
        tab.items[i] = {
          ...it,
          entries: entries.slice(),
          expanded: it.expanded !== false,
        }
        return
      }
    }
    tab.items.push({
      id: nextId('plan'),
      kind: 'plan',
      entries: entries.slice(),
      expanded: true,
    })
  }

  function upsertTool(tab: AcpTab, ev: any) {
    const toolCallId = String(readField(ev, 'toolCallId', 'ToolCallID', 'toolCallID') || nextId('tc'))
    const eventName = String(readField(ev, 'toolName', 'ToolName', 'name', 'Name') || '')
    const status = String(readField(ev, 'status', 'Status') || 'pending')
    const toolKind = String(readField(ev, 'toolKind', 'ToolKind') || '')
    const input = String(readField(ev, 'toolInput', 'ToolInput') || '')
    const output = String(readField(ev, 'content', 'Content') || '')

    const existing = tab.items.find(
      (it): it is AcpToolItem => it.kind === 'tool' && it.toolCallId === toolCallId,
    )
    if (existing) {
      if (eventName) existing.name = eventName
      if (toolKind) existing.toolKind = toolKind
      if (status) existing.status = status
      if (input) existing.input = input
      if (output) existing.output = output
      return
    }
    tab.items.push({
      id: nextId('t'),
      kind: 'tool',
      toolCallId,
      name: eventName || toolKind || 'tool',
      toolKind,
      status: status || 'pending',
      input: input || undefined,
      output: output || undefined,
      expanded: false,
    })
  }

  function subscribe(id: string) {
    if (unsubscribers.has(id)) return
    const off = EventsOn('acp-event:' + id, (raw: any) => {
      const ev = unwrapEvent(raw)
      const tab = tabs.value.find(t => t.id === id)
      if (!tab) return
      const type = String(readField(ev, 'type', 'Type'))
      if (type === 'status') {
        tab.status = String(readField(ev, 'status', 'Status') || tab.status)
        if (['ready', 'error', 'exited', 'closed'].includes(tab.status)) endStreaming(tab)
        return
      }
      if (type === 'error') {
        tab.error = String(readField(ev, 'content', 'Content') || 'ACP 错误')
        tab.status = 'error'
        endStreaming(tab)
        return
      }
      if (type === 'permission') {
        const options = normalizeOptions(ev?.options ?? ev?.Options)
        const prompt = String(readField(ev, 'content', 'Content') || '')
        const toolInput = String(readField(ev, 'toolInput', 'ToolInput') || '')
        // Never dump raw JSON into permission card body.
        let body = prompt
        if (!body && toolInput && !toolInput.trim().startsWith('{') && !toolInput.trim().startsWith('[')) {
          body = toolInput
        }
        tab.permission = {
          requestId: String(readField(ev, 'requestId', 'RequestID') || ''),
          toolName: String(readField(ev, 'toolName', 'ToolName') || 'permission'),
          toolInput,
          prompt: body || '请选择一项',
          options,
        }
        return
      }
      if (type === 'commands') {
        tab.commands = normalizeCommands(ev?.commands ?? ev?.Commands)
        return
      }
      if (type === 'plan') {
        const entries = normalizePlanEntries(ev?.planEntries ?? ev?.PlanEntries ?? ev?.entries ?? ev?.Entries)
        upsertPlan(tab, entries)
        return
      }
      if (type === 'mode') {
        const modes = normalizeModes(ev?.modes ?? ev?.Modes)
        if (modes.length) tab.modes = modes
        const cur = String(readField(ev, 'currentModeId', 'CurrentModeID', 'currentModeID') || '')
        if (cur) tab.currentModeId = cur
        return
      }
      if (type === 'tool') {
        endStreaming(tab)
        upsertTool(tab, ev)
        return
      }
      if (type === 'message') {
        const role = (readField(ev, 'role', 'Role') || 'assistant') as AcpRole
        const content = String(readField(ev, 'content', 'Content') || '')
        if (!content) return
        if (role === 'user') {
          // Backend also emits user message on Prompt; avoid double if already last.
          const last = tab.items[tab.items.length - 1]
          if (last && last.kind === 'message' && last.role === 'user' && last.content === content) return
        }
        pushMessage(tab, role, content, role === 'assistant')
        return
      }
      if (type === 'thought') {
        const content = String(readField(ev, 'content', 'Content') || '')
        pushThought(tab, content)
        return
      }
      if (type === 'usage') {
        applyUsage(tab, ev)
        return
      }
    })
    unsubscribers.set(id, off)
  }

  async function createSession(agentId?: string) {
    error.value = null
    try {
      if (!agents.value.length) await loadAgents()
      if (!agents.value.length) {
        error.value = '请先配置 ACP Agent'
        return null
      }
      const info = await CreateAcpSession(agentId || '')
      if (!info?.id) {
        error.value = '创建 ACP 会话失败'
        return null
      }
      counter += 1
      const tab: AcpTab = {
        id: info.id,
        title: info.title ? `${info.title} ${counter}` : `ACP ${counter}`,
        mode: info.mode || 'local',
        agent: info.agent || '',
        cwd: info.cwd || '',
        status: info.status || 'starting',
        items: [],
        permission: null,
        commands: [],
        modes: [],
        currentModeId: '',
        usage: null,
      }
      subscribe(tab.id)
      tabs.value.push(tab)
      activeTabId.value = tab.id
      if (info.status) tab.status = info.status
      return tab
    } catch (e: any) {
      error.value = '创建 ACP 失败: ' + (e?.message || e)
      return null
    }
  }

  async function closeTab(id: string) {
    const off = unsubscribers.get(id)
    if (off) {
      try { off() } catch {}
      unsubscribers.delete(id)
    }
    try { await CloseAcpSession(id) } catch {}
    tabs.value = tabs.value.filter(t => t.id !== id)
    if (activeTabId.value === id) {
      activeTabId.value = tabs.value.length ? tabs.value[tabs.value.length - 1].id : null
    }
  }

  function setActive(id: string) {
    if (tabs.value.some(t => t.id === id)) activeTabId.value = id
  }

  async function sendPrompt(id: string, text: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    const trimmed = text.trim()
    if (!trimmed) return
    tab.error = undefined
    // Optimistic user bubble; backend may also emit — de-duped in subscribe.
    pushMessage(tab, 'user', trimmed)
    tab.status = 'running'
    try {
      await SendAcpPrompt(id, trimmed)
    } catch (e: any) {
      tab.error = String(e?.message || e)
      tab.status = 'error'
      endStreaming(tab)
    }
  }

  async function cancelPrompt(id: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    try {
      await CancelAcpPrompt(id)
    } catch (e: any) {
      tab.error = String(e?.message || e)
    }
  }

  async function setSessionMode(id: string, modeId: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    const mid = (modeId || '').trim()
    if (!mid || mid === tab.currentModeId) return
    const prev = tab.currentModeId
    tab.currentModeId = mid // optimistic
    try {
      await SetAcpSessionMode(id, mid)
    } catch (e: any) {
      tab.currentModeId = prev
      tab.error = String(e?.message || e)
    }
  }

  async function respondPermission(id: string, requestId: string, optionId: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (tab) tab.permission = null
    try {
      await RespondAcpPermission(id, requestId, optionId)
    } catch (e: any) {
      if (tab) tab.error = String(e?.message || e)
    }
  }

  async function respondPermissionAllow(id: string, requestId: string, allow: boolean) {
    const tab = tabs.value.find(t => t.id === id)
    const opts = tab?.permission?.options || []
    let optionId = ''
    if (allow) {
      const a = opts.find(isAllowish)
      optionId = a?.optionId || 'allow'
    } else {
      const rej = opts.find(isRejectish)
      optionId = rej?.optionId || 'reject'
    }
    await respondPermission(id, requestId, optionId)
  }

  function toggleTool(id: string, itemId: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    const idx = tab.items.findIndex(x => x.id === itemId)
    if (idx < 0) return
    const it = tab.items[idx]
    if (it.kind !== 'tool') return
    // Replace object so Vue/Pinia watchers always see the change.
    tab.items[idx] = { ...it, expanded: !it.expanded }
  }

  function toggleThought(id: string, itemId: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    const idx = tab.items.findIndex(x => x.id === itemId)
    if (idx < 0) return
    const it = tab.items[idx]
    if (it.kind !== 'thought') return
    tab.items[idx] = { ...it, expanded: it.expanded === false ? true : false }
  }

  function togglePlan(id: string, itemId: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    const idx = tab.items.findIndex(x => x.id === itemId)
    if (idx < 0) return
    const it = tab.items[idx]
    if (it.kind !== 'plan') return
    tab.items[idx] = { ...it, expanded: !it.expanded }
  }

  return {
    tabs, activeTabId, activeTab, agents, error, showSettings,
    loadAgents, saveAgents, createSession, closeTab, setActive,
    sendPrompt, cancelPrompt, setSessionMode,
    respondPermission, respondPermissionAllow, toggleTool, toggleThought, togglePlan,
  }
})
