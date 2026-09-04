<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAcpStore, type AcpCommand, type AcpMediaItem, type AcpPlanItem, type AcpTimelineItem } from '../stores/acp'
import { renderMarkdown, renderMermaidBlocks } from '../utils/renderMarkdown'

const props = defineProps<{ sessionId: string }>()
const store = useAcpStore()
const input = ref('')
const listEl = ref<HTMLDivElement | null>(null)
const inputEl = ref<HTMLTextAreaElement | null>(null)
const menuIndex = ref(0)

const tab = computed(() => store.tabs.find(t => t.id === props.sessionId) || null)

/** Bare /token on the last line → slash menu query (null = menu closed). */
const slashQuery = computed(() => {
  const lines = input.value.split('\n')
  const last = lines[lines.length - 1] ?? ''
  const m = last.match(/^\/([^\s]*)$/)
  if (!m) return null
  return m[1].toLowerCase()
})

const filteredCommands = computed(() => {
  const cmds = tab.value?.commands || []
  const q = slashQuery.value
  if (q === null) return [] as AcpCommand[]
  const list = !q ? cmds : cmds.filter(c => c.name.toLowerCase().startsWith(q) || (`/${c.name}`).startsWith('/' + q))
  return list.slice(0, 12)
})

const showMenu = computed(() => slashQuery.value !== null && filteredCommands.value.length > 0)

const menuStyle = ref<Record<string, string>>({ display: 'none' })

function updateMenuPosition() {
  const el = inputEl.value
  if (!el || !showMenu.value) {
    menuStyle.value = { display: 'none' }
    return
  }
  const r = el.getBoundingClientRect()
  const maxH = 240
  const spaceAbove = r.top
  const placeAbove = spaceAbove > 160
  if (placeAbove) {
    menuStyle.value = {
      position: 'fixed',
      left: `${Math.max(8, r.left)}px`,
      width: `${Math.max(200, r.width)}px`,
      bottom: `${Math.max(8, window.innerHeight - r.top + 6)}px`,
      top: 'auto',
      maxHeight: `${Math.min(maxH, Math.max(120, spaceAbove - 12))}px`,
      zIndex: '9999',
      display: 'block',
    }
  } else {
    menuStyle.value = {
      position: 'fixed',
      left: `${Math.max(8, r.left)}px`,
      width: `${Math.max(200, r.width)}px`,
      top: `${r.bottom + 6}px`,
      bottom: 'auto',
      maxHeight: `${Math.min(maxH, Math.max(120, window.innerHeight - r.bottom - 12))}px`,
      zIndex: '9999',
      display: 'block',
    }
  }
}

watch([showMenu, filteredCommands, input], async () => {
  if (menuIndex.value >= filteredCommands.value.length) {
    menuIndex.value = Math.max(0, filteredCommands.value.length - 1)
  }
  await nextTick()
  updateMenuPosition()
})

function onWinChange() {
  updateMenuPosition()
}

onMounted(() => {
  window.addEventListener('resize', onWinChange)
  window.addEventListener('scroll', onWinChange, true)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onWinChange)
  window.removeEventListener('scroll', onWinChange, true)
})

async function scrollToBottom() {
  await nextTick()
  if (listEl.value) listEl.value.scrollTop = listEl.value.scrollHeight
}

watch(
  () => {
    const t = tab.value
    if (!t) return ''
    return t.items.map(it => {
      if (it.kind === 'message') {
        const mediaLen = it.media?.length || 0
        return it.id + it.content.length + ':' + mediaLen + (it.streaming ? '1' : '0')
      }
      if (it.kind === 'thought') return it.id + it.content.length + (it.streaming ? '1' : '0') + (it.expanded === false ? '0' : '1')
      if (it.kind === 'plan') return it.id + it.entries.length + ':' + it.entries.map(e => e.status + e.content.length).join(',')
      return it.id + it.status + (it.output?.length || 0)
    }).join('|')
  },
  async () => {
    await scrollToBottom()
    await nextTick()
    if (listEl.value) {
      try { await renderMermaidBlocks(listEl.value) } catch { /* ignore mermaid errors */ }
    }
  },
)

function isSafeMedia(m: AcpMediaItem | undefined | null): m is AcpMediaItem {
  if (!m?.url) return false
  const u = m.url.trim()
  return /^(https?:|data:image\/|data:audio\/|data:video\/)/i.test(u)
}

function applyCommand(cmd: AcpCommand) {
  const lines = input.value.split('\n')
  const needsArg = !!cmd.inputHint
  lines[lines.length - 1] = needsArg ? `/${cmd.name} ` : `/${cmd.name}`
  input.value = lines.join('\n')
  menuIndex.value = 0
  void nextTick(() => {
    inputEl.value?.focus()
    const len = input.value.length
    inputEl.value?.setSelectionRange(len, len)
  })
}

async function send() {
  if (isRunning.value) return
  const text = input.value
  if (!text.trim()) return
  input.value = ''
  await store.sendPrompt(props.sessionId, text)
}

function onKeydown(e: KeyboardEvent) {
  if (showMenu.value && filteredCommands.value.length) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      menuIndex.value = (menuIndex.value + 1) % filteredCommands.value.length
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      menuIndex.value = (menuIndex.value - 1 + filteredCommands.value.length) % filteredCommands.value.length
      return
    }
    if (e.key === 'Tab') {
      e.preventDefault()
      applyCommand(filteredCommands.value[menuIndex.value])
      return
    }
    // Enter completes only while still on bare /token with no args yet
    if (e.key === 'Enter' && !e.shiftKey && slashQuery.value !== null) {
      const bare = /^\/\S*$/.test(input.value.trim())
      if (bare) {
        e.preventDefault()
        applyCommand(filteredCommands.value[menuIndex.value])
        return
      }
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      // break slash token so menu closes
      input.value = input.value.replace(/\/[^\s]*$/, '')
      return
    }
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void send()
  }
}

function statusClass(status: string) {
  const s = (status || '').toLowerCase()
  if (s.includes('error') || s.includes('fail')) return 'err'
  if (s.includes('complete') || s === 'completed' || s === 'done' || s === 'success') return 'ok'
  if (s.includes('run') || s === 'pending' || s === 'in_progress') return 'run'
  return ''
}

function itemKey(it: AcpTimelineItem) {
  return it.id
}

function toolPreview(it: { input?: string; output?: string }) {
  const s = (it.input || it.output || '').replace(/\s+/g, ' ').trim()
  if (!s) return ''
  return s.length > 120 ? s.slice(0, 120) + '…' : s
}

function optionClass(opt: { optionId: string; name: string; kind?: string }) {
  const k = (opt.kind || '').toLowerCase()
  const n = (opt.name || '').toLowerCase()
  const id = (opt.optionId || '').toLowerCase()
  if (k.includes('reject') || k.includes('deny') || id === 'reject' || n.includes('reject') || n.includes('拒绝')) return 'deny'
  if (k.includes('allow') || id === 'allow' || n.includes('allow') || n.includes('允许')) return 'allow'
  if (isCurrentOption(opt)) return 'current'
  return 'choice'
}

function isCurrentOption(opt: { name: string }) {
  return /\(current\)/i.test(opt.name || '')
}
function planDoneCount(it: AcpPlanItem) {
  return it.entries.filter(e => /complete|done|completed|success/i.test(e.status || '')).length
}

function planStatusClass(status: string) {
  const s = (status || '').toLowerCase()
  if (s.includes('error') || s.includes('fail') || s.includes('cancel')) return 'err'
  if (s.includes('complete') || s === 'completed' || s === 'done' || s === 'success') return 'ok'
  if (s.includes('progress') || s === 'in_progress' || s === 'running' || s === 'active') return 'run'
  return 'pending'
}

function planStatusIcon(status: string) {
  const c = planStatusClass(status)
  if (c === 'ok') return '✓'
  if (c === 'run') return '●'
  if (c === 'err') return '!'
  return '○'
}

async function stopTurn() {
  await store.cancelPrompt(props.sessionId)
}

async function onModeChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value
  await store.setSessionMode(props.sessionId, v)
}

function formatTokens(n: number) {
  if (!n || n < 0) return '0'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k'
  return String(n)
}

const usageLabel = computed(() => {
  const u = tab.value?.usage
  if (!u || (!u.used && !u.size)) return ''
  if (u.size > 0) return `${formatTokens(u.used)} / ${formatTokens(u.size)}`
  return formatTokens(u.used)
})

const usagePct = computed(() => {
  const u = tab.value?.usage
  if (!u || !u.size || u.size <= 0) return 0
  return Math.max(0, Math.min(100, Math.round((u.used / u.size) * 100)))
})

const isRunning = computed(() => {
  const s = (tab.value?.status || '').toLowerCase()
  return s === 'running' || s === 'streaming'
})

/** Latest plan card, pinned above the composer (not inside message stream). */
const currentPlan = computed<AcpPlanItem | null>(() => {
  const items = tab.value?.items
  if (!items?.length) return null
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i]
    if (it.kind === 'plan') return it
  }
  return null
})

const timelineItems = computed(() => (tab.value?.items || []).filter(it => it.kind !== 'plan'))
</script>

<template>
  <div class="acp-panel" v-if="tab">
    <div class="acp-header">
      <div class="acp-meta">
        <span class="acp-title">{{ tab.title }}</span>
        <span class="acp-badge">{{ tab.mode }}</span>
        <span class="acp-status" :data-st="tab.status">{{ tab.status }}</span>
        <span v-if="tab.agent" class="acp-agent">{{ tab.agent }}</span>
        <span
          v-if="usageLabel"
          class="acp-usage"
          :data-level="usagePct >= 90 ? 'high' : usagePct >= 70 ? 'mid' : 'ok'"
          :title="tab.usage?.cost != null ? `cost ${tab.usage.cost}${tab.usage.currency ? ' ' + tab.usage.currency : ''}` : 'context window'"
        >
          <span class="acp-usage-bar" aria-hidden="true"><i :style="{ width: usagePct + '%' }"></i></span>
          <span class="acp-usage-text">{{ usageLabel }}</span>
        </span>
        <span
          v-if="tab.commands.length"
          class="acp-cmds"
          :title="tab.commands.map(c => '/' + c.name).join(' ')"
        >/{{ tab.commands.length }}</span>
        <label v-if="tab.modes.length" class="acp-mode">
          <span class="acp-mode-label">模式</span>
          <select
            class="acp-mode-select"
            :value="tab.currentModeId"
            :disabled="isRunning"
            @change="onModeChange"
          >
            <option v-if="!tab.currentModeId" value="" disabled>选择模式</option>
            <option v-for="m in tab.modes" :key="m.id" :value="m.id" :title="m.description || m.name || m.id">
              {{ m.name || m.id }}
            </option>
          </select>
        </label>
        <button
          v-if="isRunning"
          type="button"
          class="acp-stop-btn"
          title="终止当前回合"
          @click="stopTurn"
        >终止</button>
      </div>
      <div class="acp-cwd" :title="tab.cwd">{{ tab.cwd }}</div>
    </div>

    <div ref="listEl" class="acp-messages">
      <div v-if="tab.items.length === 0" class="acp-empty">
        向 Agent 发送消息开始对话
        <div v-if="tab.commands.length" class="acp-empty-hint">输入 <kbd>/</kbd> 选择斜杠命令（{{ tab.commands.length }}）</div>
      </div>

      <template v-for="it in timelineItems" :key="itemKey(it)">
        <!-- chat bubble -->
        <div v-if="it.kind === 'message'" class="acp-msg" :class="[it.role, { streaming: it.streaming }]">
          <div class="acp-msg-head">
            <span class="acp-avatar" :class="it.role" aria-hidden="true">{{ it.role === 'user' ? '你' : it.role === 'assistant' ? 'AI' : '!' }}</span>
            <div class="acp-role">{{ it.role === 'user' ? '你' : it.role === 'assistant' ? 'Agent' : '系统' }}</div>
          </div>
          <div
            v-if="it.role === 'assistant'"
            class="acp-bubble md"
          >
            <div v-if="it.content" class="acp-md-body" v-html="renderMarkdown(it.content)"></div>
            <div v-if="it.media?.length" class="acp-media">
              <template v-for="(m, mi) in it.media" :key="mi">
                <a
                  v-if="isSafeMedia(m) && m.kind === 'image'"
                  class="acp-media-item image"
                  :href="m.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <img :src="m.url" :alt="m.alt || m.name || 'image'" loading="lazy" />
                </a>
                <video
                  v-else-if="isSafeMedia(m) && m.kind === 'video'"
                  class="acp-media-item video"
                  :src="m.url"
                  controls
                  preload="metadata"
                />
                <audio
                  v-else-if="isSafeMedia(m) && m.kind === 'audio'"
                  class="acp-media-item audio"
                  :src="m.url"
                  controls
                  preload="metadata"
                />
                <a
                  v-else-if="isSafeMedia(m)"
                  class="acp-media-item file"
                  :href="m.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >{{ m.name || m.alt || m.url }}</a>
              </template>
            </div>
          </div>
          <div v-else-if="it.role === 'user'" class="acp-bubble user-bubble">
            <div v-if="it.content" class="acp-user-text">{{ it.content }}</div>
            <div v-if="it.media?.length" class="acp-media">
              <template v-for="(m, mi) in it.media" :key="mi">
                <a
                  v-if="isSafeMedia(m) && m.kind === 'image'"
                  class="acp-media-item image"
                  :href="m.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <img :src="m.url" :alt="m.alt || m.name || 'image'" loading="lazy" />
                </a>
                <video
                  v-else-if="isSafeMedia(m) && m.kind === 'video'"
                  class="acp-media-item video"
                  :src="m.url"
                  controls
                  preload="metadata"
                />
                <audio
                  v-else-if="isSafeMedia(m) && m.kind === 'audio'"
                  class="acp-media-item audio"
                  :src="m.url"
                  controls
                  preload="metadata"
                />
                <a
                  v-else-if="isSafeMedia(m)"
                  class="acp-media-item file"
                  :href="m.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >{{ m.name || m.alt || m.url }}</a>
              </template>
            </div>
          </div>
          <div v-else class="acp-note">{{ it.content }}</div>
        </div>


        <!-- thinking / thought stream -->
        <div
          v-else-if="it.kind === 'thought'"
          class="acp-thought"
          :class="{ open: it.expanded !== false, streaming: it.streaming }"
        >
          <button
            type="button"
            class="acp-thought-head"
            :aria-expanded="it.expanded !== false ? 'true' : 'false'"
            @click="store.toggleThought(tab.id, it.id)"
          >
            <span class="acp-thought-chevron" aria-hidden="true">{{ it.expanded !== false ? '▼' : '▶' }}</span>
            <span class="acp-thought-icon" aria-hidden="true">💭</span>
            <span class="acp-thought-title">Thinking</span>
            <span v-if="it.streaming" class="acp-thought-live">streaming</span>
          </button>
          <div v-show="it.expanded !== false" class="acp-thought-body">
            <pre class="acp-thought-text">{{ it.content }}</pre>
          </div>
        </div>

        <!-- tool card (always in timeline; click header to expand/collapse) -->
        <div
          v-else-if="it.kind === 'tool'"
          class="acp-tool"
          :class="[statusClass(it.status), { open: it.expanded }]"
        >
          <button
            type="button"
            class="acp-tool-head"
            :aria-expanded="it.expanded ? 'true' : 'false'"
            @click="store.toggleTool(tab.id, it.id)"
          >
            <span class="acp-tool-chevron" aria-hidden="true">{{ it.expanded ? '▼' : '▶' }}</span>
            <span class="acp-tool-icon" aria-hidden="true">⚒</span>
            <span class="acp-tool-name">{{ it.name || 'tool' }}</span>
            <span v-if="it.toolKind" class="acp-tool-kind">{{ it.toolKind }}</span>
            <span class="acp-tool-status">{{ it.status || 'pending' }}</span>
          </button>
          <div v-if="!it.expanded && toolPreview(it)" class="acp-tool-preview">{{ toolPreview(it) }}</div>
          <div v-show="it.expanded" class="acp-tool-body">
            <div v-if="it.input" class="acp-tool-block">
              <div class="lbl">input</div>
              <pre>{{ it.input }}</pre>
            </div>
            <div v-if="it.output" class="acp-tool-block">
              <div class="lbl">output</div>
              <pre>{{ it.output }}</pre>
            </div>
            <div v-if="!it.input && !it.output" class="acp-tool-empty">暂无详情</div>
          </div>
        </div>
      </template>
    </div>

    <div v-if="tab.error" class="acp-error">{{ tab.error }}</div>

    <div v-if="tab.permission" class="acp-perm">
      <div class="acp-perm-title">需要授权 · {{ tab.permission.toolName }}</div>
      <div v-if="tab.permission.prompt" class="acp-perm-prompt">{{ tab.permission.prompt }}</div>
      <pre
        v-if="tab.permission.toolInput && tab.permission.toolInput !== tab.permission.prompt"
        class="acp-perm-body"
      >{{ tab.permission.toolInput }}</pre>
      <div v-if="tab.permission.options.length" class="acp-perm-options">
        <button
          v-for="opt in tab.permission.options"
          :key="opt.optionId"
          type="button"
          class="acp-opt"
          @click="store.respondPermission(tab.id, tab.permission.requestId, opt.optionId)"
        >
          <span>{{ opt.name || opt.optionId }}</span>
          <span v-if="opt.kind" class="acp-opt-kind">{{ opt.kind }}</span>
        </button>
      </div>
      <div v-else class="acp-perm-actions">
        <button
          type="button"
          class="btn allow"
          @click="store.respondPermissionAllow(tab.id, tab.permission.requestId, true)"
        >允许</button>
        <button
          type="button"
          class="btn deny"
          @click="store.respondPermissionAllow(tab.id, tab.permission.requestId, false)"
        >拒绝</button>
      </div>
    </div>

    <div v-if="currentPlan" class="acp-plan-dock">
      <div
        class="acp-plan"
        :class="{ open: currentPlan.expanded !== false }"
      >
        <button
          type="button"
          class="acp-plan-head"
          :aria-expanded="currentPlan.expanded !== false ? 'true' : 'false'"
          @click="store.togglePlan(tab.id, currentPlan.id)"
        >
          <span class="acp-plan-chevron" aria-hidden="true">{{ currentPlan.expanded !== false ? '▼' : '▶' }}</span>
          <span class="acp-plan-icon" aria-hidden="true">☰</span>
          <span class="acp-plan-title">Todos</span>
          <span class="acp-plan-progress">{{ planDoneCount(currentPlan) }}/{{ currentPlan.entries.length }}</span>
        </button>
        <div v-show="currentPlan.expanded !== false" class="acp-plan-body">
          <div
            v-for="(e, idx) in currentPlan.entries"
            :key="idx"
            class="acp-plan-item"
            :data-st="planStatusClass(e.status)"
            :data-pri="(e.priority || '').toLowerCase() || undefined"
          >
            <span class="acp-plan-mark" aria-hidden="true">{{ planStatusIcon(e.status) }}</span>
            <span class="acp-plan-text">{{ e.content }}</span>
            <span v-if="e.priority" class="acp-plan-pri">{{ e.priority }}</span>
            <span class="acp-plan-st">{{ e.status || 'pending' }}</span>
          </div>
          <div v-if="!currentPlan.entries.length" class="acp-plan-empty">暂无任务</div>
        </div>
      </div>
    </div>

    <div class="acp-input-row">
      <textarea
        ref="inputEl"
        v-model="input"
        rows="2"
        placeholder="输入消息或 / 命令 · Enter 发送 · Shift+Enter 换行"
        @keydown="onKeydown"
        @input="updateMenuPosition"
        @focus="updateMenuPosition"
      ></textarea>
      <button
        v-if="isRunning"
        class="send-btn stop"
        type="button"
        title="停止当前回合"
        @click="stopTurn"
      >停止</button>
      <button
        v-else
        class="send-btn"
        type="button"
        :disabled="!input.trim()"
        @click="send"
      >发送</button>
    </div>

    <!-- Escape parent overflow:hidden via Teleport -->
    <Teleport to="body">
      <div
        v-if="showMenu"
        class="acp-slash-menu-portal"
        :style="menuStyle"
        role="listbox"
      >
        <button
          v-for="(cmd, i) in filteredCommands"
          :key="cmd.name"
          type="button"
          class="acp-slash-item"
          :class="{ active: i === menuIndex }"
          @mousedown.prevent="applyCommand(cmd)"
        >
          <span class="name">/{{ cmd.name }}</span>
          <span class="desc">{{ cmd.description || cmd.inputHint || '' }}</span>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.acp-panel {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--surface-app, #0d1117);
  color: #ddd;
  overflow: hidden;
}
.acp-header {
  padding: 8px 12px;
  border-bottom: 1px solid #30363d;
  flex-shrink: 0;
}
.acp-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  font-size: 12px;
}
.acp-title { font-weight: 600; color: #fff; }
.acp-badge, .acp-status, .acp-agent, .acp-cmds {
  color: #8b949e;
  background: #21262d;
  border-radius: 999px;
  padding: 1px 8px;
  font-size: 11px;
}
.acp-status[data-st="ready"] { color: #3fb950; }
.acp-status[data-st="running"] { color: #d29922; }
.acp-status[data-st="error"] { color: #ff7b72; }
.acp-cmds { color: #79c0ff; }
.acp-cwd {
  margin-top: 4px;
  font-size: 11px;
  color: #6e7681;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.acp-messages {
  flex: 1;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding: 12px 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  background: transparent;
}
.acp-empty {
  color: #6e7681;
  font-size: 13px;
  margin: auto;
  text-align: center;
  line-height: 1.6;
}
.acp-empty-hint { margin-top: 6px; font-size: 12px; }
.acp-empty kbd {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 4px;
  padding: 0 5px;
  color: #79c0ff;
}
.acp-msg {
  max-width: 100%;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  box-sizing: border-box;
}
.acp-msg.user {
  align-self: flex-end;
  width: min(100%, 560px);
  margin-left: auto;
}
.acp-msg.assistant,
.acp-msg.system {
  align-self: flex-start;
  width: 90%;
  max-width: 90%;
}
.acp-msg-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.acp-msg.user .acp-msg-head {
  justify-content: flex-end;
  flex-direction: row-reverse;
}
.acp-avatar {
  width: 22px;
  height: 22px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.02em;
  flex-shrink: 0;
  border: 1px solid transparent;
}
.acp-avatar.user {
  color: #dbeafe;
  background: linear-gradient(180deg, #2563eb 0%, #1d4ed8 100%);
  border-color: #3b82f680;
  box-shadow: 0 0 0 1px rgba(37, 99, 235, 0.18);
}
.acp-avatar.assistant {
  color: #dcfce7;
  background: linear-gradient(180deg, #238636 0%, #196c2e 100%);
  border-color: #3fb95080;
}
.acp-avatar.system {
  color: #f3e8ff;
  background: linear-gradient(180deg, #7e22ce 0%, #6b21a8 100%);
  border-color: #a855f780;
}
.acp-role {
  font-size: 11px;
  letter-spacing: 0.02em;
  color: #8b949e;
  font-weight: 600;
}
.acp-msg.user .acp-role { color: #79c0ff; }
.acp-msg.assistant .acp-role { color: #3fb950; }
.acp-msg.system .acp-role { color: #d2a8ff; }
.acp-bubble {
  border-radius: 12px;
  padding: 10px 12px;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
  word-break: break-word;
  min-width: 0;
  width: 100%;
  max-width: 100%;
  box-sizing: border-box;
  overflow-x: hidden;
  box-shadow: 0 1px 0 rgba(255,255,255,0.03) inset;
}
.acp-msg.user .user-bubble {
  background: linear-gradient(180deg, #1a2740 0%, #152033 100%);
  border: 1px solid #2f4b73;
  color: #e6edf3;
  border-radius: 12px 12px 4px 12px;
  box-shadow:
    0 0 0 1px rgba(56, 139, 253, 0.08),
    0 8px 20px rgba(0, 0, 0, 0.18);
}
.acp-user-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  margin: 0;
  color: #e6edf3;
}
.acp-msg.assistant .acp-bubble.md {
  background: rgba(22, 27, 34, .78);
  border: 1px solid rgba(139, 179, 232, .18);
  border-radius: 12px 12px 12px 4px;
  box-shadow: 0 1px 0 rgba(255,255,255,.03) inset, 0 8px 20px rgba(0,0,0,.12);
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(p),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(li),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h1),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h2),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h3),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h4),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h5),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(h6),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(a),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(th),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(td),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(blockquote) {
  overflow-wrap: anywhere;
  word-break: break-word;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(> *:first-child) { margin-top: 0; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(> *:last-child) { margin-bottom: 0; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(p),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(ul),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(ol) { margin: 0 0 8px; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(ul),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(ol) { padding-left: 1.35em; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(li) { margin: 2px 0; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(li > ul),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(li > ol) { margin: 2px 0; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(blockquote) {
  margin: 0 0 8px;
  padding: 4px 0 4px 10px;
  border-left: 3px solid rgba(139, 179, 232, .28);
  color: #9da7b3;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(pre) {
  width: auto;
  max-width: 100%;
  box-sizing: border-box;
  overflow-x: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  padding: 8px 10px;
  margin: 0 0 8px;
  border-radius: 8px;
  background: rgba(1, 6, 14, .45);
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(pre code),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(pre code.hljs) {
  display: block;
  width: auto;
  max-width: none;
  padding: 0;
  background: transparent;
  white-space: inherit;
  overflow-wrap: inherit;
  word-break: inherit;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(code) {
  overflow-wrap: anywhere;
  word-break: break-word;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(table) {
  display: block;
  width: max-content;
  max-width: 100%;
  overflow-x: auto;
  margin: 0 0 8px;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(img),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(svg),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(video) {
  max-width: 100%;
  height: auto;
}
.acp-msg.assistant.streaming .acp-bubble.md {
  border-color: #238636;
}

.acp-md-body { min-width: 0; }
.acp-media {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
  min-width: 0;
}
.acp-md-body + .acp-media { margin-top: 10px; }
.acp-user-text + .acp-media { margin-top: 10px; }
.acp-media-item {
  max-width: 100%;
  border-radius: 8px;
  overflow: hidden;
}
.acp-media-item.image {
  display: block;
  line-height: 0;
  border: 1px solid rgba(139, 179, 232, 0.22);
  background: rgba(1, 6, 14, 0.35);
}
.acp-media-item.image img {
  display: block;
  max-width: 100%;
  max-height: min(420px, 55vh);
  width: auto;
  height: auto;
  object-fit: contain;
  margin: 0 auto;
}
.acp-media-item.video {
  display: block;
  width: 100%;
  max-height: min(420px, 55vh);
  background: #010409;
  border: 1px solid #30363d;
}
.acp-media-item.audio {
  display: block;
  width: 100%;
}
.acp-media-item.file {
  display: inline-block;
  font-size: 12px;
  color: #79c0ff;
  text-decoration: none;
  word-break: break-all;
  padding: 4px 0;
}
.acp-media-item.file:hover { text-decoration: underline; }
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(.mermaid-block),
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(.mermaid-diagram) {
  max-width: 100%;
  overflow-x: auto;
  margin: 0 0 8px;
}
.acp-msg.assistant .acp-bubble.md .acp-md-body :deep(.mermaid-diagram svg) {
  max-width: 100%;
  height: auto;
}
.acp-note {
  font-size: 12px;
  color: #8b949e;
  white-space: pre-wrap;
  padding: 2px 4px;
  border-left: 2px solid #30363d;
  margin-left: 2px;
}
.acp-tool {
  border: 1px solid #3d444d;
  background: #12161c;
  border-radius: 10px;
  overflow: hidden;
  flex-shrink: 0;
  box-shadow: 0 1px 0 rgba(255,255,255,0.04) inset;
}
.acp-tool.open {
  border-color: #5882b0;
  background: #141b24;
}
.acp-tool.ok { border-color: #2ea04388; }
.acp-tool.err { border-color: #f8514988; }
.acp-tool.run { border-color: #d2992288; }
.acp-tool-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 12px;
  border: 0;
  margin: 0;
  background: linear-gradient(180deg, #1c2330 0%, #161b22 100%);
  color: #e6edf3;
  cursor: pointer;
  font-size: 12px;
  text-align: left;
  box-sizing: border-box;
  appearance: none;
  -webkit-appearance: none;
}
.acp-tool-head:hover { filter: brightness(1.08); }
.acp-tool-head:focus-visible {
  outline: 2px solid #388bfd;
  outline-offset: -2px;
}
.acp-tool-chevron {
  color: #9be9a8;
  width: 14px;
  flex-shrink: 0;
  font-size: 10px;
  line-height: 1;
}
.acp-tool-icon {
  color: #79c0ff;
  flex-shrink: 0;
  font-size: 13px;
  line-height: 1;
}
.acp-tool-name {
  font-weight: 650;
  color: #f0f3f6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.acp-tool-kind {
  color: #9da7b3;
  font-size: 11px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 999px;
  padding: 1px 8px;
  flex-shrink: 0;
}
.acp-tool-status {
  margin-left: auto;
  font-size: 11px;
  font-weight: 600;
  color: #9da7b3;
  text-transform: lowercase;
  flex-shrink: 0;
  background: #0d1117;
  border-radius: 999px;
  padding: 2px 8px;
  border: 1px solid #30363d;
}
.acp-tool.ok .acp-tool-status { color: #3fb950; border-color: #23863666; }
.acp-tool.err .acp-tool-status { color: #ff7b72; border-color: #da363366; }
.acp-tool.run .acp-tool-status { color: #e3b341; border-color: #d2992266; }
.acp-tool-preview {
  padding: 0 12px 8px 34px;
  font-size: 11px;
  line-height: 1.4;
  color: #8b949e;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  border-top: none;
}
.acp-tool-body {
  padding: 10px 12px 12px;
  border-top: 1px solid #30363d;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: #0d1117;
}
.acp-tool-block .lbl {
  font-size: 10px;
  color: #8b949e;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  margin-bottom: 4px;
}
.acp-tool-block pre {
  margin: 0;
  padding: 8px 10px;
  background: #010409;
  border: 1px solid #30363d;
  border-radius: 6px;
  font-size: 11px;
  line-height: 1.45;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 260px;
  overflow: auto;
  color: #c9d1d9;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.acp-tool-empty { font-size: 12px; color: #6e7681; padding: 4px 0; }
.acp-perm {
  margin: 0 10px 8px;
  padding: 8px 10px;
  border: 1px solid #6e4b1f;
  background: #2a2112;
  border-radius: 6px;
  flex-shrink: 0;
}
.acp-perm-title { font-size: 12px; color: #e3b341; margin-bottom: 4px; }
.acp-perm-body {
  font-size: 11px;
  color: #d2c59a;
  white-space: pre-wrap;
  margin: 0 0 8px;
  max-height: 120px;
  overflow: auto;
}
.acp-perm-prompt {
  font-size: 12px;
  color: #e3b341;
  margin-bottom: 8px;
  white-space: pre-wrap;
}
.acp-perm-options {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 280px;
  overflow: auto;
  margin-bottom: 4px;
}
.acp-opt {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  text-align: left;
  border: 1px solid #4a3b1f;
  background: #1c160c;
  color: #e6edf3;
  border-radius: 6px;
  padding: 8px 10px;
  cursor: pointer;
  font-size: 12px;
}
.acp-opt:hover { border-color: #d29922; background: #2a2112; }
.acp-opt.choice { border-color: #3d4450; background: #161b22; }
.acp-opt.choice:hover { border-color: #58a6ff; background: #1f2937; }
.acp-opt.current { border-color: #388bfd; }
.acp-opt.allow { border-color: #238636; color: #3fb950; }
.acp-opt.deny { border-color: #da3633; color: #ff7b72; }
.acp-opt .opt-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.acp-opt .opt-cur {
  font-size: 10px;
  color: #58a6ff;
  background: #0d419d44;
  border-radius: 4px;
  padding: 1px 6px;
}
.acp-perm-actions { display: flex; gap: 8px; }
.btn {
  border: 1px solid #444;
  background: #21262d;
  color: #ddd;
  border-radius: 4px;
  padding: 4px 10px;
  cursor: pointer;
}
.btn.allow { border-color: #238636; color: #3fb950; }
.btn.deny { border-color: #da3633; color: #ff7b72; }
.acp-error {
  padding: 6px 12px;
  color: #ff7b72;
  font-size: 12px;
  border-top: 1px solid #4d1f1f;
  background: #2a1212;
  flex-shrink: 0;
}
.acp-input-row {
  display: flex;
  gap: 8px;
  padding: 10px;
  border-top: 1px solid #30363d;
  flex-shrink: 0;
  background: #0d1117;
}
.acp-input-row textarea {
  flex: 1;
  resize: none;
  background: #010409;
  color: #e6edf3;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 8px 10px;
  font-family: inherit;
  font-size: 13px;
  line-height: 1.4;
}
.acp-input-row textarea:focus {
  outline: none;
  border-color: #388bfd;
}
.send-btn {
  min-width: 64px;
  border: none;
  border-radius: 8px;
  background: #238636;
  color: #fff;
  cursor: pointer;
  font-weight: 600;
}
.send-btn.stop {
  background: #da3633;
}
.send-btn.stop:hover {
  background: #f85149;
}
.send-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.acp-usage {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid #30363d;
  background: #161b22;
  color: #8b949e;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.acp-usage[data-level="mid"] { border-color: #9e6a03; color: #d4a72c; }
.acp-usage[data-level="high"] { border-color: #f85149; color: #ff7b72; }
.acp-usage-bar {
  display: inline-block;
  width: 36px;
  height: 4px;
  border-radius: 999px;
  background: #21262d;
  overflow: hidden;
}
.acp-usage-bar > i {
  display: block;
  height: 100%;
  background: #58a6ff;
}
.acp-usage[data-level="mid"] .acp-usage-bar > i { background: #d4a72c; }
.acp-usage[data-level="high"] .acp-usage-bar > i { background: #f85149; }
.acp-thought {
  border: 1px solid #3d3a55;
  background: #14121c;
  border-radius: 10px;
  overflow: hidden;
  flex-shrink: 0;
  box-shadow: 0 1px 0 rgba(255,255,255,0.03) inset;
}
.acp-thought.streaming {
  border-color: #6e5cb8;
}
.acp-thought-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border: 0;
  background: transparent;
  color: #c9b8ff;
  cursor: pointer;
  text-align: left;
  font-size: 12px;
}
.acp-thought-head:hover { background: rgba(110, 92, 184, 0.12); }
.acp-thought-chevron { color: #a899e6; width: 12px; }
.acp-thought-title { font-weight: 600; }
.acp-thought-live {
  margin-left: auto;
  font-size: 10px;
  color: #8b949e;
}
.acp-thought-body {
  padding: 0 10px 10px 30px;
}
.acp-thought-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  color: #b1a7c7;
  font-size: 12px;
  line-height: 1.45;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>

<!-- portal menu is outside scoped tree -->
<style>
.acp-slash-menu-portal {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 10px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.55);
  padding: 4px;
  overflow: auto;
  box-sizing: border-box;
}
.acp-slash-menu-portal .acp-slash-item {
  display: flex;
  gap: 10px;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  color: #e6edf3;
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
  align-items: baseline;
}
.acp-slash-menu-portal .acp-slash-item:hover,
.acp-slash-menu-portal .acp-slash-item.active {
  background: #21262d;
}
.acp-slash-menu-portal .acp-slash-item .name {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: #79c0ff;
  min-width: 96px;
  flex-shrink: 0;
}
.acp-slash-menu-portal .acp-slash-item .desc {
  color: #8b949e;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.acp-mode {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 4px;
}
.acp-mode-label {
  font-size: 11px;
  color: #8b949e;
}
.acp-mode-select {
  appearance: none;
  background: #161b22;
  color: #e6edf3;
  border: 1px solid #30363d;
  border-radius: 6px;
  padding: 2px 22px 2px 8px;
  font-size: 12px;
  line-height: 1.4;
  max-width: 140px;
  background-image: linear-gradient(45deg, transparent 50%, #8b949e 50%), linear-gradient(135deg, #8b949e 50%, transparent 50%);
  background-position: calc(100% - 12px) 55%, calc(100% - 7px) 55%;
  background-size: 5px 5px, 5px 5px;
  background-repeat: no-repeat;
  cursor: pointer;
}
.acp-mode-select:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.acp-stop-btn,
.acp-stop-inline {
  border: 1px solid #f8514966;
  background: #3d1214;
  color: #ffb4b4;
  border-radius: 6px;
  padding: 2px 10px;
  font-size: 12px;
  cursor: pointer;
  margin-left: 6px;
}
.acp-stop-btn:hover,
.acp-stop-inline:hover {
  background: #5a1a1d;
  border-color: #f85149aa;
}
.acp-plan-dock {
  flex-shrink: 0;
  padding: 8px 10px 0;
  border-top: 1px solid #30363d;
  background: linear-gradient(180deg, rgba(13,17,23,0.2), rgba(13,17,23,0.85));
}
.acp-plan-dock .acp-plan {
  margin: 0;
  max-height: min(40vh, 320px);
  display: flex;
  flex-direction: column;
}
.acp-plan-dock .acp-plan-body {
  overflow: auto;
  max-height: min(32vh, 260px);
}
.acp-plan {
  border: 1px solid #3d444d;
  background: #12161c;
  border-radius: 10px;
  overflow: hidden;
  flex-shrink: 0;
  box-shadow: 0 1px 0 rgba(255,255,255,0.04) inset;
}
.acp-plan.open {
  border-color: #5882b0;
  background: #141b24;
}
.acp-plan-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  background: transparent;
  border: 0;
  color: #e6edf3;
  cursor: pointer;
  text-align: left;
}
.acp-plan-chevron {
  color: #8b949e;
  font-size: 10px;
  width: 12px;
}
.acp-plan-title {
  font-weight: 600;
  font-size: 13px;
}
.acp-plan-count {
  margin-left: auto;
  font-size: 11px;
  color: #8b949e;
  background: #21262d;
  border-radius: 999px;
  padding: 1px 8px;
}
.acp-plan-list {
  list-style: none;
  margin: 0;
  padding: 0 10px 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.acp-plan-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 8px;
  background: #0d1117;
  border: 1px solid #30363d;
  font-size: 12px;
  line-height: 1.45;
}
.acp-plan-item[data-st="ok"] {
  border-color: #2ea04366;
  background: #0f1a12;
}
.acp-plan-item[data-st="run"] {
  border-color: #d2992266;
  background: #1a160c;
}
.acp-plan-item[data-st="err"] {
  border-color: #f8514966;
  background: #1a0f10;
}
.acp-plan-icon {
  flex-shrink: 0;
  width: 16px;
  text-align: center;
  margin-top: 1px;
  color: #8b949e;
}
.acp-plan-item[data-st="ok"] .acp-plan-icon { color: #3fb950; }
.acp-plan-item[data-st="run"] .acp-plan-icon { color: #d29922; }
.acp-plan-item[data-st="err"] .acp-plan-icon { color: #f85149; }
.acp-plan-text {
  flex: 1;
  min-width: 0;
  color: #e6edf3;
  white-space: pre-wrap;
  word-break: break-word;
}
.acp-plan-item[data-st="ok"] .acp-plan-text {
  color: #8b949e;
  text-decoration: line-through;
}
.acp-plan-pri {
  flex-shrink: 0;
  font-size: 10px;
  color: #a5b4c4;
  background: #21262d;
  border-radius: 4px;
  padding: 0 5px;
}
.acp-plan-st {
  flex-shrink: 0;
  font-size: 10px;
  color: #8b949e;
  text-transform: lowercase;
}
.acp-plan-empty {
  color: #8b949e;
  font-size: 12px;
  padding: 4px 2px;
}

</style>
