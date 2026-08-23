<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { GetProjectRunCommands, SaveProjectRunCommands } from '../../wailsjs/go/main/App'
import { config } from '../../wailsjs/go/models'
import { useWorkspaceStore } from '../stores/workspace'

const emit = defineEmits<{ (e: 'close'): void }>()
const ws = useWorkspaceStore()
const commands = ref<config.ProjectRunCommand[]>([])
const editing = ref<{ idx: number; name: string; command: string } | null>(null)
const newName = ref('')
const newCommand = ref('')
const error = ref('')
const projectName = computed(() => ws.info?.name || '当前项目')

onMounted(async () => {
  commands.value = (await GetProjectRunCommands()) || []
})

async function save() {
  error.value = ''
  try {
    await SaveProjectRunCommands(commands.value)
  } catch (e: any) {
    error.value = e?.message || String(e)
  }
}

function startAdd() {
  editing.value = { idx: -1, name: '', command: '' }
  newName.value = ''
  newCommand.value = ''
}

function startEdit(idx: number) {
  const c = commands.value[idx]
  editing.value = { idx, name: c.name, command: c.command }
  newName.value = c.name
  newCommand.value = c.command
}

function cancelEdit() {
  editing.value = null
}

async function confirmEdit() {
  if (!editing.value) return
  if (!newName.value.trim() || !newCommand.value.trim()) return
  const entry = new config.ProjectRunCommand({
    name: newName.value.trim(),
    command: newCommand.value.trim(),
  })
  if (editing.value.idx >= 0) {
    commands.value[editing.value.idx] = entry
  } else {
    commands.value.push(entry)
  }
  await save()
  editing.value = null
}

async function removeCmd(idx: number) {
  commands.value.splice(idx, 1)
  await save()
}
</script>

<template>
  <div class="modal-overlay" @click.self="emit('close')">
    <div class="modal-content">
      <div class="modal-header">
        <div class="title-wrap">
          <span>项目运行命令</span>
          <span class="project-name">{{ projectName }}</span>
        </div>
        <button class="btn-close" @click="emit('close')">&times;</button>
      </div>
      <div class="modal-body">
        <div class="hint">这些命令只作用于当前项目，不会影响其他工作区。</div>
        <div v-if="error" class="error">{{ error }}</div>
        <div v-if="commands.length === 0 && !editing" class="empty-hint">
          暂无命令，点击下方按钮添加，例如 npm run dev / go run .
        </div>
        <div v-for="(cmd, i) in commands" :key="i" class="cmd-row">
          <template v-if="editing?.idx === i">
            <input v-model="newName" placeholder="名称" class="input-sm" />
            <input v-model="newCommand" placeholder="命令" class="input-sm flex-1" />
            <button class="btn-sm btn-primary" @click="confirmEdit">保存</button>
            <button class="btn-sm" @click="cancelEdit">取消</button>
          </template>
          <template v-else>
            <span class="cmd-name">{{ cmd.name }}</span>
            <code class="cmd-preview">{{ cmd.command }}</code>
            <button class="btn-sm" @click="startEdit(i)">编辑</button>
            <button class="btn-sm btn-danger" @click="removeCmd(i)">删除</button>
          </template>
        </div>
        <div v-if="editing?.idx === -1" class="cmd-row">
          <input v-model="newName" placeholder="名称" class="input-sm" />
          <input v-model="newCommand" placeholder="命令" class="input-sm flex-1" />
          <button class="btn-sm btn-primary" @click="confirmEdit">添加</button>
          <button class="btn-sm" @click="cancelEdit">取消</button>
        </div>
      </div>
      <div class="modal-footer">
        <button v-if="!editing" class="btn" @click="startAdd">+ 添加命令</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-overlay {
  position: fixed; inset: 0; background: rgba(0,0,0,0.5);
  display: flex; align-items: center; justify-content: center; z-index: 100;
}
.modal-content {
  background: #1a1a1e; border: 1px solid #3a3a3e; border-radius: 8px;
  width: 600px; max-height: 70vh; display: flex; flex-direction: column;
}
.modal-header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 12px 16px; border-bottom: 1px solid #2a2a2e;
}
.title-wrap { display: flex; flex-direction: column; gap: 2px; }
.title-wrap > span:first-child { font-size: 14px; font-weight: 600; }
.project-name { font-size: 12px; color: #8b949e; }
.modal-body { padding: 12px 16px; overflow-y: auto; flex: 1; }
.modal-footer { padding: 10px 16px; border-top: 1px solid #2a2a2e; }
.hint { color: #8b949e; font-size: 12px; margin-bottom: 10px; }
.error { color: #f85149; font-size: 12px; margin-bottom: 8px; }
.empty-hint { color: #666; text-align: center; padding: 20px 0; font-size: 13px; }
.cmd-row {
  display: flex; align-items: center; gap: 8px; padding: 6px 0;
  border-bottom: 1px solid #222; font-size: 13px;
}
.cmd-name { min-width: 90px; color: #ccc; font-weight: 500; }
.cmd-preview {
  flex: 1; color: #8a8; font-size: 12px; background: #111;
  padding: 2px 6px; border-radius: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.input-sm {
  background: #111; border: 1px solid #3a3a3e; color: #ddd;
  padding: 4px 8px; border-radius: 4px; font-size: 12px; width: 100px;
}
.input-sm.flex-1 { flex: 1; }
.btn-close { background: none; border: none; color: #888; font-size: 18px; cursor: pointer; }
.btn, .btn-sm {
  background: #2a2a2e; border: 1px solid #3a3a3e; color: #ccc;
  padding: 4px 10px; border-radius: 4px; cursor: pointer; font-size: 12px;
}
.btn-sm { padding: 3px 8px; font-size: 11px; }
.btn:hover, .btn-sm:hover { background: #3a3a3e; }
.btn-primary { background: #238636; border-color: #2ea043; color: #fff; }
.btn-primary:hover { background: #2ea043; }
.btn-danger { color: #f66; }
.btn-danger:hover { background: #3a1a1a; }
</style>
