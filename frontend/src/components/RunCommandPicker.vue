<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { GetProjectRunCommands } from '../../wailsjs/go/main/App'
import { config } from '../../wailsjs/go/models'
import { useWorkspaceStore } from '../stores/workspace'

const emit = defineEmits<{
  (e: 'select', cmd: config.ProjectRunCommand): void
  (e: 'run-all'): void
  (e: 'dismiss'): void
  (e: 'settings'): void
}>()

const ws = useWorkspaceStore()
const commands = ref<config.ProjectRunCommand[]>([])
const loading = ref(true)

const projectName = computed(() => ws.info?.name || '当前项目')

onMounted(async () => {
  try {
    commands.value = (await GetProjectRunCommands()) || []
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="picker-overlay" @click.self="emit('dismiss')">
    <div class="picker-card">
      <div class="picker-header">
        <div class="title-wrap">
          <span>运行项目</span>
          <span class="project-name">{{ projectName }}</span>
        </div>
        <div class="header-actions">
          <button class="btn-sm" @click="emit('settings')" title="配置本项目运行命令">配置</button>
          <button class="btn-close" @click="emit('dismiss')">&times;</button>
        </div>
      </div>
      <div class="picker-body">
        <div v-if="loading" class="empty-hint">加载中...</div>
        <div v-else-if="commands.length === 0" class="empty-hint">
          当前项目还没有运行命令，点击“配置”添加
        </div>
        <template v-else>
          <button class="cmd-btn run-all" @click="emit('run-all')">
            <span class="cmd-label">▶ 按顺序运行全部</span>
            <code class="cmd-text">每条命令单独打开一个终端 · 共 {{ commands.length }} 条</code>
          </button>
          <button
            v-for="cmd in commands"
            :key="`${cmd.name}:${cmd.command}`"
            class="cmd-btn"
            @click="emit('select', cmd)"
          >
            <span class="cmd-label">{{ cmd.name }}</span>
            <code class="cmd-text">{{ cmd.command }}</code>
          </button>
        </template>
      </div>
      <div class="picker-footer">
        <button class="btn" @click="emit('dismiss')">取消</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.picker-overlay {
  position: fixed; inset: 0; background: rgba(0,0,0,0.4);
  display: flex; align-items: center; justify-content: center; z-index: 90;
}
.picker-card {
  background: #1a1a1e; border: 1px solid #3a3a3e; border-radius: 10px;
  width: 440px; max-height: 64vh; display: flex; flex-direction: column;
  box-shadow: 0 8px 32px rgba(0,0,0,0.5);
}
.picker-header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 14px 18px; border-bottom: 1px solid #2a2a2e;
}
.title-wrap { display: flex; flex-direction: column; gap: 2px; }
.title-wrap > span:first-child { font-size: 14px; font-weight: 600; }
.project-name { font-size: 12px; color: #8b949e; }
.header-actions { display: flex; gap: 8px; align-items: center; }
.picker-body { padding: 12px; overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: 6px; }
.picker-footer { padding: 10px 16px; border-top: 1px solid #2a2a2e; display: flex; justify-content: center; }
.empty-hint { color: #666; text-align: center; padding: 24px 0; font-size: 13px; }
.cmd-btn {
  display: flex; flex-direction: column; align-items: flex-start; gap: 2px;
  padding: 10px 14px; background: #222; border: 1px solid #333; border-radius: 6px;
  cursor: pointer; text-align: left; transition: background 0.15s, border-color 0.15s;
  color: inherit; font-family: inherit; width: 100%;
}
.cmd-btn:hover { background: #1f2a1f; border-color: #3fb950; }
.cmd-btn.run-all { border-color: #2ea043; background: #1a2a1a; }
.cmd-btn.run-all:hover { background: #1f3a1f; }
.cmd-label { color: #ddd; font-size: 13px; font-weight: 500; }
.cmd-text { color: #7dba7d; font-size: 12px; background: transparent; }
.btn, .btn-sm {
  background: #2a2a2e; border: 1px solid #3a3a3e; color: #ccc;
  padding: 4px 10px; border-radius: 4px; cursor: pointer; font-size: 12px;
}
.btn:hover, .btn-sm:hover { background: #3a3a3e; }
.btn-sm { padding: 3px 8px; font-size: 11px; }
.btn-close { background: none; border: none; color: #888; font-size: 18px; cursor: pointer; }
</style>
