<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  CopyWorkspacePaths,
  CreateWorkspaceFile,
  CreateWorkspaceFolder,
  DeleteWorkspaceFile,
  MoveWorkspacePaths,
  RenameWorkspacePath,
  ReplaceWorkspace,
  SearchWorkspace,
  UploadWorkspaceFiles,
  UploadWorkspacePaths,
} from '../../wailsjs/go/main/App'
import { useWorkspaceStore } from '../stores/workspace'
import { getFileIcon, getFolderIcon } from '../utils/fileIcon'
import { OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime'

const ws = useWorkspaceStore()

interface RemoteEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: number
  isBinary?: boolean
}

interface TreeNode {
  name: string
  path: string
  isDir: boolean
  children: TreeNode[] | null
  loading?: boolean
  isBinary?: boolean
}

interface FlatNode {
  node: TreeNode
  depth: number
  padding: number
  ancestorHasNext: boolean[]
  hasNext: boolean
}

interface ClipboardState {
  mode: 'copy' | 'cut'
  paths: string[]
}

interface SearchMatch {
  line: number
  column: number
  text: string
  match: string
}

interface SearchResult {
  path: string
  matches: SearchMatch[]
}

const tree = ref<TreeNode[]>([])
const pendingFiles = ref<string[]>([])
const pendingOtherFiles = ref<string[]>([])
const pendingDirectories = ref<string[]>([])
const treeBuildScheduled = ref(false)
const expanded = ref<Set<string>>(new Set())
const selectedPaths = ref<Set<string>>(new Set())
const lastSelectedIndex = ref<number | null>(null)
const clipboard = ref<ClipboardState | null>(null)
const actionError = ref('')
const contextMenu = ref<{ x: number; y: number; node: TreeNode } | null>(null)
const renamePath = ref<string | null>(null)
const renameValue = ref('')
const createMode = ref<'file' | 'folder' | null>(null)
const createTargetDir = ref('')
const createValue = ref('')
const inputRef = ref<HTMLInputElement>()
const searchOpen = ref(false)
const searchQuery = ref('')
const replaceValue = ref('')
const matchCase = ref(false)
const searchResults = ref<SearchResult[]>([])
const searching = ref(false)
const replacing = ref(false)
const searchError = ref('')
const dragOverPath = ref<string | null>(null)
const dropUploading = ref(false)

const flattenedTree = computed(() => renderTree(tree.value))
const searchMatchCount = computed(() => searchResults.value.reduce((total, result) => total + result.matches.length, 0))
const selectedNodes = computed(() => flattenedTree.value
  .filter(item => selectedPaths.value.has(item.node.path))
  .map(item => item.node))
const operationNodes = computed(() => selectedNodes.value.filter(node =>
  node.name !== '..' && !selectedNodes.value.some(parent =>
    parent.isDir && parent.path !== node.path && node.path.startsWith(`${parent.path}/`),
  ),
))

function normalizePath(value: string): string {
  return value.replace(/\\/g, '/').replace(/^\.\//, '').replace(/\/+$/, '')
}

function joinPath(dir: string, name: string): string {
  return normalizePath([dir, name.trim()].filter(Boolean).join('/'))
}

function getParentDir(node?: TreeNode): string {
  if (!node || node.name === '..') return ''
  return node.isDir ? node.path : node.path.split('/').slice(0, -1).join('/')
}

function sortChildren(nodes: TreeNode[]) {
  nodes.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    if (a.name === '..') return -1
    if (b.name === '..') return 1
    return a.name.localeCompare(b.name)
  })
  for (const node of nodes) {
    if (node.isDir && node.children && node.children.length > 0) sortChildren(node.children)
  }
}

function entriesToTree(entries: RemoteEntry[]): TreeNode[] {
  return entries
    .filter(entry => entry.name !== '.' && entry.name !== '..')
    .map(entry => ({
      name: entry.name,
      path: entry.path,
      isDir: entry.isDir,
      children: entry.isDir ? null : [],
      isBinary: !!entry.isBinary,
    }))
}

function buildTree(files: string[], binaryFiles: string[] = [], directories: string[] = []): TreeNode[] {
  const root: TreeNode = { name: '', path: '', isDir: true, children: [] }
  const addPath = (file: string, isBinary: boolean, isDirectory = false) => {
    const parts = normalizePath(file).split('/')
    let current = root
    let currentPath = ''
    for (let i = 0; i < parts.length; i += 1) {
      const part = parts[i]
      currentPath = currentPath ? `${currentPath}/${part}` : part
      const isLast = i === parts.length - 1
      let child = current.children!.find(item => item.name === part)
      if (!child) {
        child = { name: part, path: currentPath, isDir: !isLast || isDirectory, children: [] }
        if (isLast && !isDirectory) child.isBinary = isBinary
        current.children!.push(child)
      }
      if (!isLast || isDirectory) child.isDir = true
      current = child
    }
  }
  directories.forEach(directory => addPath(directory, false, true))
  files.forEach(file => addPath(file, false))
  binaryFiles.forEach(file => addPath(file, true))
  sortChildren(root.children!)
  return root.children!
}

function scheduleLocalTreeBuild(files: string[], otherFiles: string[], directories: string[] = []) {
 const nextFiles = files
 const nextOtherFiles = otherFiles
 const nextDirectories = directories
 pendingFiles.value = nextFiles
 pendingOtherFiles.value = nextOtherFiles
 pendingDirectories.value = nextDirectories
 if (treeBuildScheduled.value) return
 treeBuildScheduled.value = true
 window.setTimeout(() => {
  treeBuildScheduled.value = false
  if (pendingFiles.value !== nextFiles || pendingOtherFiles.value !== nextOtherFiles || pendingDirectories.value !== nextDirectories) {
   scheduleLocalTreeBuild(pendingFiles.value, pendingOtherFiles.value, pendingDirectories.value)
   return
  }
  tree.value = buildTree(nextFiles, nextOtherFiles, nextDirectories)
 }, 0)
}

async function initRemoteTree() {
  tree.value = []
  const entries = await ws.loadRemoteDir('')
  tree.value = entriesToTree(entries || [])
  sortChildren(tree.value)
}

async function loadRemoteChildren(node: TreeNode) {
  node.loading = true
  const entries = await ws.loadRemoteDir(node.path)
  node.children = entriesToTree(entries || [])
  node.loading = false
  sortChildren(node.children!)
  tree.value = [...tree.value]
}

async function runSearch() {
  const query = searchQuery.value.trim()
  searchError.value = ''
  if (!query) {
    searchResults.value = []
    return
  }
  searching.value = true
  try {
    searchResults.value = await SearchWorkspace(query, matchCase.value) || []
  } catch (error: any) {
    searchResults.value = []
    searchError.value = error?.message || String(error)
  } finally {
    searching.value = false
  }
}

async function replaceAll() {
  const query = searchQuery.value.trim()
  if (!query || replacing.value) return
  const message = '确定替换整个工作区中的所有匹配内容吗？'
  if (!window.confirm(message)) return
  replacing.value = true
  searchError.value = ''
  try {
    const changedPaths = await ReplaceWorkspace(query, replaceValue.value, matchCase.value) || []
    await ws.syncChanges()
    ws.reloadPreviewFiles(changedPaths)
    await runSearch()
  } catch (error: any) {
    searchError.value = error?.message || String(error)
  } finally {
    replacing.value = false
  }
}

function openSearchResult(result: SearchResult) {
  ws.openPreviewFile(result.path)
}

function closeSearch() {
  searchOpen.value = false
  searchError.value = ''
}

async function refreshTree() {
  actionError.value = ''
  if (ws.info?.isRemote) {
    await initRemoteTree()
  } else {
    await ws.refreshLocal()
  }
}

watch(() => ws.info, async info => {
  selectedPaths.value = new Set()
  lastSelectedIndex.value = null
  if (!info) {
    pendingFiles.value = []
    pendingOtherFiles.value = []
    pendingDirectories.value = []
    tree.value = []
    return
  }
  if (info.isRemote) {
    await initRemoteTree()
  } else {
    scheduleLocalTreeBuild(info.files || [], info.otherFiles || [], info.directories || [])
  }
}, { immediate: true })

function renderTree(nodes: TreeNode[], depth = 0, ancestorHasNext: boolean[] = []): FlatNode[] {
  const result: FlatNode[] = []
  nodes.forEach((node, index) => {
    const hasNext = index < nodes.length - 1
    result.push({ node, depth, padding: depth * 18 + 8, ancestorHasNext, hasNext })
    if (node.isDir && isExpanded(node) && node.children) {
      result.push(...renderTree(node.children, depth + 1, [...ancestorHasNext, hasNext]))
    }
  })
  return result
}

function isExpanded(node: TreeNode): boolean {
  return expanded.value.has(node.path)
}

function isSelected(node: TreeNode): boolean {
  return selectedPaths.value.has(node.path)
}

function selectNode(node: TreeNode, event?: MouseEvent) {
  if (node.name === '..') return
  const items = flattenedTree.value
  const index = items.findIndex(item => item.node.path === node.path)
  const next = new Set(selectedPaths.value)
  if (event?.shiftKey && lastSelectedIndex.value !== null) {
    const [start, end] = [lastSelectedIndex.value, index].sort((a, b) => a - b)
    for (let i = start; i <= end; i += 1) {
      if (items[i].node.name !== '..') next.add(items[i].node.path)
    }
  } else if (event?.ctrlKey || event?.metaKey) {
    if (next.has(node.path)) next.delete(node.path)
    else next.add(node.path)
    lastSelectedIndex.value = index
  } else {
    next.clear()
    next.add(node.path)
    lastSelectedIndex.value = index
  }
  selectedPaths.value = next
}

function toggleFolder(node: TreeNode) {
  if (!node.isDir) return
  if (expanded.value.has(node.path)) {
    expanded.value.delete(node.path)
  } else {
    expanded.value.add(node.path)
    if (node.children === null) loadRemoteChildren(node)
  }
}

function handleClick(node: TreeNode, event: MouseEvent) {
  selectNode(node, event)
  if (renamePath.value === node.path) return
  if (node.isDir) {
    toggleFolder(node)
  } else if (!node.isBinary) {
    ws.openPreviewFile(node.path)
  }
}

function onDragStart(event: DragEvent, node: TreeNode) {
  if (node.name === '..') return
  if (!isSelected(node)) selectNode(node)
  const paths = operationNodes.value.length ? operationNodes.value.map(item => item.path) : [node.path]
  const absolutePath = ws.getAbsolutePath(paths[0])
  const transfer = event.dataTransfer
  if (!transfer) return
  transfer.setData('text/plain', absolutePath)
  transfer.setData('text/uri-list', `file://${absolutePath.replace(/\\/g, '/')}`)
  transfer.setData('application/x-aimuxterm-file-path', absolutePath)
  transfer.setData('application/x-aimuxterm-workspace-paths', JSON.stringify(paths))
  transfer.effectAllowed = 'copyMove'
}

function dropTargetDir(node?: TreeNode): string {
  return node?.isDir ? node.path : ''
}

function onDragOver(event: DragEvent, node?: TreeNode) {
  event.preventDefault()
  event.stopPropagation()
  const types = Array.from(event.dataTransfer?.types || [])
  if (!types.includes('application/x-aimuxterm-workspace-paths') && !types.includes('Files')) return
  if (event.dataTransfer) event.dataTransfer.dropEffect = types.includes('application/x-aimuxterm-workspace-paths') ? 'move' : 'copy'
  dragOverPath.value = dropTargetDir(node)
}

function onDragLeave(event: DragEvent, node?: TreeNode) {
  if (event.currentTarget !== event.target) return
  const target = dropTargetDir(node)
  if (dragOverPath.value === target) dragOverPath.value = null
}

async function moveDroppedPaths(paths: string[], targetDir: string) {
  if (!paths.length || dropUploading.value) return
  dropUploading.value = true
  actionError.value = ''
  try {
    await MoveWorkspacePaths(paths, targetDir)
    const nodeMap = new Map(flattenedTree.value.map(item => [item.node.path, item.node]))
    for (const sourcePath of paths) {
      const node = nodeMap.get(sourcePath)
      const newPath = joinPath(targetDir, sourcePath.split('/').pop() || sourcePath)
      ws.replacePreviewPath(sourcePath, newPath, Boolean(node?.isDir))
    }
    await refreshTree()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  } finally {
    dropUploading.value = false
  }
}

async function uploadDroppedPaths(paths: string[], targetDir: string) {
  if (!paths.length || dropUploading.value) return
  dropUploading.value = true
  actionError.value = ''
  try {
    await UploadWorkspacePaths(paths, targetDir)
    await refreshTree()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  } finally {
    dropUploading.value = false
  }
}

function onDrop(event: DragEvent, node?: TreeNode) {
  event.preventDefault()
  event.stopPropagation()
  const targetDir = dropTargetDir(node)
  dragOverPath.value = null
  const internalPaths = event.dataTransfer?.getData('application/x-aimuxterm-workspace-paths')
  if (internalPaths) {
    try {
      void moveDroppedPaths(JSON.parse(internalPaths), targetDir)
    } catch {
      actionError.value = '读取拖放文件失败'
    }
  }
}

function externalDropTarget(x: number, y: number): string | null {
  const element = document.elementFromPoint(x, y)
  if (!element?.closest('.file-tree-panel')) return null
  const treeNode = element.closest<HTMLElement>('[data-tree-path]')
  const path = treeNode?.dataset.treePath
  const node = path ? flattenedTree.value.find(item => item.node.path === path)?.node : undefined
  return dropTargetDir(node)
}

onMounted(() => {
  OnFileDrop((x, y, paths) => {
    const targetDir = externalDropTarget(x, y)
    if (ws.hasWorkspace && targetDir !== null) void uploadDroppedPaths(paths, targetDir)
  }, false)
})
onBeforeUnmount(() => OnFileDropOff())

function getIcon(node: TreeNode): string {
  if (node.name === '..') return getFolderIcon(node.name, true)
  if (node.isDir) return getFolderIcon(node.name, isExpanded(node))
  return getFileIcon(node.name)
}

function openContextMenu(event: MouseEvent, node: TreeNode) {
  event.preventDefault()
  if (!isSelected(node)) selectNode(node)
  contextMenu.value = { x: event.clientX, y: event.clientY, node }
}

function closeContextMenu() {
  contextMenu.value = null
}

function startCreate(mode: 'file' | 'folder', target?: TreeNode) {
  closeContextMenu()
  createMode.value = mode
  createTargetDir.value = getParentDir(target || selectedNodes.value[0])
  createValue.value = ''
  nextTick(() => inputRef.value?.focus())
}

function cancelCreate() {
  createMode.value = null
  createValue.value = ''
}

async function submitCreate() {
  const mode = createMode.value
  const name = createValue.value.trim()
  if (!mode || !name) return cancelCreate()
  if (name.includes('/') || name.includes('\\')) {
    actionError.value = '名称不能包含路径分隔符'
    return
  }
  const path = joinPath(createTargetDir.value, name)
  try {
    if (mode === 'file') await CreateWorkspaceFile(path)
    else await CreateWorkspaceFolder(path)
    await refreshTree()
    cancelCreate()
    if (mode === 'file') ws.openPreviewFile(path)
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  }
}

function startRename(node = contextMenu.value?.node) {
  closeContextMenu()
  if (!node || node.name === '..') return
  renamePath.value = node.path
  renameValue.value = node.name
  nextTick(() => inputRef.value?.focus())
}

function cancelRename() {
  renamePath.value = null
  renameValue.value = ''
}

async function submitRename(node: TreeNode) {
  const name = renameValue.value.trim()
  if (!name || name === node.name) return cancelRename()
  if (name.includes('/') || name.includes('\\')) {
    actionError.value = '名称不能包含路径分隔符'
    return
  }
  const target = joinPath(getParentDir(node), name)
  try {
    await RenameWorkspacePath(node.path, target)
    ws.replacePreviewPath(node.path, target, node.isDir)
    await refreshTree()
    cancelRename()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  }
}

function copySelection(mode: 'copy' | 'cut') {
  closeContextMenu()
  const paths = operationNodes.value.map(node => node.path)
  if (paths.length) clipboard.value = { mode, paths }
}

async function pasteSelection(target?: TreeNode) {
  closeContextMenu()
  if (!clipboard.value?.paths.length) return
  const destination = getParentDir(target || selectedNodes.value.find(node => node.isDir) || selectedNodes.value[0])
  try {
    await CopyWorkspacePaths(clipboard.value.paths, destination)
    if (clipboard.value.mode === 'cut') {
      for (const path of clipboard.value.paths) await DeleteWorkspaceFile(path)
      ws.closePreviewPaths(clipboard.value.paths)
      clipboard.value = null
    }
    await refreshTree()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  }
}

async function uploadFiles(target?: TreeNode) {
  closeContextMenu()
  try {
    await UploadWorkspaceFiles(getParentDir(target || selectedNodes.value.find(node => node.isDir)))
    await refreshTree()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  }
}

async function deleteSelection() {
  closeContextMenu()
  const nodes = operationNodes.value
  if (!nodes.length) return
  const label = nodes.length === 1 ? `“${nodes[0].name}”` : `选中的 ${nodes.length} 个项目`
  if (!window.confirm(`确定删除${label}？删除后可通过文件变更列表还原。`)) return
  try {
    for (const node of nodes) await DeleteWorkspaceFile(node.path)
    ws.closePreviewPaths(nodes.map(node => node.path))
    selectedPaths.value = new Set()
    await refreshTree()
  } catch (error: any) {
    actionError.value = error?.message || String(error)
  }
}
</script>

<template>
  <div class="file-tree-panel" @click="closeContextMenu">
    <div class="panel-header">
      <span>文件目录</span>
      <button class="tree-action icon-action" title="全局搜索与替换" @click.stop="searchOpen = !searchOpen; searchError = ''">⌕</button>
      <button class="tree-action" title="新建文件" @click.stop="startCreate('file')">＋文件</button>
      <button class="tree-action" title="新建文件夹" @click.stop="startCreate('folder')">＋文件夹</button>
      <button class="tree-action" title="上传文件" @click.stop="uploadFiles()">上传</button>
      <button class="tree-action icon-action" title="刷新文件目录" @click.stop="refreshTree">↻</button>
    </div>
    <div class="tree-body" @dragover="onDragOver($event)" @dragleave="onDragLeave($event)" @drop="onDrop($event)">
      <div v-if="searchOpen" class="workspace-search" @click.stop>
        <div class="search-fields">
          <input v-model="searchQuery" placeholder="搜索工作区" @keydown.enter.prevent="runSearch" />
          <input v-model="replaceValue" placeholder="替换为" @keydown.enter.prevent="replaceAll" />
        </div>
        <div class="search-actions">
          <label title="区分大小写"><input v-model="matchCase" type="checkbox" @change="runSearch" /> Aa</label>
          <span class="search-summary">{{ searching ? '搜索中...' : `${searchResults.length} 个文件，${searchMatchCount} 处匹配` }}</span>
          <button class="tree-action" :disabled="!searchQuery.trim() || searching" @click="runSearch">搜索</button>
          <button class="tree-action" :disabled="!searchMatchCount || replacing" @click="replaceAll">{{ replacing ? '替换中...' : '全部替换' }}</button>
          <button class="tree-action icon-action" title="关闭搜索" @click="closeSearch">×</button>
        </div>
        <div v-if="searchError" class="tree-error">{{ searchError }}</div>
        <div v-if="searchResults.length" class="search-results">
          <div v-for="result in searchResults" :key="result.path" class="search-result-file">
            <button class="search-result-path" @click="openSearchResult(result)">{{ result.path }} <span>{{ result.matches.length }}</span></button>
            <button v-for="match in result.matches" :key="`${match.line}:${match.column}`" class="search-result-match" @click="openSearchResult(result)">
              <span>{{ match.line }}:{{ match.column }}</span><code>{{ match.text }}</code>
            </button>
          </div>
        </div>
      </div>
      <div v-if="dropUploading" class="tree-drop-status">正在上传或移动文件…</div>
      <div v-if="actionError" class="tree-error">{{ actionError }}</div>
      <div v-if="createMode" class="tree-inline-editor create-editor">
        <span>{{ createMode === 'file' ? '新建文件' : '新建文件夹' }}</span>
        <input ref="inputRef" v-model="createValue" @keydown.enter="submitCreate" @keydown.escape="cancelCreate" @blur="submitCreate" />
      </div>
      <div v-if="!ws.hasWorkspace" class="tree-empty">未选择工作区</div>
      <div v-else-if="tree.length === 0" class="tree-empty">加载中...</div>
      <div
        v-for="item in flattenedTree"
        :key="item.node.path"
        class="tree-node"
        :draggable="item.node.name !== '..'"
        :data-tree-path="item.node.path"
        :class="{ 'is-binary': item.node.isBinary, selected: isSelected(item.node), 'drop-target': item.node.isDir && dragOverPath === item.node.path }"
        :title="item.node.isBinary ? item.node.name + ' - 二进制文件，不加载内容' : item.node.name"
        @click.stop="handleClick(item.node, $event)"
        @contextmenu="openContextMenu($event, item.node)"
        @dragstart="onDragStart($event, item.node)"
        @dragover="onDragOver($event, item.node)"
        @dragleave="onDragLeave($event, item.node)"
        @drop="onDrop($event, item.node)"
      >
        <span class="tree-guides" aria-hidden="true">
          <i
            v-for="(hasNext, level) in item.ancestorHasNext"
            v-show="hasNext"
            :key="level"
            class="tree-guide"
            :style="{ left: (level * 18 + 8) + 'px' }"
          ></i>
          <i
            v-if="item.depth > 0"
            class="tree-branch"
            :class="{ 'is-last': !item.hasNext }"
            :style="{ left: ((item.depth - 1) * 18 + 8) + 'px' }"
          ></i>
        </span>
        <button
          v-if="item.node.isDir"
          class="tree-chevron"
          :class="{ expanded: isExpanded(item.node) }"
          :title="isExpanded(item.node) ? '折叠文件夹' : '展开文件夹'"
          @click.stop="toggleFolder(item.node)"
        >›</button>
        <span v-else class="tree-chevron-placeholder"></span>
        <img class="node-icon" :src="getIcon(item.node)" alt="" />
        <input
          v-if="renamePath === item.node.path"
          ref="inputRef"
          v-model="renameValue"
          class="rename-input"
          @click.stop
          @keydown.enter="submitRename(item.node)"
          @keydown.escape="cancelRename"
          @blur="submitRename(item.node)"
        />
        <span v-else class="node-name">{{ item.node.name }}</span>
        <span v-if="item.node.loading" class="node-loading">...</span>
      </div>
    </div>
    <div
      v-if="contextMenu"
      class="tree-context-menu"
      :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
      @click.stop
    >
      <button @click="startCreate('file', contextMenu!.node)">新建文件</button>
      <button @click="startCreate('folder', contextMenu!.node)">新建文件夹</button>
      <button @click="startRename(contextMenu!.node)">重命名</button>
      <button @click="copySelection('copy')">复制</button>
      <button @click="copySelection('cut')">剪切</button>
      <button :disabled="!clipboard" @click="pasteSelection(contextMenu.node)">粘贴</button>
      <button @click="uploadFiles(contextMenu.node)">上传到此处</button>
      <button class="danger" @click="deleteSelection">删除{{ selectedNodes.length > 1 ? ` (${selectedNodes.length})` : '' }}</button>
    </div>
  </div>
</template>

<style scoped>
.file-tree-panel { width: 220px; background: var(--surface-tree); border-right: 1px solid #2a2a2e; display: flex; flex-direction: column; overflow: hidden; flex-shrink: 0; }
.panel-header { display: flex; align-items: center; gap: 3px; min-height: 32px; padding: 5px 7px 5px 10px; color: #888; border-bottom: 1px solid #2a2a2e; font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: .5px; }
.panel-header > span { margin-right: auto; white-space: nowrap; }
.tree-action { border: 1px solid #3a3a3e; border-radius: 3px; background: #21262d; color: #8b949e; padding: 2px 4px; font-size: 10px; cursor: pointer; text-transform: none; white-space: nowrap; }
.tree-action:hover { color: #58a6ff; border-color: #58a6ff; }
.icon-action { font-size: 13px; padding: 0 4px; }
.tree-body { flex: 1; overflow: auto; }
.tree-empty { padding: 20px 12px; color: #555; text-align: center; font-size: 12px; }
.tree-drop-status { margin: 6px 8px; color: #58a6ff; font-size: 11px; }
.tree-error { margin: 6px 8px; color: #f85149; font-size: 11px; line-height: 1.35; word-break: break-word; }
.workspace-search { padding: 7px; border-bottom: 1px solid #30363d; background: #202124; }
.search-fields { display: grid; gap: 5px; }
.search-fields input { min-width: 0; width: 100%; box-sizing: border-box; border: 1px solid #3a3a3e; border-radius: 3px; background: #161618; color: #e6edf3; padding: 5px 6px; font-size: 11px; }
.search-fields input:focus { outline: 1px solid #58a6ff; border-color: #58a6ff; }
.search-actions { display: flex; align-items: center; gap: 5px; margin-top: 6px; }
.search-actions label { display: inline-flex; align-items: center; gap: 3px; color: #9da7b3; font-size: 10px; white-space: nowrap; }
.search-actions input { margin: 0; }
.search-summary { flex: 1; min-width: 0; overflow: hidden; color: #8b949e; font-size: 10px; white-space: nowrap; text-overflow: ellipsis; }
.search-actions .tree-action { padding: 3px 5px; font-size: 10px; }
.search-results { max-height: 230px; margin: 7px -2px -2px; overflow: auto; }
.search-result-file + .search-result-file { margin-top: 5px; }
.search-result-path, .search-result-match { display: flex; width: 100%; border: 0; background: transparent; text-align: left; cursor: pointer; }
.search-result-path { justify-content: space-between; color: #58a6ff; padding: 3px 4px; font-size: 11px; }
.search-result-path span { color: #8b949e; }
.search-result-match { align-items: baseline; gap: 5px; color: #c9d1d9; padding: 2px 4px 2px 8px; font-size: 10px; }
.search-result-match:hover, .search-result-path:hover { background: #30363d; }
.search-result-match span { flex-shrink: 0; color: #8b949e; }
.search-result-match code { min-width: 0; overflow: hidden; color: inherit; font: inherit; white-space: nowrap; text-overflow: ellipsis; }
.tree-node { position: relative; display: flex; align-items: center; gap: 4px; min-height: 24px; padding: 3px 8px; color: #aaa; cursor: pointer; font-size: 12px; white-space: nowrap; overflow: hidden; user-select: none; }
.tree-node:hover { background: #1e1e22; color: #ddd; }
.tree-node.selected { background: #264f78; color: #fff; }
.tree-guides { position: absolute; inset: 0; pointer-events: none; }
.tree-guide { position: absolute; top: 0; bottom: 0; width: 1px; background: rgba(139, 148, 158, .28); }
.tree-branch { position: absolute; top: 0; width: 18px; height: 50%; border-bottom: 1px solid rgba(139, 148, 158, .28); border-left: 1px solid rgba(139, 148, 158, .28); }
.tree-branch:not(.is-last) { height: 100%; }
.tree-chevron, .tree-chevron-placeholder { position: relative; z-index: 1; width: 14px; height: 18px; flex-shrink: 0; }
.tree-chevron { border: 0; border-radius: 2px; background: transparent; color: #8b949e; cursor: pointer; font-size: 17px; line-height: 16px; transition: transform .12s ease, color .12s ease; }
.tree-chevron:hover { background: #30363d; color: #c9d1d9; }
.tree-chevron.expanded { transform: rotate(90deg); }
.tree-node.selected .tree-chevron { color: #dbeafe; }
.tree-node.drop-target { outline: 1px solid #58a6ff; outline-offset: -1px; background: rgba(56, 139, 253, .2); }
.tree-node.is-binary { color: #6b6b73; }
.tree-node.is-binary:hover { color: #8a8a93; }
.node-icon { width: 16px; height: 16px; flex-shrink: 0; object-fit: contain; }
.node-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.node-loading { color: #666; font-size: 10px; }
.tree-inline-editor { display: flex; align-items: center; gap: 6px; padding: 6px 8px; color: #8b949e; font-size: 11px; }
.tree-inline-editor input, .rename-input { min-width: 0; flex: 1; border: 1px solid #58a6ff; border-radius: 2px; outline: none; background: #0d1117; color: #e6edf3; padding: 2px 4px; font: inherit; }
.rename-input { width: 100%; }
.tree-context-menu { position: fixed; z-index: 100; min-width: 136px; padding: 4px; border: 1px solid #3a3a3e; border-radius: 4px; background: #1e1e20; box-shadow: 0 8px 24px rgba(0, 0, 0, .45); }
.tree-context-menu button { display: block; width: 100%; border: 0; border-radius: 3px; background: transparent; color: #c9d1d9; padding: 5px 8px; text-align: left; font-size: 11px; cursor: pointer; }
.tree-context-menu button:hover:not(:disabled) { background: #30363d; }
.tree-context-menu button:disabled { color: #555; cursor: default; }
.tree-context-menu button.danger { color: #f85149; }
</style>
