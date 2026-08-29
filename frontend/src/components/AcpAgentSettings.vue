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
        <span>ACP Agent 命令</span>
        <button class="btn-close" @click="emit('close')">&times;</button>
      </div>
      <div class="modal-body">
        <div class="left">
          <div
            v-for="(a, i) in draft"
            :key="a.id"
            class="item"
            :class="{ active: i === selected }"
            @click="selected = i"
          >
            <span>{{ a.name || '未命名' }}</span>
            <button class="x" @click.stop="removeAgent(i)">&times;</button>
          </div>
          <button class="btn" @click="addAgent">+ 新增</button>
        </div>
        <div v-if="draft[selected]" class="right">
          <label>名称</label>
          <input v-model="draft[selected].name" />
          <label>本地命令</label>
          <input v-model="draft[selected].command" placeholder="例如 npx 或绝对路径" />
          <label>本地参数（每行一个）</label>
          <textarea :value="argsText(draft[selected])" @input="setArgsText(draft[selected], ($event.target as HTMLTextAreaElement).value)" rows="4"></textarea>
          <label>远程命令（可选，空则用本地命令）</label>
          <input v-model="draft[selected].remoteCommand" placeholder="远端可执行文件" />
          <label>远程参数（每行一个）</label>
          <textarea :value="remoteArgsText(draft[selected])" @input="setRemoteArgsText(draft[selected], ($event.target as HTMLTextAreaElement).value)" rows="3"></textarea>
          <label class="check">
            <input type="checkbox" :checked="draft[selected].isDefault" @change="setDefault(selected)" />
            设为默认
          </label>
          <div class="hint">本地工作区在本机 spawn；远程工作区通过 SSH 在远端启动同一套（或覆盖）命令，cwd 为远程路径。</div>
          <div class="actions">
            <button class="btn primary" @click="save">保存</button>
            <span class="status">{{ status }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-overlay {
  position: fixed; inset: 0; background: rgba(0,0,0,.55);
  display: flex; align-items: center; justify-content: center; z-index: 100;
}
.modal-content {
  width: min(860px, 92vw); height: min(560px, 86vh);
  background: #161618; border: 1px solid #333; border-radius: 8px;
  display: flex; flex-direction: column; overflow: hidden;
}
.modal-header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 14px; border-bottom: 1px solid #333; color: #fff;
}
.btn-close { background: none; border: none; color: #aaa; font-size: 18px; cursor: pointer; }
.modal-body { flex: 1; display: flex; min-height: 0; }
.left {
  width: 220px; border-right: 1px solid #333; padding: 10px; overflow: auto;
  display: flex; flex-direction: column; gap: 6px;
}
.item {
  display: flex; justify-content: space-between; gap: 6px;
  padding: 6px 8px; border-radius: 4px; cursor: pointer; color: #bbb; font-size: 12px;
}
.item.active, .item:hover { background: #2a2a2e; color: #fff; }
.item .x { background: none; border: none; color: #666; cursor: pointer; }
.right {
  flex: 1; padding: 12px 14px; overflow: auto; display: flex; flex-direction: column; gap: 6px;
}
label { font-size: 12px; color: #9aa; margin-top: 4px; }
input, textarea {
  background: #0d1117; border: 1px solid #30363d; color: #e6edf3;
  border-radius: 4px; padding: 6px 8px; font-size: 12px;
}
.check { display: flex; align-items: center; gap: 6px; color: #ccc; }
.hint { font-size: 11px; color: #6e7681; margin-top: 8px; line-height: 1.4; }
.actions { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.btn {
  background: #21262d; border: 1px solid #444; color: #ddd; border-radius: 4px;
  padding: 6px 10px; cursor: pointer; font-size: 12px;
}
.btn.primary { background: #238636; border-color: #238636; color: #fff; }
.status { color: #3fb950; font-size: 12px; }
</style>
