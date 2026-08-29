<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useTerminalStore } from '../stores/terminal'
import { useWorkspaceStore } from '../stores/workspace'
import { useAcpStore } from '../stores/acp'
import TerminalView from './TerminalView.vue'
import BrowserPanel from './BrowserPanel.vue'
import AcpPanel from './AcpPanel.vue'
import AcpAgentSettings from './AcpAgentSettings.vue'
import SSHConnectDialog from './SSHConnectDialog.vue'

const props = defineProps<{ browserOpen: boolean }>()
const emit = defineEmits<{ (e: 'close-browser'): void }>()

const store = useTerminalStore()
const ws = useWorkspaceStore()
const acp = useAcpStore()
const showSSHDialog = ref(false)
const showCmdInput = ref(false)
const activeView = ref<'terminal' | 'browser' | 'acp'>('terminal')

watch(() => props.browserOpen, open => {
  if (open) activeView.value = 'browser'
  else if (activeView.value === 'browser') {
    activeView.value = acp.activeTabId ? 'acp' : 'terminal'
  }
})

watch(() => acp.activeTabId, id => {
  if (id && activeView.value === 'terminal' && store.tabs.length === 0 && !props.browserOpen) {
    activeView.value = 'acp'
  }
})

function selectTerminal(id: string) {
  activeView.value = 'terminal'
  store.setActive(id)
}

function selectBrowser() {
  activeView.value = 'browser'
}

function selectAcp(id: string) {
  activeView.value = 'acp'
  acp.setActive(id)
}

function closeBrowser() {
  emit('close-browser')
  activeView.value = acp.activeTabId ? 'acp' : 'terminal'
}

async function createAcp() {
  const tab = await acp.createSession()
  if (tab) activeView.value = 'acp'
}

async function closeAcp(id: string) {
  await acp.closeTab(id)
  if (!acp.tabs.length && activeView.value === 'acp') {
    activeView.value = props.browserOpen ? 'browser' : 'terminal'
  }
}

const gridCols = computed(() => {
  const n = store.tabs.length
  if (n <= 1) return 1
  if (n <= 4) return 2
  return 3
})

const hasAnyContent = computed(() => store.tabs.length > 0 || props.browserOpen || acp.tabs.length > 0)
</script>

<template>
  <div class="main-panel">
    <div class="tab-bar">
      <div
        v-for="tab in store.tabs"
        :key="tab.id"
        class="tab"
        :class="{ active: tab.id === store.activeTabId && activeView === 'terminal' && store.layoutMode === 'tabs' }"
        @click="selectTerminal(tab.id)"
      >
        <span class="tab-type">></span>
        <span>{{ tab.title }}</span>
        <button class="tab-close" @click.stop="store.closeTab(tab.id)">×</button>
      </div>
      <div
        v-if="browserOpen"
        class="tab browser-tab"
        :class="{ active: activeView === 'browser' }"
        @click="selectBrowser"
      >
        <span class="tab-type">◉</span>
        <span>浏览器</span>
        <button class="tab-close" @click.stop="closeBrowser">×</button>
      </div>
      <div
        v-for="tab in acp.tabs"
        :key="'acp-' + tab.id"
        class="tab acp-tab"
        :class="{ active: tab.id === acp.activeTabId && activeView === 'acp' }"
        @click="selectAcp(tab.id)"
      >
        <span class="tab-type">✦</span>
        <span>{{ tab.title }}</span>
        <button class="tab-close" @click.stop="closeAcp(tab.id)">×</button>
      </div>
      <button class="tab-new" title="新建终端" @click="ws.showStartupPicker = true">+</button>
      <button class="tab-acp" title="新建 ACP 客户端" @click="createAcp">✦</button>
      <button class="tab-ssh" title="SSH连接" @click="showSSHDialog = true">&#x1F50C;</button>
      <div class="tab-spacer"></div>
      <button
        class="btn-layout"
        :title="store.layoutMode === 'tabs' ? '切换到网格布局' : '切换到标签布局'"
        @click="store.toggleLayout()"
      >
        {{ store.layoutMode === 'tabs' ? '⊞' : '⊟' }}
      </button>
      <button
        class="btn-cmd-input"
        :class="{ active: showCmdInput }"
        :title="showCmdInput ? '隐藏命令输入栏' : '显示命令输入栏'"
        @click="showCmdInput = !showCmdInput"
      >
        &#x2328;
      </button>
      <button class="btn-acp-settings" title="ACP Agent 设置" @click="acp.showSettings = true">⚙</button>
    </div>

    <div v-if="!hasAnyContent" class="no-tabs">
      <p v-if="store.error || acp.error" class="error-msg">{{ store.error || acp.error }}</p>
      <p v-else>点击 + 创建终端，或 ✦ 创建 ACP 客户端</p>
    </div>

    <div v-if="browserOpen" v-show="activeView === 'browser'" class="tab-body">
      <BrowserPanel @close="closeBrowser" />
    </div>

    <div v-if="acp.tabs.length > 0 && activeView === 'acp'" class="tab-body">
      <div
        v-for="tab in acp.tabs"
        v-show="tab.id === acp.activeTabId"
        :key="'acp-body-' + tab.id"
        class="tab-content"
      >
        <AcpPanel :session-id="tab.id" />
      </div>
    </div>

    <div
      v-if="activeView === 'terminal' && store.tabs.length > 0 && store.layoutMode === 'grid'"
      class="grid-body"
      :style="{ gridTemplateColumns: `repeat(${gridCols}, 1fr)` }"
    >
      <div v-for="tab in store.tabs" :key="tab.id" class="grid-cell">
        <div class="grid-cell-header">
          <span class="grid-cell-title">{{ tab.title }}</span>
          <button class="grid-cell-close" @click="store.closeTab(tab.id)">×</button>
        </div>
        <div class="grid-cell-body">
          <TerminalView :tab-id="tab.id" :show-cmd-input="showCmdInput" />
        </div>
      </div>
    </div>

    <div v-if="activeView === 'terminal' && store.tabs.length > 0 && store.layoutMode === 'tabs'" class="tab-body">
      <div
        v-for="tab in store.tabs"
        v-show="tab.id === store.activeTabId"
        :key="tab.id"
        class="tab-content"
      >
        <TerminalView :tab-id="tab.id" :show-cmd-input="showCmdInput" />
      </div>
    </div>

    <SSHConnectDialog
      v-if="showSSHDialog"
      @close="showSSHDialog = false"
      @connected="(id, title) => { store.addSSHTab(id, title); showSSHDialog = false }"
    />
    <AcpAgentSettings v-if="acp.showSettings" @close="acp.showSettings = false" />
  </div>
</template>

<style scoped>
.main-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tab-bar {
  display: flex;
  align-items: center;
  background: var(--surface-bar);
  border-bottom: 1px solid #333;
  height: 32px;
  padding: 0 4px;
  gap: 2px;
  overflow-x: auto;
  flex-shrink: 0;
}
.tab {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 10px;
  border-radius: 4px 4px 0 0;
  cursor: pointer;
  font-size: 12px;
  color: #999;
  white-space: nowrap;
  user-select: none;
}
.tab.active { background: #161618; color: #fff; }
.tab:hover { background: #2a2a2e; }
.tab-type { font-size: 10px; flex-shrink: 0; }
.tab-close {
  background: none;
  border: none;
  color: #666;
  cursor: pointer;
  font-size: 14px;
  padding: 0;
  line-height: 1;
}
.tab-close:hover { color: #fff; }
.tab-new {
  background: none;
  border: none;
  color: #888;
  cursor: pointer;
  font-size: 16px;
  padding: 0 10px;
}
.tab-new:hover { color: #fff; }
.tab-ssh {
  background: none; border: none; color: #888; cursor: pointer;
  font-size: 12px; padding: 0 8px;
}
.tab-ssh:hover { color: #58a6ff; }
.tab-spacer { flex: 1; }
.btn-layout {
  background: none;
  border: 1px solid #444;
  color: #888;
  cursor: pointer;
  font-size: 14px;
  padding: 0 8px;
  border-radius: 3px;
  line-height: 22px;
}
.btn-layout:hover { color: #fff; border-color: #666; }
.btn-cmd-input {
  background: none;
  border: 1px solid #444;
  color: #888;
  cursor: pointer;
  font-size: 14px;
  padding: 0 8px;
  border-radius: 3px;
  line-height: 22px;
}
.btn-cmd-input:hover { color: #fff; border-color: #666; }
.btn-cmd-input.active { color: #58a6ff; border-color: #58a6ff; }
.tab-body {
  flex: 1;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  position: relative;
}
.no-tabs {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: #666;
  font-size: 14px;
  gap: 8px;
}
.error-msg { color: #f44336; font-size: 13px; }
.tab-content {
  flex: 1;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.tab-content > * {
  flex: 1;
  min-height: 0;
  height: 100%;
}
.grid-body {
  flex: 1;
  display: grid;
  gap: 2px;
  /* Transparent so multi-pane terminals can still show the app background. */
  background: transparent;
  overflow: hidden;
}
.grid-cell {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: transparent;
  border: 1px solid #2a2a2e;
}
.grid-cell-header {
  display: flex;
  align-items: center;
  padding: 2px 8px;
  background: #1e1e20;
  border-bottom: 1px solid #2a2a2e;
  height: 24px;
}
.grid-cell-title { flex: 1; font-size: 11px; color: #888; }
.grid-cell-close {
  background: none;
  border: none;
  color: #555;
  cursor: pointer;
  font-size: 14px;
  padding: 0 2px;
  line-height: 1;
}
.grid-cell-close:hover { color: #f44336; }
.grid-cell-body { flex: 1; overflow: hidden; }

.tab-acp {
  background: none; border: none; color: #888; cursor: pointer;
  font-size: 14px; padding: 0 8px;
}
.tab-acp:hover { color: #a371f7; }
.acp-tab.active { color: #d2a8ff; }
.btn-acp-settings {
  background: none; border: 1px solid #444; color: #888; cursor: pointer;
  font-size: 12px; padding: 0 8px; border-radius: 4px; margin-left: 4px;
}
.btn-acp-settings:hover { color: #fff; border-color: #666; }
</style>
