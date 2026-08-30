<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAcpStore } from '../stores/acp'
import { config } from '../../wailsjs/go/models'

const emit = defineEmits<{ (e: 'close'): void }>()
const store = useAcpStore()
const status = ref('')
const selected = ref(0)

function emptyAgent(): any {
  return {
    id: 'agent-' + Date.now().toString(36),
    name: '新 Agent',
    command: '',
    args: [] as string[],
    env: {} as Record<string, string>,
    remoteCommand: '',
    remoteArgs: [] as string[],
    isDefault: false,
  }
}

const draft = ref<any[]>([])

onMounted(async () => {
  await store.loadAgents()
  draft.value = (store.agents || []).map(a => ({
    id: a.id || ('agent-' + Date.now().toString(36)),
    name: a.name || '',
    command: a.command || '',
    args: [...(a.args || [])],
    env: { ...(a.env || {}) },
    remoteCommand: a.remoteCommand || '',
    remoteArgs: [...(a.remoteArgs || [])],
    isDefault: !!a.isDefault,
  }))
  if (!draft.value.length) draft.value = [emptyAgent()]
})

function addAgent() {
  draft.value.push(emptyAgent())
  selected.value = draft.value.length - 1
}

function removeAgent(i: number) {
  draft.value.splice(i, 1)
  if (!draft.value.length) draft.value = [emptyAgent()]
  selected.value = Math.max(0, Math.min(selected.value, draft.value.length - 1))
}

function setDefault(i: number) {
  draft.value.forEach((a, idx) => { a.isDefault = idx === i })
}

function argsText(a: any) {
  return (a.args || []).join('\n')
}
function setArgsText(a: any, text: string) {
  a.args = text.split(/\r?\n/).map((s: string) => s.trim()).filter(Boolean)
}
function remoteArgsText(a: any) {
  return (a.remoteArgs || []).join('\n')
}
function setRemoteArgsText(a: any, text: string) {
  a.remoteArgs = text.split(/\r?\n/).map((s: string) => s.trim()).filter(Boolean)
}

async function save() {
  const items = draft.value.map(a => new config.AcpAgentConfig({
    id: a.id,
    name: a.name,
    command: a.command,
    args: a.args || [],
    env: a.env || {},
    remoteCommand: a.remoteCommand || '',
    remoteArgs: a.remoteArgs || [],
    isDefault: !!a.isDefault,
  }))
  await store.saveAgents(items as any)
  status.value = '已保存'
}
</script>

<template>
  <div class="modal-overlay" @click.self="emit('close')">
    <div class="modal-content acp-modal">
      <div class="modal-header">
        <div class="header-title">
          <span class="header-icon">✦</span>
          <div>
            <strong>ACP Agent</strong>
            <small>配置本地与远程代理启动方式</small>
          </div>
        </div>
        <button class="btn-close" aria-label="关闭" @click="emit('close')">&times;</button>
      </div>
      <div class="modal-body">
        <aside class="left">
          <div class="sidebar-head">
            <span>代理列表</span>
            <span class="count">{{ draft.length }}</span>
          </div>
          <div class="agent-list">
            <div
              v-for="(a, i) in draft"
              :key="a.id"
              class="item"
              :class="{ active: i === selected }"
              @click="selected = i"
            >
              <span class="agent-dot" :class="{ default: a.isDefault }"></span>
              <span class="agent-name">{{ a.name || '未命名 Agent' }}</span>
              <span v-if="a.isDefault" class="default-mark">默认</span>
              <button class="x" aria-label="删除 Agent" @click.stop="removeAgent(i)">&times;</button>
            </div>
          </div>
          <button class="btn add-btn" @click="addAgent"><span>＋</span> 新增 Agent</button>
        </aside>
        <main v-if="draft[selected]" class="right">
          <div class="editor-heading">
            <div>
              <span class="eyebrow">AGENT PROFILE</span>
              <h2>{{ draft[selected].name || '未命名 Agent' }}</h2>
            </div>
            <span v-if="draft[selected].isDefault" class="default-badge">默认代理</span>
          </div>

          <div class="form-section">
            <div class="section-title"><span>基础信息</span><small>用于识别和启动 Agent</small></div>
            <div class="field">
              <label>名称</label>
              <input v-model="draft[selected].name" placeholder="例如 Claude Code" />
            </div>
            <div class="field">
              <label>本地命令</label>
              <input v-model="draft[selected].command" placeholder="例如 npx 或绝对路径" />
            </div>
            <div class="field">
              <label>本地参数 <small>每行一个</small></label>
              <textarea :value="argsText(draft[selected])" @input="setArgsText(draft[selected], ($event.target as HTMLTextAreaElement).value)" rows="3" placeholder="--config
/path/to/workspace"></textarea>
            </div>
          </div>

          <div class="form-section remote-section">
            <div class="section-title"><span>远程工作区</span><small>可选，留空则复用本地配置</small></div>
            <div class="field">
              <label>远程命令</label>
              <input v-model="draft[selected].remoteCommand" placeholder="远端可执行文件" />
            </div>
            <div class="field">
              <label>远程参数 <small>每行一个</small></label>
              <textarea :value="remoteArgsText(draft[selected])" @input="setRemoteArgsText(draft[selected], ($event.target as HTMLTextAreaElement).value)" rows="2" placeholder="--profile
remote-project"></textarea>
            </div>
          </div>

          <label class="check">
            <input type="checkbox" :checked="draft[selected].isDefault" @change="setDefault(selected)" />
            <span>设为默认 Agent</span>
          </label>
          <div class="hint">本地工作区在本机启动；远程工作区通过 SSH 在远端启动同一套（或覆盖的）命令。</div>
          <div class="actions">
            <span class="status">{{ status }}</span>
            <button class="btn primary" @click="save">保存配置</button>
          </div>
        </main>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(2, 6, 14, .48);
  backdrop-filter: blur(8px);
}
.modal-content {
  width: min(900px, 94vw);
  height: min(650px, 88vh);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: #e6edf3;
  background: rgba(13, 20, 32, .72);
  border: 1px solid rgba(139, 179, 232, .24);
  border-radius: 18px;
  box-shadow: 0 24px 80px rgba(0, 0, 0, .42), 0 0 0 1px rgba(255,255,255,.03) inset;
  backdrop-filter: blur(24px) saturate(130%);
}
.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 22px;
  background: linear-gradient(135deg, rgba(88,166,255,.12), rgba(167,139,250,.08));
  border-bottom: 1px solid rgba(139, 179, 232, .16);
}
.header-title { display: flex; align-items: center; gap: 12px; }
.header-title strong { display: block; color: #f0f6fc; font-size: 16px; letter-spacing: .01em; }
.header-title small { display: block; margin-top: 3px; color: #8b9bb0; font-size: 11px; }
.header-icon {
  display: grid; place-items: center; width: 34px; height: 34px;
  color: #b7d8ff; background: rgba(88,166,255,.15); border: 1px solid rgba(88,166,255,.28);
  border-radius: 10px; font-size: 18px;
}
.btn-close { padding: 4px 8px; color: #8b9bb0; background: transparent; border: 0; cursor: pointer; font-size: 22px; line-height: 1; }
.btn-close:hover { color: #fff; }
.modal-body { display: flex; flex: 1; min-height: 0; }
.left {
  display: flex; flex-direction: column; gap: 12px; width: 236px; padding: 18px 12px;
  background: rgba(5, 11, 20, .28); border-right: 1px solid rgba(139, 179, 232, .14);
}
.sidebar-head { display: flex; align-items: center; justify-content: space-between; padding: 0 8px 4px; color: #aebed1; font-size: 12px; font-weight: 600; }
.count, .default-badge { color: #9ccaff; background: rgba(88,166,255,.12); border: 1px solid rgba(88,166,255,.2); border-radius: 999px; padding: 2px 7px; font-size: 10px; }
.agent-list { display: flex; flex: 1; flex-direction: column; gap: 5px; overflow: auto; }
.item { display: flex; align-items: center; gap: 8px; min-height: 40px; padding: 8px 9px; color: #9aaabd; border: 1px solid transparent; border-radius: 10px; cursor: pointer; font-size: 12px; transition: .15s ease; }
.item:hover { color: #dbeafe; background: rgba(88,166,255,.07); }
.item.active { color: #f0f6fc; background: linear-gradient(100deg, rgba(88,166,255,.18), rgba(167,139,250,.11)); border-color: rgba(120,175,238,.3); box-shadow: 0 5px 18px rgba(0,0,0,.12); }
.agent-dot { width: 7px; height: 7px; flex: 0 0 auto; background: #66758a; border-radius: 50%; }
.agent-dot.default { background: #58a6ff; box-shadow: 0 0 9px #58a6ff; }
.agent-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.default-mark { color: #9ccaff; font-size: 9px; }
.item .x { padding: 1px 4px; color: #718096; background: transparent; border: 0; cursor: pointer; font-size: 16px; line-height: 1; opacity: .5; }
.item:hover .x, .item.active .x { opacity: 1; }
.item .x:hover { color: #ff7b72; }
.add-btn { width: 100%; color: #b9d7f7 !important; border-style: dashed !important; }
.add-btn span { font-size: 16px; vertical-align: -1px; }
.right { flex: 1; min-width: 0; padding: 24px 28px; overflow: auto; }
.editor-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.eyebrow { color: #79c0ff; font-size: 10px; font-weight: 700; letter-spacing: .14em; }
h2 { margin: 5px 0 0; color: #f0f6fc; font-size: 22px; font-weight: 650; }
.form-section { padding: 15px 16px 16px; background: rgba(7, 14, 25, .28); border: 1px solid rgba(139, 179, 232, .13); border-radius: 13px; }
.remote-section { margin-top: 12px; }
.section-title { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; margin-bottom: 12px; color: #d7e5f5; font-size: 12px; font-weight: 600; }
.section-title small, label small { color: #708198; font-size: 10px; font-weight: 400; }
.field { display: flex; flex-direction: column; gap: 6px; margin-top: 10px; }
.field:first-of-type { margin-top: 0; }
label { color: #aebed1; font-size: 11px; }
input, textarea { width: 100%; box-sizing: border-box; color: #e6edf3; background: rgba(1, 7, 16, .54); border: 1px solid rgba(139, 179, 232, .2); border-radius: 8px; padding: 9px 10px; outline: none; font: inherit; font-size: 12px; transition: .15s ease; }
input:focus, textarea:focus { background: rgba(1, 7, 16, .7); border-color: rgba(88,166,255,.7); box-shadow: 0 0 0 3px rgba(88,166,255,.1); }
textarea { resize: vertical; line-height: 1.45; }
input::placeholder, textarea::placeholder { color: #536277; }
.check { display: flex; align-items: center; gap: 8px; margin-top: 16px; color: #c9d7e8; cursor: pointer; }
.check input { width: 14px; height: 14px; accent-color: #58a6ff; }
.hint { margin-top: 10px; color: #718096; font-size: 10px; line-height: 1.5; }
.actions { display: flex; align-items: center; justify-content: flex-end; gap: 12px; margin-top: 20px; }
.btn { padding: 8px 12px; color: #b8c7d9; background: rgba(33, 43, 58, .62); border: 1px solid rgba(139, 179, 232, .2); border-radius: 8px; cursor: pointer; font-size: 12px; transition: .15s ease; }
.btn:hover { color: #f0f6fc; background: rgba(70, 93, 125, .55); border-color: rgba(139, 179, 232, .42); }
.btn.primary { color: #fff; background: linear-gradient(135deg, #287dcc, #6252bb); border-color: rgba(151,196,255,.45); box-shadow: 0 5px 18px rgba(55,112,190,.22); }
.btn.primary:hover { background: linear-gradient(135deg, #3991df, #7564d2); }
.status { margin-right: auto; color: #56d364; font-size: 11px; }
@media (max-width: 680px) {
  .modal-overlay { padding: 10px; }
  .modal-content { height: min(720px, 94vh); }
  .modal-body { flex-direction: column; }
  .left { width: auto; max-height: 160px; border-right: 0; border-bottom: 1px solid rgba(139,179,232,.14); }
  .agent-list { flex-direction: row; overflow-x: auto; }
  .item { min-width: 150px; }
  .right { padding: 18px; }
}
</style>
