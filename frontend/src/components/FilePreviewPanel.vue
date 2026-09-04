<script setup lang="ts">
import { ref, watch, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import hljs from 'highlight.js'
import 'highlight.js/styles/github-dark.css'
import DiffView from './DiffView.vue'
import CodeEditor from './CodeEditor.vue'
import { GetFileContent, GetFileDiff, GetFilePreviewData, SaveFile } from '../../wailsjs/go/main/App'
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime'
import { useWorkspaceStore } from '../stores/workspace'
import { useFileChangesStore } from '../stores/fileChanges'
import { detectLang } from '../utils/detectLang'
import { previewKind, type PreviewKind } from '../utils/previewKind'
import { renderMarkdown, renderMermaidBlocks, isSafeHref } from '../utils/renderMarkdown'

const ws = useWorkspaceStore()
const fc = useFileChangesStore()

const cache = ref<Record<string, {
  content: string
  highlightedHtml: string
  loading: boolean
  showDiff: boolean
  oldContent: string
  newContent: string
  isEditing: boolean
  editContent: string
  isDirty: boolean
  saveError: string
  /** Markdown files only: show the rendered document instead of the source. */
  showRendered: boolean
  kind: PreviewKind
  previewUrl: string
  previewError: string
  sheetName: string
  sheets: { name: string; rows: string[][] }[]
  wordHtml: string
}>>({})

const activeFile = computed(() => ws.activePreviewFile)
const activeState = computed(() => activeFile.value ? cache.value[activeFile.value] : null)
const isChanged = computed(() => !!activeFile.value && fc.changes.some(c => c.path === activeFile.value))
const isMarkdown = computed(() => !!activeFile.value && isMarkdownPath(activeFile.value))
const activeKind = computed<PreviewKind>(() => activeFile.value ? previewKind(activeFile.value) : 'text')
const isBinaryPreview = computed(() => activeKind.value !== 'text')
const activeSheet = computed(() => {
  const st = activeState.value
  if (!st?.sheets?.length) return null
  return st.sheets.find(s => s.name === st.sheetName) || st.sheets[0]
})
const renderedHtml = computed(() => {
  const st = activeState.value
  if (!st || !isMarkdown.value) return ''
  return renderMarkdown(st.content)
})
const markdownRef = ref<HTMLElement | null>(null)

watch([renderedHtml, () => activeState.value?.showRendered], async () => {
  await nextTick()
  const root = markdownRef.value
  if (!root || !activeState.value?.showRendered) return
  await renderMermaidBlocks(root)
}, { flush: 'post' })

function isMarkdownPath(path: string): boolean {
  return detectLang(path) === 'markdown'
}

function getOrCreate(path: string) {
  if (!cache.value[path]) {
    cache.value[path] = {
      content: '', highlightedHtml: '', loading: false, showDiff: false,
      oldContent: '', newContent: '', isEditing: true, editContent: '', isDirty: false, saveError: '',
      showRendered: false,
      kind: previewKind(path), previewUrl: '', previewError: '',
      sheetName: '', sheets: [], wordHtml: '',
    }
  }
  return cache.value[path]
}

function getFileName(path: string) {
  return path.replace(/\\/g, '/').split('/').pop() || path
}

function highlight(code: string, filePath: string): string {
  if (!code) return ''
  const lang = detectLang(filePath)
  try {
    if (lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang }).value
    }
    return hljs.highlightAuto(code).value
  } catch {
    return code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  }
}

async function loadFile(path: string) {
  const st = getOrCreate(path)
  st.loading = true
  st.showDiff = false
  st.isDirty = false
  st.saveError = ''
  st.previewError = ''
  st.kind = previewKind(path)
  st.isEditing = st.kind === 'text'
  if (st.kind !== 'text') {
    st.previewUrl = ''
    st.sheets = []
    st.wordHtml = ''
    try {
      const url = await GetFilePreviewData(path)
      st.previewUrl = url || ''
      if (!st.previewUrl) throw new Error('empty preview')
      if (st.kind === 'spreadsheet') await parseSpreadsheet(st)
      if (st.kind === 'word') await parseWord(st)
    } catch (e: any) {
      st.previewError = String(e?.message || e || '无法预览该文件')
    }
    st.loading = false
    return
  }
  try {
    const raw = await GetFileContent(path) || ''
    st.content = raw
    st.editContent = raw
    st.highlightedHtml = highlight(raw, path)
  } catch {
    st.highlightedHtml = '<span style="color:#f85149">[无法读取文件]</span>'
  }
  st.loading = false
}

function dataURLToUint8(url: string): Uint8Array {
  const i = url.indexOf(',')
  const b64 = i >= 0 ? url.slice(i + 1) : url
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let n = 0; n < bin.length; n++) out[n] = bin.charCodeAt(n)
  return out
}

function cellText(v: unknown): string {
  if (v == null) return ''
  if (v instanceof Date) return v.toISOString().slice(0, 10)
  return String(v)
}

async function parseSpreadsheet(st: { previewUrl: string; sheets: { name: string; rows: string[][] }[]; sheetName: string; previewError: string }) {
  const { read, utils } = await import('xlsx')
  const bytes = dataURLToUint8(st.previewUrl)
  const wb = read(bytes, { type: 'array', cellDates: true })
  const names = (wb.SheetNames || []) as string[]
  const sheets = names.map((name: string) => {
    const sheet = wb.Sheets[name]
    const aoa = utils.sheet_to_json(sheet, { header: 1, raw: false, defval: '' }) as unknown[][]
    const rows = aoa.slice(0, 500).map((r: unknown[]) => (r || []).slice(0, 40).map(cellText))
    return { name, rows }
  }).filter((s: { name: string }) => !!s.name)
  st.sheets = sheets
  st.sheetName = sheets[0]?.name || ''
  if (!sheets.length) st.previewError = '工作表为空'
}

async function parseWord(st: { previewUrl: string; wordHtml: string; previewError: string; kind: PreviewKind }) {
  if (st.previewUrl.startsWith('data:application/msword')) {
    st.previewError = '旧版 .doc 暂不支持预览，请另存为 .docx'
    return
  }
  const mammothMod: any = await import('mammoth/mammoth.browser')
  const convert = mammothMod.convertToHtml || mammothMod.default?.convertToHtml
  const bytes = dataURLToUint8(st.previewUrl)
  const copy = new Uint8Array(bytes.byteLength)
  copy.set(bytes)
  const result = await convert({ arrayBuffer: copy.buffer })
  st.wordHtml = result.value || '<p>（空文档）</p>'
}

async function toggleDiff() {
  const st = activeState.value
  if (!st || !activeFile.value) return
  if (!st.showDiff) {
    try {
      const diff = await GetFileDiff(activeFile.value)
      st.oldContent = diff?.old ?? ''
      st.newContent = diff?.new ?? ''
      st.showDiff = true
    } catch { }
  } else {
    st.showDiff = false
  }
}

function toggleRendered() {
  const st = activeState.value
  if (!st) return
  st.showRendered = !st.showRendered
}

/** Resolve a relative markdown link against the directory of `fromPath`. */
function resolveRelative(fromPath: string, href: string): string {
  const dir = fromPath.replace(/\\/g, '/').split('/').slice(0, -1)
  const out = href.startsWith('/') ? [] : dir
  const parts = href.replace(/\\/g, '/').replace(/^\//, '').split('/')
  const stack = [...out]
  for (const part of parts) {
    if (part === '' || part === '.') continue
    if (part === '..') stack.pop()
    else stack.push(part)
  }
  return stack.join('/')
}

/**
 * Links inside rendered markdown must not navigate the webview away from the
 * app: external targets go to the system browser, in-workspace targets open as
 * another preview tab.
 */
function onRenderedClick(e: MouseEvent) {
  const anchor = (e.target as HTMLElement | null)?.closest('a')
  if (!anchor) return
  e.preventDefault()
  const href = anchor.getAttribute('href') || ''
  if (!href || href.startsWith('#')) return
  if (isSafeHref(href)) {
    BrowserOpenURL(href)
    return
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return // unknown scheme: ignore
  if (!activeFile.value) return
  const target = resolveRelative(activeFile.value, href.split(/[?#]/)[0])
  if (target) ws.openPreviewFile(target)
}

function enterEdit() {
  const st = activeState.value
  if (!st) return
  st.editContent = st.content
  st.isEditing = true
  st.saveError = ''
}

function updateEditContent(value: string) {
  const st = activeState.value
  if (!st) return
  st.editContent = value
  st.isDirty = value !== st.content
  st.saveError = ''
}

function cancelEdit() {
  const st = activeState.value
  if (!st) return
  st.editContent = st.content
  st.isDirty = false
  st.saveError = ''
}

async function handleSave() {
  const st = activeState.value
  const path = activeFile.value
  if (!st || !path) return
  st.saveError = ''
  try {
    await SaveFile(path, st.editContent)
    st.content = st.editContent
    st.highlightedHtml = highlight(st.editContent, path)
    st.isDirty = false
    st.isEditing = true
    st.saveError = ''
    await fc.refresh()
  } catch (e: any) {
    st.saveError = '保存失败: ' + (e?.message || e)
  }
}

watch(activeFile, (path) => {
  if (path && !cache.value[path]) loadFile(path)
}, { immediate: true })

watch(() => ws.previewReloadVersion, () => {
  for (const path of ws.previewReloadPaths) {
    const state = cache.value[path]
    if (!state?.isDirty) loadFile(path)
  }
})

// ── Floating window: geometry, drag, resize ──
const GEOM_KEY = 'preview-window-geometry'
const MIN_W = 360
const MIN_H = 220

interface Geometry { x: number; y: number; w: number; h: number; maximized: boolean }

function defaultGeometry(): Geometry {
  const w = Math.min(760, Math.max(MIN_W, Math.round(window.innerWidth * 0.5)))
  const h = Math.min(620, Math.max(MIN_H, Math.round(window.innerHeight * 0.7)))
  return {
    x: Math.max(8, window.innerWidth - w - 48),
    y: Math.max(8, Math.round((window.innerHeight - h) / 2)),
    w, h, maximized: false,
  }
}

function loadGeometry(): Geometry {
  try {
    const raw = localStorage.getItem(GEOM_KEY)
    if (raw) {
      const g = JSON.parse(raw) as Geometry
      if (typeof g?.x === 'number' && typeof g?.w === 'number') return g
    }
  } catch { }
  return defaultGeometry()
}

const geom = ref<Geometry>(loadGeometry())
const minimized = ref(false)

function saveGeometry() {
  try { localStorage.setItem(GEOM_KEY, JSON.stringify(geom.value)) } catch { }
}

const windowStyle = computed(() => {
  if (geom.value.maximized) {
    return { left: '0px', top: '0px', width: '100%', height: '100%' }
  }
  return {
    left: geom.value.x + 'px',
    top: geom.value.y + 'px',
    width: geom.value.w + 'px',
    height: minimized.value ? 'auto' : geom.value.h + 'px',
  }
})

function clampIntoView() {
  const g = geom.value
  g.w = Math.min(Math.max(g.w, MIN_W), window.innerWidth)
  g.h = Math.min(Math.max(g.h, MIN_H), window.innerHeight)
  g.x = Math.min(Math.max(g.x, -g.w + 80), window.innerWidth - 80)
  g.y = Math.min(Math.max(g.y, 0), window.innerHeight - 40)
}

// Shared pointer-drag driver: onMove receives the delta from drag start.
function beginDrag(e: MouseEvent, onMove: (dx: number, dy: number) => void, cursor: string) {
  e.preventDefault()
  const startX = e.clientX
  const startY = e.clientY
  const move = (ev: MouseEvent) => onMove(ev.clientX - startX, ev.clientY - startY)
  const up = () => {
    document.removeEventListener('mousemove', move)
    document.removeEventListener('mouseup', up)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    clampIntoView()
    saveGeometry()
  }
  document.addEventListener('mousemove', move)
  document.addEventListener('mouseup', up)
  document.body.style.cursor = cursor
  document.body.style.userSelect = 'none'
}

function startMove(e: MouseEvent) {
  if (geom.value.maximized) return
  const ox = geom.value.x
  const oy = geom.value.y
  beginDrag(e, (dx, dy) => {
    geom.value.x = ox + dx
    geom.value.y = oy + dy
  }, 'move')
}

type Edge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw'

function startResize(e: MouseEvent, edge: Edge) {
  if (geom.value.maximized || minimized.value) return
  const o = { ...geom.value }
  const cursor = edge.length === 2 ? edge + '-resize' : edge === 'n' || edge === 's' ? 'ns-resize' : 'ew-resize'
  beginDrag(e, (dx, dy) => {
    const g = geom.value
    if (edge.includes('e')) g.w = Math.max(MIN_W, o.w + dx)
    if (edge.includes('s')) g.h = Math.max(MIN_H, o.h + dy)
    if (edge.includes('w')) {
      const w = Math.max(MIN_W, o.w - dx)
      g.x = o.x + (o.w - w)
      g.w = w
    }
    if (edge.includes('n')) {
      const h = Math.max(MIN_H, o.h - dy)
      g.y = o.y + (o.h - h)
      g.h = h
    }
  }, cursor)
}

function toggleMaximize() {
  geom.value.maximized = !geom.value.maximized
  minimized.value = false
  saveGeometry()
}

function toggleMinimize() {
  minimized.value = !minimized.value
}

function closeWindow() {
  for (const path of [...ws.previewFiles]) ws.closePreviewFile(path)
}

const onWindowResize = () => { clampIntoView(); saveGeometry() }
onMounted(() => {
  clampIntoView()
  window.addEventListener('resize', onWindowResize)
})
onBeforeUnmount(() => window.removeEventListener('resize', onWindowResize))
</script>

<template>
  <div
    class="preview-window"
    :class="{ maximized: geom.maximized, minimized }"
    :style="windowStyle"
    role="dialog"
    aria-label="文件预览窗口"
  >
    <div class="title-bar" @mousedown="startMove" @dblclick="toggleMaximize">
      <span class="title-text">文件预览</span>
      <div class="window-controls">
        <button
          class="win-btn"
          :title="minimized ? '展开' : '折叠'"
          :aria-label="minimized ? '展开' : '折叠'"
          @mousedown.stop
          @click="toggleMinimize"
        >{{ minimized ? '▢' : '—' }}</button>
        <button
          class="win-btn"
          :title="geom.maximized ? '还原' : '最大化'"
          :aria-label="geom.maximized ? '还原' : '最大化'"
          @mousedown.stop
          @click="toggleMaximize"
        >{{ geom.maximized ? '❐' : '□' }}</button>
        <button
          class="win-btn win-close"
          title="关闭"
          aria-label="关闭预览窗口"
          @mousedown.stop
          @click="closeWindow"
        >×</button>
      </div>
    </div>
    <div class="window-body" v-if="!minimized">
      <div class="tab-bar" v-if="ws.previewFiles.length > 0">
        <div
          v-for="path in ws.previewFiles"
          :key="path"
          class="tab"
          :class="{ active: path === ws.activePreviewFile }"
          @click="ws.activePreviewFile = path"
        >
          <span>{{ getFileName(path) }}</span>
          <button class="tab-close" @click.stop="ws.closePreviewFile(path)">×</button>
        </div>
      </div>

      <div v-if="!activeFile" class="panel-empty">点击文件树查看</div>
      <template v-else>
        <div class="preview-toolbar">
          <span class="preview-path">{{ activeFile }}</span>
          <button
            v-if="isMarkdown && !isBinaryPreview"
            class="btn-md"
            :class="{ active: activeState?.showRendered }"
            @click="toggleRendered"
          >{{ activeState?.showRendered ? '看源码' : '看渲染' }}</button>
          <template v-if="!isBinaryPreview && activeState?.isEditing">
            <span v-if="activeState?.isDirty" class="unsaved-indicator" title="有未保存的更改">●</span>
            <button class="btn-save" :disabled="!activeState?.isDirty" @click="handleSave">保存</button>
            <button class="btn-cancel" :disabled="!activeState?.isDirty" @click="cancelEdit">还原</button>
            <span v-if="activeState?.saveError" class="save-error">{{ activeState.saveError }}</span>
          </template>
          <template v-else-if="!isBinaryPreview">
            <button class="btn-edit" @click="enterEdit">编辑</button>
            <button class="btn-diff" :class="{ active: isChanged }" @click="toggleDiff">
              {{ activeState?.showDiff ? '隐藏差异' : '查看差异' }}
            </button>
          </template>
        </div>
        <div v-if="activeState?.loading" class="preview-loading">加载中...</div>
        <div v-else-if="activeState?.previewError" class="preview-error">{{ activeState.previewError }}</div>
        <div v-else-if="activeKind === 'image'" class="media-wrap">
          <img class="preview-image" :src="activeState?.previewUrl" :alt="getFileName(activeFile)" />
        </div>
        <iframe
          v-else-if="activeKind === 'pdf'"
          class="preview-pdf"
          :src="activeState?.previewUrl"
          title="PDF 预览"
        ></iframe>
        <div v-else-if="activeKind === 'spreadsheet'" class="sheet-wrap">
          <div v-if="activeState?.sheets.length" class="sheet-tabs">
            <button
              v-for="s in activeState.sheets"
              :key="s.name"
              type="button"
              class="sheet-tab"
              :class="{ active: (activeSheet?.name || activeState.sheetName) === s.name }"
              @click="activeState!.sheetName = s.name"
            >{{ s.name }}</button>
          </div>
          <div class="sheet-table-wrap">
            <table v-if="activeSheet" class="sheet-table">
              <tbody>
                <tr v-for="(row, ri) in activeSheet.rows" :key="ri">
                  <td v-for="(cell, ci) in row" :key="ci">{{ cell }}</td>
                </tr>
              </tbody>
            </table>
            <div v-else class="preview-loading">工作表为空</div>
          </div>
        </div>
        <div v-else-if="activeKind === 'word'" class="word-body" v-html="activeState?.wordHtml"></div>
        <div v-else-if="activeState?.isEditing && !activeState?.showRendered" class="editor-wrap">
          <CodeEditor
            :model-value="activeState!.editContent"
            :language="detectLang(activeFile)"
            :path="activeFile"
            @update:model-value="updateEditContent"
            @save="handleSave"
          />
        </div>
        <div v-else-if="activeState?.showDiff" class="diff-wrap">
          <DiffView
            :old-string="activeState!.oldContent"
            :new-string="activeState!.newContent"
            :language="detectLang(activeFile)"
            :file-path="activeFile"
          />
        </div>
        <!-- Rendered markdown. Source is escaped by the renderer (html: false). -->
        <div
          v-else-if="isMarkdown && activeState?.showRendered"
          class="markdown-body"
          ref="markdownRef"
          @click="onRenderedClick"
          v-html="renderedHtml"
        ></div>
        <CodeEditor
          v-else
          :model-value="activeState?.content || ''"
          :language="detectLang(activeFile)"
          :path="activeFile"
          :read-only="true"
        />
      </template>
    </div>

    <template v-if="!geom.maximized && !minimized">
      <div class="rz rz-n" @mousedown="startResize($event, 'n')"></div>
      <div class="rz rz-s" @mousedown="startResize($event, 's')"></div>
      <div class="rz rz-e" @mousedown="startResize($event, 'e')"></div>
      <div class="rz rz-w" @mousedown="startResize($event, 'w')"></div>
      <div class="rz rz-ne" @mousedown="startResize($event, 'ne')"></div>
      <div class="rz rz-nw" @mousedown="startResize($event, 'nw')"></div>
      <div class="rz rz-se" @mousedown="startResize($event, 'se')"></div>
      <div class="rz rz-sw" @mousedown="startResize($event, 'sw')"></div>
    </template>
  </div>
</template>

<style scoped>
.preview-window {
  position: fixed;
  /* Above panels/workspace bar, below modal overlays (90+) and the AI FAB (120). */
  z-index: 80;
  background: var(--surface-editor);
  border: 1px solid #30363d;
  border-radius: 8px;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.55);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 360px;
}
.preview-window.maximized {
  border-radius: 0;
}
.title-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 6px 0 12px;
  height: 32px;
  flex-shrink: 0;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  cursor: move;
  user-select: none;
}
.title-text {
  flex: 1;
  font-size: 11px;
  font-weight: 600;
  color: #8b949e;
  letter-spacing: 0.5px;
}
.window-controls {
  display: flex;
  align-items: center;
  gap: 2px;
}
.win-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  width: 24px;
  height: 22px;
  border-radius: 4px;
}
.win-btn:hover { background: #30363d; color: #e6edf3; }
.win-close:hover { background: #da3633; color: #fff; }
.window-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}
.tab-bar {
  display: flex;
  align-items: center;
  background: #1a1a1c;
  border-bottom: 1px solid #333;
  height: 28px;
  padding: 0 4px;
  gap: 2px;
  overflow-x: auto;
  flex-shrink: 0;
}
.tab {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 4px 4px 0 0;
  cursor: pointer;
  font-size: 11px;
  color: #999;
  white-space: nowrap;
  user-select: none;
}
.tab.active { background: #0d1117; color: #fff; }
.tab:hover { background: #2a2a2e; }
.tab-close {
  background: none;
  border: none;
  color: #666;
  cursor: pointer;
  font-size: 14px;
  padding: 0;
  line-height: 1;
}
.tab-close:hover { color: #f44336; }
.panel-empty {
  padding: 20px;
  text-align: center;
  color: #555;
  font-size: 12px;
}
.preview-toolbar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  height: 28px;
  flex-shrink: 0;
}
.preview-path {
  flex: 1;
  font-size: 11px;
  color: #8b949e;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.btn-edit, .btn-save, .btn-cancel {
  background: #21262d;
  border: 1px solid #30363d;
  color: #8b949e;
  font-size: 10px;
  padding: 1px 8px;
  border-radius: 3px;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.btn-edit:hover, .btn-save:hover { color: #58a6ff; border-color: #58a6ff; }
.btn-save:disabled, .btn-cancel:disabled { opacity: .45; cursor: default; }
.btn-cancel:hover:not(:disabled) { color: #f85149; border-color: #f85149; }
.unsaved-indicator { color: #d29922; font-size: 12px; line-height: 1; }
.btn-diff {
  background: #21262d;
  border: 1px solid #30363d;
  color: #8b949e;
  font-size: 10px;
  padding: 1px 8px;
  border-radius: 3px;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.btn-diff:hover { color: #d2991d; border-color: #d2991d; }
.btn-diff.active { color: #d2991d; }
.btn-md {
  background: #21262d;
  border: 1px solid #30363d;
  color: #8b949e;
  font-size: 10px;
  padding: 1px 8px;
  border-radius: 3px;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.btn-md:hover { color: #58a6ff; border-color: #58a6ff; }
.btn-md.active { color: #58a6ff; }
.save-error {
  font-size: 10px;
  color: #f85149;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.preview-loading { padding: 20px; color: #8b949e; font-size: 12px; }
.preview-error { padding: 20px; color: #f85149; font-size: 12px; }
.media-wrap {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #010409;
  padding: 16px;
}
.preview-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  background: repeating-conic-gradient(#161b22 0% 25%, #0d1117 0% 50%) 50% / 16px 16px;
}
.preview-pdf {
  flex: 1;
  min-height: 0;
  width: 100%;
  border: 0;
  background: #0d1117;
}
.sheet-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sheet-tabs {
  display: flex;
  gap: 4px;
  padding: 6px 8px;
  overflow-x: auto;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  flex-shrink: 0;
}
.sheet-tab {
  background: #21262d;
  border: 1px solid #30363d;
  color: #8b949e;
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  cursor: pointer;
  white-space: nowrap;
}
.sheet-tab.active { color: #58a6ff; border-color: #58a6ff; }
.sheet-table-wrap { flex: 1; overflow: auto; min-height: 0; }
.sheet-table {
  border-collapse: collapse;
  font-size: 12px;
  color: #c9d1d9;
}
.sheet-table td {
  border: 1px solid #30363d;
  padding: 4px 8px;
  white-space: nowrap;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sheet-table tr:first-child td { font-weight: 600; background: #161b22; color: #e6edf3; }
.word-body {
  flex: 1;
  overflow: auto;
  min-height: 0;
  padding: 16px 20px 40px;
  color: #c9d1d9;
  font-size: 14px;
  line-height: 1.65;
}
.word-body :deep(img) { max-width: 100%; }
.word-body :deep(table) { border-collapse: collapse; max-width: 100%; }
.word-body :deep(td), .word-body :deep(th) { border: 1px solid #30363d; padding: 4px 8px; }
.word-body :deep(p) { margin: 0 0 10px; }

.editor-wrap {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.diff-wrap { flex: 1; overflow: auto; min-height: 0; }

/* Rendered markdown. Injected via v-html, so children need :deep(). */
.markdown-body {
  flex: 1;
  overflow: auto;
  min-height: 0;
  padding: 16px 20px 40px;
  color: #c9d1d9;
  font-size: 14px;
  line-height: 1.65;
  word-wrap: break-word;
}
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  margin: 22px 0 12px;
  font-weight: 600;
  line-height: 1.3;
  color: #e6edf3;
}
.markdown-body :deep(h1) { font-size: 1.7em; padding-bottom: 6px; border-bottom: 1px solid #30363d; }
.markdown-body :deep(h2) { font-size: 1.35em; padding-bottom: 5px; border-bottom: 1px solid #30363d; }
.markdown-body :deep(h3) { font-size: 1.15em; }
.markdown-body :deep(h4) { font-size: 1em; }
.markdown-body :deep(h5), .markdown-body :deep(h6) { font-size: 0.9em; color: #8b949e; }
.markdown-body :deep(> *:first-child) { margin-top: 0; }
.markdown-body :deep(p), .markdown-body :deep(ul), .markdown-body :deep(ol) { margin: 0 0 12px; }
.markdown-body :deep(ul), .markdown-body :deep(ol) { padding-left: 26px; }
.markdown-body :deep(li) { margin: 3px 0; }
.markdown-body :deep(li > ul), .markdown-body :deep(li > ol) { margin: 3px 0; }
.markdown-body :deep(a) { color: #58a6ff; text-decoration: none; cursor: pointer; }
.markdown-body :deep(a:hover) { text-decoration: underline; }
.markdown-body :deep(code) {
  background: rgba(110, 118, 129, 0.4);
  padding: 0.2em 0.4em;
  border-radius: 4px;
  font-family: Consolas, 'Courier New', monospace;
  font-size: 0.88em;
}
.markdown-body :deep(pre) {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 6px;
  padding: 12px;
  overflow: auto;
  margin: 0 0 14px;
}
.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
  font-size: 12px;
  line-height: 1.5;
}
.markdown-body :deep(.mermaid-block) { margin: 0 0 14px; }
.markdown-body :deep(.mermaid-diagram) { margin: 16px 0; overflow: auto; text-align: center; }
.markdown-body :deep(.mermaid-diagram svg) { max-width: 100%; height: auto; }
.markdown-body :deep(.mermaid-error) { color: #f85149; white-space: pre-wrap; }
.markdown-body :deep(blockquote) {
  margin: 0 0 14px;
  padding: 0 0 0 14px;
  border-left: 3px solid #30363d;
  color: #8b949e;
}
.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid #30363d;
  margin: 20px 0;
}
.markdown-body :deep(table) {
  border-collapse: collapse;
  margin: 0 0 14px;
  display: block;
  overflow: auto;
  max-width: 100%;
}
.markdown-body :deep(th), .markdown-body :deep(td) {
  border: 1px solid #30363d;
  padding: 6px 12px;
}
.markdown-body :deep(th) { background: #161b22; font-weight: 600; color: #e6edf3; }
.markdown-body :deep(img) { max-width: 100%; }

/* Resize handles */
.rz { position: absolute; z-index: 2; }
.rz-n { top: 0; left: 6px; right: 6px; height: 5px; cursor: ns-resize; }
.rz-s { bottom: 0; left: 6px; right: 6px; height: 5px; cursor: ns-resize; }
.rz-e { right: 0; top: 6px; bottom: 6px; width: 5px; cursor: ew-resize; }
.rz-w { left: 0; top: 6px; bottom: 6px; width: 5px; cursor: ew-resize; }
.rz-ne { top: 0; right: 0; width: 10px; height: 10px; cursor: ne-resize; }
.rz-nw { top: 0; left: 0; width: 10px; height: 10px; cursor: nw-resize; }
.rz-se { bottom: 0; right: 0; width: 12px; height: 12px; cursor: se-resize; }
.rz-sw { bottom: 0; left: 0; width: 10px; height: 10px; cursor: sw-resize; }
</style>
