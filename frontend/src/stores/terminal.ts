import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { CreateTerminal, WriteToTerminal, ResizeTerminal, CloseTerminal, GetTerminalSnapshots, SaveTerminalSnapshots, ReconnectTerminal } from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { config } from '../../wailsjs/go/models'
import { useWorkspaceStore } from './workspace'

export type TerminalType = 'local' | 'ssh'

export interface TabItem {
  id: string
  title: string
  type: TerminalType
  cwd: string
  sshName?: string
  restored?: boolean
  output?: string
  error?: string
}

const MAX_OUTPUT = 200 * 1024
const OUTPUT_FLUSH_DELAY = 250
const OUTPUT_SAVE_DELAY = 5000

export const useTerminalStore = defineStore('terminal', () => {
  const ws = useWorkspaceStore()
  const tabs = ref<TabItem[]>([])
  const activeTabId = ref<string | null>(null)
  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) || null)
  const error = ref<string | null>(null)
  const layoutMode = ref<'tabs' | 'grid'>('tabs')
  const pendingOutputs = new Map<string, string>()
  let counter = 0
  let saveTimer: number | null = null
  let outputFlushTimer: number | null = null
  let outputSaveTimer: number | null = null
  let saveInProgress = false
  let saveRequested = false

  function trimOutput(text: string) {
    return text.length > MAX_OUTPUT ? text.slice(text.length - MAX_OUTPUT) : text
  }

  function scheduleSave() {
    if (saveTimer !== null) return
    saveTimer = window.setTimeout(() => {
      saveTimer = null
      persistSnapshots()
    }, 1000)
  }

  function scheduleOutputSave() {
    if (outputSaveTimer !== null) return
    outputSaveTimer = window.setTimeout(() => {
      outputSaveTimer = null
      persistSnapshots()
    }, OUTPUT_SAVE_DELAY)
  }

  function flushTerminalOutput(tabId: string) {
    const data = pendingOutputs.get(tabId)
    if (!data) return
    pendingOutputs.delete(tabId)
    const tab = tabs.value.find(t => t.id === tabId)
    if (!tab) return
    tab.output = trimOutput((tab.output || '') + data)
    scheduleOutputSave()
  }

  function flushPendingOutputs() {
    outputFlushTimer = null
    for (const id of pendingOutputs.keys()) flushTerminalOutput(id)
  }

  function queueOutput(tabId: string, data: string) {
    pendingOutputs.set(tabId, trimOutput((pendingOutputs.get(tabId) || '') + data))
    if (outputFlushTimer !== null) return
    outputFlushTimer = window.setTimeout(flushPendingOutputs, OUTPUT_FLUSH_DELAY)
  }

  async function persistSnapshots() {
    if (saveInProgress) {
      saveRequested = true
      return
    }
    saveInProgress = true
    try {
      do {
        saveRequested = false
        const snapshots = tabs.value.map(t => new config.TerminalSnapshot({
          id: t.id,
          title: t.title,
          type: t.type,
          workspace: ws.info?.path || (tabs.value[0]?.cwd || ''),
          cwd: t.cwd,
          sshName: t.sshName || '',
          output: t.output || '',
          restored: !!t.restored,
          active: t.id === activeTabId.value,
          updatedAt: new Date().toISOString()
        }))
        try { await SaveTerminalSnapshots(snapshots as any) } catch {}
      } while (saveRequested)
    } finally {
      saveInProgress = false
    }
  }

  async function loadSnapshots() {
    try {
      const snapshots = await GetTerminalSnapshots()
      tabs.value = (snapshots || []).map((s: any) => ({
        id: s.id,
        title: s.title || '恢复的终端',
        type: (s.type || 'local') as TerminalType,
        cwd: s.cwd || '',
        sshName: s.sshName || '',
        output: s.output || '',
        restored: true
      }))
      const active = (snapshots || []).find((s: any) => s.active)
      activeTabId.value = active?.id || tabs.value[0]?.id || null
      counter = tabs.value.length
    } catch (e: any) {
      error.value = '恢复终端失败: ' + (e?.message || e)
    }
  }

  async function createTerminal(): Promise<TabItem | null> {
    error.value = null
    try {
      const id = await CreateTerminal()
      if (!id) {
        error.value = '创建终端失败'
        return null
      }
      counter++
      const tab: TabItem = { id, title: `终端 ${counter}`, type: 'local', cwd: ws.info?.path || '', output: '' }
      tabs.value.push(tab)
      activeTabId.value = id
      scheduleSave()
      return tab
    } catch (e: any) {
      error.value = '创建终端失败: ' + (e?.message || e)
      return null
    }
  }

  async function reconnectTab(id: string) {
    const tab = tabs.value.find(t => t.id === id)
    if (!tab) return
    tab.error = ''
    pendingOutputs.delete(id)
    try {
      const snap = new config.TerminalSnapshot({
        id: tab.id,
        title: tab.title,
        type: tab.sshName ? 'ssh' : 'local',
        workspace: ws.info?.path || (tabs.value[0]?.cwd || ''),
        cwd: tab.cwd,
        sshName: tab.sshName || '',
        output: tab.output || '',
        restored: true,
        active: tab.id === activeTabId.value,
        updatedAt: new Date().toISOString()
      })
      const newId = await ReconnectTerminal(snap as any)
      EventsOff('terminal-output:' + tab.id)
      tab.id = newId
      tab.restored = false
      tab.output = ''
      tab.error = ''
      activeTabId.value = newId
      scheduleSave()
    } catch (e: any) {
      tab.error = '重新连接失败: ' + (e?.message || e)
    }
  }

  async function closeTab(id: string) {
    flushTerminalOutput(id)
    const tab = tabs.value.find(t => t.id === id)
    if (tab && !tab.restored) {
      try { await CloseTerminal(id) } catch {}
      EventsOff('terminal-output:' + id)
    }
    pendingOutputs.delete(id)
    const idx = tabs.value.findIndex(t => t.id === id)
    if (idx !== -1) tabs.value.splice(idx, 1)
    if (activeTabId.value === id) {
      activeTabId.value = tabs.value.length > 0 ? tabs.value[tabs.value.length - 1].id : null
    }
    persistSnapshots()
  }

  function setActive(id: string) {
    activeTabId.value = id
    scheduleSave()
  }

  function addSSHTab(id: string, title: string) {
    tabs.value.push({ id, title, type: 'ssh', cwd: ws.info?.isRemote ? (ws.info?.path || '') : '', sshName: title, output: '' })
    activeTabId.value = id
    scheduleSave()
  }

  function appendOutput(tabId: string, data: string) {
    if (!tabs.value.some(t => t.id === tabId)) return
    queueOutput(tabId, data)
  }

  function writeToTerminal(tabId: string, data: string) { WriteToTerminal(tabId, data) }
  function resizeTerminal(tabId: string, cols: number, rows: number) { ResizeTerminal(tabId, cols, rows) }

  function subscribeTerminal(id: string, handler: (data: string) => void): () => void {
    const eventName = 'terminal-output:' + id
    return EventsOn(eventName, (data: string) => {
      appendOutput(id, data)
      handler(data)
    })
  }

  function toggleLayout() {
    layoutMode.value = layoutMode.value === 'tabs' ? 'grid' : 'tabs'
    scheduleSave()
  }

  return {
    tabs, activeTabId, activeTab, error,
    layoutMode, toggleLayout,
    loadSnapshots, reconnectTab,
    createTerminal, addSSHTab, closeTab, setActive,
    writeToTerminal, resizeTerminal, subscribeTerminal
  }
})
