<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { GetStartupCommands } from '../../wailsjs/go/main/App'
import { config } from '../../wailsjs/go/models'

const props = withDefaults(defineProps<{ mode?: 'startup' | 'run' }>(), {
  mode: 'startup',
})
const emit = defineEmits<{
  (e: 'select', cmd: config.StartupCommand): void
  (e: 'dismiss'): void
  (e: 'settings'): void
}>()
const commands = ref<config.StartupCommand[]>([])

const title = computed(() => props.mode === 'run' ? '运行项目' : '启动命令')
const emptyHint = computed(() => props.mode === 'run'
  ? '暂无运行命令，点击“配置”添加 npm start、go run 等'
  : '暂无启动命令，点击“配置”添加')
const footerLabel = computed(() => props.mode === 'run' ? '取消' : '跳过，创建空白终端')

onMounted(async () => {
  commands.value = (await GetStartupCommands()) || []
})
</script>

<template>
  <div class="picker-overlay" @click.self="emit('dismiss')">
    <div class="picker-card">
      <div class="picker-header">
        <span>{{ title }}</span>
        <div class="header-actions">
          <button class="btn-sm" @click="emit('settings')" title="配置运行命令">配置</button>
          <button class="btn-close" @click="emit('dismiss')">&times;</button>
        </div>
      </div>
      <div class="picker-body">
        <div v-if="commands.length === 0" class="empty-hint">
          {{ emptyHint }}
        </div>
        <button
          v-for="cmd in commands"
          :key="`${cmd.name}:${cmd.command}`"
          class="cmd-btn"
          @click="emit('select', cmd)"
        >
          <span class="cmd-label">▶ {{ cmd.name }}</span>
          <code class="cmd-text">{{ cmd.command }}</code>
        </button>
      </div>
      <div class="picker-footer">
        <button class="btn" @click="emit('dismiss')">{{ footerLabel }}</button>
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
  width: 420px; max-height: 64vh; display: flex; flex-direction: column;
  box-shadow: 0 8px 32px rgba(0,0,0,0.5);
}
.picker-header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 14px 18px; border-bottom: 1px solid #2a2a2e; font-size: 14px; font-weight: 600;
}
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
.cmd-btn:hover { background: #2a2a3e; border-color: #3fb950; }
.cmd-label { color: #ddd; font-size: 13px; font-weight: 500; }
.cmd-text { color: #7a7; font-size: 12px; background: transparent; }
.btn, .btn-sm {
  background: #2a2a2e; border: 1px solid #3a3a3e; color: #ccc;
  padding: 4px 10px; border-radius: 4px; cursor: pointer; font-size: 12px;
}
.btn:hover { background: #3a3a3e; }
.btn-sm { padding: 3px 8px; font-size: 11px; }
.btn-sm:hover { background: #3a3a3e; }
.btn-close { background: none; border: none; color: #888; font-size: 18px; cursor: pointer; }
</style>
