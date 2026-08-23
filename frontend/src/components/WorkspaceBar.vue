<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useWorkspaceStore } from '../stores/workspace'
import RemoteWorkspaceDialog from './RemoteWorkspaceDialog.vue'

const emit = defineEmits<{
  (e: 'open-appearance'): void
  (e: 'open-ai-settings'): void
  (e: 'open-browser'): void
  (e: 'run-project'): void
  (e: 'open-run-picker'): void
  (e: 'open-run-settings'): void
}>()

const ws = useWorkspaceStore()
const showDropdown = ref(false)
const showRemoteDialog = ref(false)
const dropdownRef = ref<HTMLElement>()

function toggleDropdown() {
  showDropdown.value = !showDropdown.value
}

function closeDropdown() {
  showDropdown.value = false
}

function onDocClick(e: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(e.target as Node)) {
    closeDropdown()
  }
}

onMounted(() => document.addEventListener('click', onDocClick))
onUnmounted(() => document.removeEventListener('click', onDocClick))

async function selectFromHistory(path: string) {
  closeDropdown()
  if (ws.info?.path === path) return
  if (ws.hasWorkspace) {
    await ws.openInNewWindow(path)
  } else {
    await ws.openWorkspace(path)
  }
}

async function openRemote(entry: any) {
  closeDropdown()
  await ws.openRemoteWorkspace(entry)
}

async function removeRemote(name: string, e: Event) {
  e.stopPropagation()
  await ws.removeRemote(name)
}

function infoMatchesRemote(entry: any) {
  return ws.info?.path?.includes(entry.name + ':')
}

async function removeHistory(path: string, e: Event) {
  e.stopPropagation()
  await ws.removeFromHistory(path)
}
</script>

<template>
  <div class="workspace-bar">
    <div ref="dropdownRef" class="dropdown-wrapper">
      <button class="btn-select" @click="toggleDropdown">
        <span class="icon">&#x1F4C1;</span>
        <span>{{ ws.hasWorkspace ? ws.info?.name : '选择工作区...' }}</span>
        <span class="arrow">&#x25BE;</span>
      </button>

      <div v-if="showDropdown" class="dropdown-menu">
        <div class="dropdown-header">本地工作区</div>
        <div v-if="ws.history.length === 0" class="dropdown-empty">暂无</div>
        <div v-for="entry in ws.history" :key="'l'+entry.path" class="dropdown-item"
          :class="{ active: !ws.info?.isRemote && ws.info?.path === entry.path }"
          @click="selectFromHistory(entry.path)">
          <span class="item-name">&#x1F4C1; {{ entry.name }}</span>
          <span class="item-path">{{ entry.path }}</span>
          <button class="item-remove" @click="(e: Event) => removeHistory(entry.path, e)" title="移除">&times;</button>
        </div>

        <div class="dropdown-header" style="margin-top:4px">远程工作区</div>
        <div v-if="ws.remoteList.length === 0" class="dropdown-empty">暂无</div>
        <div v-for="entry in ws.remoteList" :key="'r'+entry.name" class="dropdown-item"
          :class="{ active: ws.info?.isRemote && infoMatchesRemote(entry) }"
          @click="openRemote(entry)">
          <span class="item-name">&#x1F310; {{ entry.name }}</span>
          <span class="item-path">{{ entry.user }}@{{ entry.host }}:{{ entry.remotePath }}</span>
          <button class="item-remove" @click="(e: Event) => removeRemote(entry.name, e)" title="移除">&times;</button>
        </div>

        <div class="dropdown-footer" @click="showRemoteDialog = true; closeDropdown()">
          + 添加远程服务器...
        </div>
        <div class="dropdown-footer" @click="ws.selectWorkspace(); closeDropdown()">
          + 浏览本地文件夹...
        </div>
      </div>
    </div>

    <div v-if="ws.hasWorkspace" class="workspace-info">
      <span v-if="ws.info?.isRemote" class="remote-badge">&#x1F310; 远程</span>
      <button v-if="ws.info?.isRemote" class="btn-refresh" @click="ws.refreshRemote()" title="刷新远程工作区">&#x21BB;</button>
      <span class="file-count">{{ ws.info?.fileCount }} 个文件</span>
    </div>

    <div class="bar-spacer"></div>

    <div class="bar-actions">
      <div v-if="ws.hasWorkspace" class="run-group">
        <button class="btn-run" title="按顺序运行全部命令，每条命令单独打开终端" @click="emit('run-project')">▶ 运行</button>
        <button class="btn-run-more" title="查看/选择运行命令" @click="emit('open-run-picker')">▾</button>
        <button class="btn-run-cfg" title="配置本项目运行命令" @click="emit('open-run-settings')">⚙</button>
      </div>
      <button class="btn-bar" @click="emit('open-browser')" title="打开内置浏览器">浏览器</button>
      <button class="btn-bar" @click="emit('open-appearance')" title="外观设置">外观</button>
      <button class="btn-bar btn-bar-primary" @click="emit('open-ai-settings')" title="AI 配置">AI 配置</button>
    </div>

    <RemoteWorkspaceDialog v-if="showRemoteDialog" @close="showRemoteDialog = false" />
  </div>
</template>

<style scoped>
.workspace-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 12px;
  background: var(--surface-bar);
  border-bottom: 1px solid #333;
  height: 36px;
  position: relative;
  /* Must sit above .main-area (z-index 1). The dropdown is position:absolute
     inside this bar; if the bar's stacking context is ≤ main-area, the whole
     menu (including workspace picker clicks) is covered and unusable. */
  z-index: 50;
  flex-shrink: 0;
}
.dropdown-wrapper {
  position: relative;
}
.btn-select {
  display: flex;
  align-items: center;
  gap: 6px;
  background: #2a2a2e;
  border: 1px solid #444;
  color: #ccc;
  padding: 4px 12px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  max-width: 320px;
  overflow: hidden;
  white-space: nowrap;
}
.btn-select:hover {
  background: #3a3a3e;
}
.arrow {
  font-size: 10px;
  color: #888;
}
.dropdown-menu {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 4px;
  width: 420px;
  max-height: 360px;
  overflow-y: auto;
  background: #1e1e20;
  border: 1px solid #444;
  border-radius: 6px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.5);
  z-index: 100;
}
.dropdown-header {
  padding: 8px 14px;
  font-size: 11px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  border-bottom: 1px solid #333;
}
.dropdown-empty {
  padding: 16px;
  text-align: center;
  color: #555;
  font-size: 13px;
}
.dropdown-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  cursor: pointer;
  border-bottom: 1px solid #222;
  font-size: 13px;
}
.dropdown-item:hover {
  background: #2a2a2e;
}
.dropdown-item.active {
  background: #1a3a5c;
}
.item-name {
  font-weight: 600;
  color: #ddd;
  flex-shrink: 0;
}
.item-path {
  color: #888;
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.item-remove {
  background: none;
  border: none;
  color: #555;
  cursor: pointer;
  font-size: 16px;
  padding: 0 2px;
  line-height: 1;
  flex-shrink: 0;
}
.item-remove:hover {
  color: #f44336;
}
.dropdown-footer {
  padding: 8px 14px;
  font-size: 13px;
  color: #4a9eff;
  cursor: pointer;
  border-top: 1px solid #333;
}
.dropdown-footer:hover {
  background: #2a2a2e;
}
.workspace-info {
  font-size: 12px;
  color: #888;
}
.file-count {
  background: #333;
  padding: 2px 8px;
  border-radius: 10px;
}
.remote-badge {
  color: #58a6ff;
  font-size: 11px;
  margin-right: 4px;
}
.btn-refresh {
  background: none;
  border: 1px solid #444;
  color: #58a6ff;
  cursor: pointer;
  font-size: 14px;
  padding: 0 6px;
  border-radius: 3px;
  margin-right: 6px;
  line-height: 20px;
}
.btn-refresh:hover { background: #1a3a5c; }
/* Pushes the action buttons to the right edge regardless of whether the
   workspace-info block is present. */
.bar-spacer {
  flex: 1;
}
.bar-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.btn-bar {
  background: #21262d;
  border: 1px solid #30363d;
  color: #c9d1d9;
  padding: 4px 12px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
  white-space: nowrap;
}
.btn-bar:hover { background: #30363d; }
.btn-bar-primary {
  background: #2563eb;
  border-color: #1d4ed8;
  color: #fff;
  font-weight: 600;
}
.btn-bar-primary:hover { background: #1d4ed8; }
.run-group {
  display: flex;
  align-items: stretch;
  margin-right: 4px;
}
.btn-run {
  background: #238636;
  border: 1px solid #2ea043;
  border-right: 0;
  color: #fff;
  padding: 4px 12px;
  border-radius: 4px 0 0 4px;
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.btn-run:hover { background: #2ea043; }
.btn-run-more, .btn-run-cfg {
  background: #1a7f37;
  border: 1px solid #2ea043;
  color: #fff;
  padding: 4px 8px;
  cursor: pointer;
  font-size: 12px;
}
.btn-run-more { border-left: 1px solid rgba(255,255,255,0.18); border-radius: 0; }
.btn-run-cfg {
  border-left: 1px solid rgba(255,255,255,0.18);
  border-radius: 0 4px 4px 0;
}
.btn-run-more:hover, .btn-run-cfg:hover { background: #2ea043; }
</style>
