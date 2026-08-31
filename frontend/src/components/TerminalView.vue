<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { useTerminalStore } from '../stores/terminal'
import { useAppearanceStore } from '../stores/appearance'
import '@xterm/xterm/css/xterm.css'

const props = withDefaults(defineProps<{
  tabId: string
  showCmdInput?: boolean
  /** False while ACP/browser hides this terminal (parent uses v-show). */
  active?: boolean
}>(), {
  showCmdInput: false,
  active: true,
})

const store = useTerminalStore()
const appearance = useAppearanceStore()

/**
 * xterm paints its own canvas background, so it cannot inherit the panel
 * surface colour from CSS. Build the theme from the same alpha the panels use
 * (22,22,24 is --surface-app) and keep it in sync via the watcher below.
 */
function termTheme() {
  // Keep canvas alpha in sync with --panel-alpha. Parent DOM layers must also
  // be transparent (see styles below) or this rgba is painted over a solid fill.
  return {
    background: `rgba(22, 22, 24, ${appearance.panelOpacity})`,
    foreground: '#cccccc',
    cursor: '#ffffff',
    selectionBackground: 'rgba(68, 68, 68, 0.6)',
  }
}

function applyTermTheme() {
  if (!term) return
  // Assign a new object so xterm's option proxy notices the change and
  // repaints the background layer (in-place mutation is a no-op on some versions).
  term.options.theme = { ...termTheme() }
  term.refresh(0, Math.max(0, term.rows - 1))
}
const termEl = ref<HTMLDivElement>()
const isDragOver = ref(false)
const cmdInput = ref('')
const tab = computed(() => store.tabs.find(t => t.id === props.tabId) || null)
let term: Terminal | null = null
let fitAddon: FitAddon | null = null
let unsubscribe: (() => void) | null = null
let resizeObserver: ResizeObserver | null = null
let fitFrame: number | null = null

function scheduleFit() {
  if (fitFrame !== null) return
  fitFrame = requestAnimationFrame(() => {
    fitFrame = null
    const el = termEl.value
    if (!fitAddon || !el || el.offsetParent === null) return
    try { fitAddon.fit() } catch {}
  })
}

function observeTerminal(el: HTMLDivElement) {
  scheduleFit()
  resizeObserver = new ResizeObserver(scheduleFit)
  resizeObserver.observe(el)
}

onMounted(async () => {
  await nextTick()
  const el = termEl.value
  if (!el) return

  if (tab.value?.restored) {
    term = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      fontSize: 14,
      fontFamily: 'Consolas, "Courier New", monospace',
      theme: termTheme(),
      allowTransparency: true
    })
    fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(el)
    term.write('[2J[H')
    term.write(tab.value.output || '[无输出]')
    observeTerminal(el)
    return
  }

  term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Consolas, "Courier New", monospace',
    theme: termTheme(),
    allowTransparency: true,
    allowProposedApi: true
  })

  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(el)
  observeTerminal(el)

  // Replay any output buffered while this view was unmounted / not yet subscribed.
  const seed = tab.value?.output || ''
  if (seed) {
    term.write(seed)
  }

  unsubscribe = store.subscribeTerminal(props.tabId, (data: string) => {
    term?.write(data)
  })

  term.onData((data: string) => {
    store.writeToTerminal(props.tabId, data)
  })

  term.onResize(({ cols, rows }) => {
    store.resizeTerminal(props.tabId, cols, rows)
  })
})

// Repaint the xterm canvas when the user drags the panel-opacity slider.
watch(() => appearance.panelOpacity, () => {
  applyTermTheme()
})

// Parent keeps this instance mounted with v-show while ACP/browser is open.
// When shown again, layout size is valid again — refit and refresh the canvas.
watch(() => props.active, async (isActive, wasActive) => {
  if (!isActive || wasActive === true || !term || !fitAddon) return
  await nextTick()
  requestAnimationFrame(() => {
    const el = termEl.value
    if (!term || !fitAddon || !el || el.offsetParent === null) return
    try { fitAddon.fit() } catch {}
    term.refresh(0, Math.max(0, term.rows - 1))
  })
})

function sendCommand() {
  if (tab.value?.restored) return
  const text = cmdInput.value.trim()
  if (text) {
    store.writeToTerminal(props.tabId, text + '\n')
    cmdInput.value = ''
  }
}

function onDragOver(event: DragEvent) {
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
  isDragOver.value = true
}
function onDragLeave() { isDragOver.value = false }
function onDrop(event: DragEvent) {
  event.preventDefault()
  isDragOver.value = false
  if (tab.value?.restored) return
  const transfer = event.dataTransfer
  const path = transfer?.getData('application/x-aimuxterm-file-path') || transfer?.getData('text/plain') || ''
  if (path) store.writeToTerminal(props.tabId, quoteDroppedPath(path))
}

function quoteDroppedPath(path: string): string {
  const normalized = path.replace(/^file:\/\//, '').replace(/^\/?([A-Za-z]):\//, '$1:/').replace(/\\/g, '/')
  if (/\s|[()&;|<>]/.test(normalized)) return `"${normalized.replace(/"/g, '\\"')}"`
  return normalized
}

onUnmounted(() => {
  unsubscribe?.()
  resizeObserver?.disconnect()
  if (fitFrame !== null) cancelAnimationFrame(fitFrame)
  term?.dispose()
  term = null
  fitAddon = null
})
</script>

<template>
  <div v-if="tab?.restored" class="restored-terminal" :class="{ 'has-cmd-input': props.showCmdInput }">
    <div class="restore-banner">
      <span>上次终端会话已恢复，原进程已结束。</span>
      <button class="reconnect-btn" @click="store.reconnectTab(props.tabId)">重新连接</button>
    </div>
    <div ref="termEl" class="terminal-container has-cmd-input"></div>
    <div v-if="tab.error" class="restore-error">{{ tab.error }}</div>
  </div>
  <div
    v-else
    ref="termEl"
    class="terminal-container"
    :class="{ 'drag-over': isDragOver, 'has-cmd-input': props.showCmdInput }"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  ></div>
<div v-if="props.showCmdInput" class="cmd-input-bar">
  <textarea
    v-model="cmdInput"
    class="cmd-input"
    :placeholder="tab?.restored ? '恢复的终端需要先重新连接才能发送命令' : '输入命令，Ctrl+Enter 发送到终端...'"
    :disabled="tab?.restored"
    @keydown.ctrl.enter="sendCommand()"
    rows="8"
  ></textarea>
  <button class="cmd-send" :disabled="tab?.restored" @click="sendCommand()" title="Ctrl+Enter 发送">
    发送 &#x23CE;
  </button>
</div>
</template>

<style scoped>

.restored-terminal {
  width: 100%;
  height: 100%;
  /* Transparent: xterm canvas owns the dimmed fill via allowTransparency. */
  background: transparent;
  color: #c9d1d9;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.restored-terminal.has-cmd-input {
  height: calc(100% - 80px);
}
.restore-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: #1f2937;
  border-bottom: 1px solid #374151;
  color: #d1d5db;
  font-size: 12px;
}
.reconnect-btn {
  background: #238636;
  border: 1px solid #2ea043;
  color: #fff;
  border-radius: 4px;
  padding: 4px 12px;
  cursor: pointer;
  font-size: 12px;
}
.reconnect-btn:hover { background: #2ea043; }
.restore-error {
  color: #f85149;
  padding: 6px 10px;
  border-top: 1px solid #333;
  font-size: 12px;
}

.terminal-container {
  width: 100%;
  height: 100%;
  /* Must stay transparent so the xterm canvas alpha (allowTransparency)
     can reveal the app background image under the terminal. */
  background: transparent;
}
.terminal-container.drag-over {
  box-shadow: inset 0 0 0 2px #58a6ff;
}
/* xterm.css paints solid #000 on .xterm / .xterm-viewport by default, which
   completely hides allowTransparency. Force those DOM layers transparent so
   only the canvas theme background (with alpha) remains. */
.terminal-container :deep(.xterm),
.terminal-container :deep(.xterm-viewport),
.terminal-container :deep(.xterm-screen) {
  background-color: transparent !important;
  background: transparent !important;
}
.terminal-container.has-cmd-input {
  height: calc(100% - 80px);
}
.cmd-input-bar {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 6px 8px;
  background: #1a1a1c;
  border-top: 1px solid #333;
  flex-shrink: 0;
}
.cmd-input {
  width: 100%;
  background: #0d1117;
  border: 1px solid #30363d;
  color: #c9d1d9;
  padding: 6px 10px;
  border-radius: 4px;
  font-size: 13px;
  font-family: Consolas, 'Courier New', monospace;
  outline: none;
  resize: vertical;
  min-height: 48px;
}
.cmd-input:focus {
  border-color: #58a6ff;
}
.cmd-send {
  align-self: flex-end;
  background: #238636;
  border: 1px solid #2ea043;
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  padding: 4px 16px;
  border-radius: 4px;
}
.cmd-send:hover {
  background: #30363d;
  color: #58a6ff;
}
.cmd-input:disabled,
.cmd-send:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
