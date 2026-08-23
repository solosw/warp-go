<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useWorkspaceStore } from './stores/workspace'
import { useFileChangesStore } from './stores/fileChanges'
import { useTerminalStore } from './stores/terminal'
import { GetStartupWorkspace, CreateTerminal, WriteToTerminal, GetProjectRunCommands } from '../wailsjs/go/main/App'
import { config } from '../wailsjs/go/models'
import WorkspaceBar from './components/WorkspaceBar.vue'
import FileTreePanel from './components/FileTreePanel.vue'
import TerminalPanel from './components/TerminalPanel.vue'
import FilePreviewPanel from './components/FilePreviewPanel.vue'
import FileChangesPanel from './components/FileChangesPanel.vue'
import StartupCommandPicker from './components/StartupCommandPicker.vue'
import StartupCommandSettings from './components/StartupCommandSettings.vue'
import RunCommandPicker from './components/RunCommandPicker.vue'
import RunCommandSettings from './components/RunCommandSettings.vue'
import AIConfigSettings from './components/AIConfigSettings.vue'
import AppearanceSettings from './components/AppearanceSettings.vue'
import { useAppearanceStore } from './stores/appearance'
import { useAICompletionStore } from './stores/aiCompletion'
import { stopAllLSP } from './services/lsp'

const ws = useWorkspaceStore()
const term = useTerminalStore()
const showSettings = ref(false)
const showRunSettings = ref(false)
const showAISettings = ref(false)
const showAppearance = ref(false)
const showBrowser = ref(false)
const showRunPicker = ref(false)
const treeCollapsed = ref(false)
const changesCollapsed = ref(false)
const fc = useFileChangesStore()
const appearance = useAppearanceStore()
const aiCompletion = useAICompletionStore()

function escapeCdPath(p: string) {
  return p.replace(/"/g, '\\"')
}

function sleep(ms: number) {
  return new Promise(resolve => window.setTimeout(resolve, ms))
}

async function runCommandInNewTerminal(cmd: { name: string; command: string }) {
  const id = await CreateTerminal()
  if (!id) return
  term.addSSHTab(id, cmd.name || '运行')
  await sleep(350)
  await WriteToTerminal(id, `${cmd.command}\n`)
  if (ws.info && !ws.info.isRemote) {
    await WriteToTerminal(id, `cd "${escapeCdPath(ws.info.path)}"\n`)
  }
}

async function runCommandsInNewTerminal(cmds: Array<{ name: string; command: string }>) {
  if (!cmds.length) return
  for (const cmd of cmds) {
    if (!cmd.command?.trim()) continue
    await runCommandInNewTerminal(cmd)
    await sleep(120)
  }
}

async function onPickerSelect(cmd: config.StartupCommand) {
  ws.showStartupPicker = false
  await runCommandInNewTerminal(cmd)
}

async function onRunSelect(cmd: config.ProjectRunCommand) {
  showRunPicker.value = false
  await runCommandInNewTerminal(cmd)
}

async function onPickerDismiss() {
  ws.showStartupPicker = false
  const tab = await term.createTerminal()
  if (tab && ws.info && !ws.info.isRemote) {
    await sleep(350)
    await WriteToTerminal(tab.id, `cd "${escapeCdPath(ws.info.path)}"\n`)
  }
}

function onPickerSettings() {
  showSettings.value = true
  ws.showStartupPicker = false
}

function openRunPicker() {
  if (!ws.hasWorkspace) return
  showRunPicker.value = true
}

function openRunSettings() {
  if (!ws.hasWorkspace) return
  showRunSettings.value = true
  showRunPicker.value = false
}

async function runAllProjectCommands() {
  if (!ws.hasWorkspace) return
  const cmds = (await GetProjectRunCommands()) || []
  if (!cmds.length) {
    openRunSettings()
    return
  }
  showRunPicker.value = false
  await runCommandsInNewTerminal(cmds)
}

function onOpenAISettings() {
  showAISettings.value = true
}

// Resizable panel widths
const treeWidth = ref(220)
const changesWidth = ref(280)

function startResize(target: 'tree' | 'changes') {
  const onMove = (e: MouseEvent) => {
    if (target === 'tree') {
      treeWidth.value = Math.max(140, Math.min(400, e.clientX - 4))
    } else if (target === 'changes') {
      changesWidth.value = Math.max(180, Math.min(500, window.innerWidth - e.clientX - 4))
    }
  }
  const onUp = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onOpenAppearance() {
  showAppearance.value = true
}

function onOpenBrowser() {
  showBrowser.value = true
}

watch(() => ws.info?.path, (path, previousPath) => {
  if (previousPath && path !== previousPath) void stopAllLSP()
})

onMounted(async () => {
  appearance.load()
  aiCompletion.load()
  ws.loadHistory()
  fc.initListener()
  const startupWs = await GetStartupWorkspace()
  if (startupWs) {
    await ws.openWorkspace(startupWs)
  }
  await term.loadSnapshots()
})
</script>

<template>
  <div class="app-layout">
    <!-- Background image layer. Fixed, z-index 0, below content (z-index 1+).
         Must NOT use z-index: -1 — that paints under the solid body colour
         and the image never becomes visible. Image + opacity come from the
         appearance store as an inline style (not a CSS variable). -->
    <div
      class="app-background"
      aria-hidden="true"
      :style="appearance.backgroundLayerStyle"
    ></div>
    <WorkspaceBar
      @open-appearance="onOpenAppearance"
      @open-ai-settings="onOpenAISettings"
      @open-browser="onOpenBrowser"
      @run-project="runAllProjectCommands"
      @open-run-picker="openRunPicker"
      @open-run-settings="openRunSettings"
    />
    <div class="main-area">
      <template v-if="ws.hasWorkspace">
        <FileTreePanel v-if="!treeCollapsed" :style="{ width: treeWidth + 'px' }" />
        <button v-if="treeCollapsed" class="collapsed-panel-tab left" title="展开文件目录" @click="treeCollapsed = false">文件目录 ›</button>
        <div v-if="!treeCollapsed" class="resize-handle" @mousedown="startResize('tree')"></div>
        <button v-if="!treeCollapsed" class="panel-toggle left" title="隐藏文件目录" @click="treeCollapsed = true">‹</button>
      </template>
      <TerminalPanel :browser-open="showBrowser" @close-browser="showBrowser = false" />
      <template v-if="ws.hasWorkspace">
        <button v-if="!changesCollapsed" class="panel-toggle right" title="隐藏文件变更" @click="changesCollapsed = true">›</button>
        <div v-if="!changesCollapsed" class="resize-handle" @mousedown="startResize('changes')"></div>
        <FileChangesPanel v-if="!changesCollapsed" :style="{ width: changesWidth + 'px' }" />
        <button v-if="changesCollapsed" class="collapsed-panel-tab right" title="展开文件变更" @click="changesCollapsed = false">‹ 文件变更</button>
      </template>
    </div>
    <FilePreviewPanel v-if="ws.hasWorkspace && ws.previewFiles.length > 0" />
    <StartupCommandPicker
      v-if="ws.showStartupPicker"
      @select="onPickerSelect"
      @dismiss="onPickerDismiss"
      @settings="onPickerSettings"
    />
    <StartupCommandSettings
      v-if="showSettings"
      @close="showSettings = false"
    />
    <RunCommandPicker
      v-if="showRunPicker"
      @select="onRunSelect"
      @run-all="runAllProjectCommands"
      @dismiss="showRunPicker = false"
      @settings="openRunSettings"
    />
    <RunCommandSettings
      v-if="showRunSettings"
      @close="showRunSettings = false"
    />
    <AIConfigSettings
      v-if="showAISettings"
      @close="showAISettings = false"
    />
    <AppearanceSettings
      v-if="showAppearance"
      @close="showAppearance = false"
    />
  </div>
</template>

<style scoped>
.app-layout {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
  /* Transparent so the background layer can show through. The solid base
     colour lives on html/body; panels paint their own --surface-* colours. */
  background: transparent;
  z-index: 0;
}
.main-area {
  position: relative;
  z-index: 1;
  flex: 1;
  display: flex;
  overflow: hidden;
}
.resize-handle {
  width: 4px;
  cursor: col-resize;
  background: transparent;
  transition: background 0.15s;
  flex-shrink: 0;
  z-index: 10;
}
.resize-handle:hover {
  background: #58a6ff;
}
.panel-toggle { align-self: stretch; width: 18px; padding: 0; border: 0; background: var(--surface-bar); color: #8b949e; cursor: pointer; flex-shrink: 0; font-size: 16px; }
.panel-toggle:hover { background: #30363d; color: #58a6ff; }
.collapsed-panel-tab { align-self: stretch; width: 26px; padding: 8px 4px; border: 0; background: var(--surface-bar); color: #8b949e; cursor: pointer; flex-shrink: 0; font-size: 11px; line-height: 1.15; writing-mode: vertical-rl; }
.collapsed-panel-tab:hover { background: #30363d; color: #58a6ff; }
/* Background image layer. Fixed at z-index 0; content sits at z-index 1+.
   Image URL and opacity are set via :style from the appearance store —
   do not put large base64 data URLs into CSS custom properties. */
.app-background {
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  background-size: cover;
  background-position: center;
  background-repeat: no-repeat;
}
</style>
