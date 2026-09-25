<script setup lang="ts">
// 工具卡片（ToolRow / ToolCallTree chrome，§5.5 REF-tokens）：
// 折叠单行（图标 + 标题 + 分隔点 + 摘要），点击展开显示 IO（args/result）。
import { ref } from 'vue'
import type { ToolCallBlock } from '../composables/useConversation'

const props = defineProps<{ tool: ToolCallBlock }>()

const expanded = ref(false)

function toolTitle(t: ToolCallBlock): string {
  switch (t.name) {
    case 'bash': return '命令'
    case 'read': return '读取文件'
    case 'write': return '写入文件'
    case 'edit': return '编辑文件'
    case 'glob': return '查找文件'
    case 'grep': return '搜索内容'
    default: return t.name
  }
}

function summary(t: ToolCallBlock): string {
  try {
    const args = JSON.parse(t.args ?? '{}')
    const cmd = args.command ?? args.path ?? args.pattern ?? ''
    return String(cmd).slice(0, 80)
  } catch {
    return ''
  }
}

function exitCode(t: ToolCallBlock): string | null {
  if (props.tool.result?.message) {
    const m = /exit code: (\d+)/.exec(props.tool.result.message)
    if (m) return m[1]
  }
  return null
}
</script>

<template>
  <div class="tool-row" :data-state="tool.status" :data-expandable="'true'" @click="expanded = !expanded">
    <div class="collapsed">
      <span class="icon">⚙</span>
      <span class="title">{{ toolTitle(tool) }}</span>
      <span class="sep" />
      <span class="summary" :class="tool.status === 'error' ? 'err' : ''">{{ summary(tool) }}</span>
      <span v-if="exitCode(tool) !== null" class="diff">exit {{ exitCode(tool) }}</span>
      <span v-if="tool.status === 'running'" class="running-dot"></span>
    </div>

    <div v-if="expanded" class="expanded">
      <div class="io-card">
        <div class="io-row">
          <span class="io-label">参数</span>
          <pre class="io-content">{{ pretty(tool.args) }}</pre>
        </div>
        <div v-if="tool.result" class="io-row" :data-error="tool.status === 'error' ? '1' : undefined">
          <span class="io-label">{{ tool.status === 'error' ? '错误' : '输出' }}</span>
          <pre class="io-content" :class="{ err: tool.status === 'error' }">{{ tool.result.message ?? tool.result.error ?? '' }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts">
function pretty(s: string): string {
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}
</script>

<style scoped>
.tool-row {
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}
.collapsed {
  display: flex;
  align-items: center;
  height: 24px;
  padding: 0 6px;
  border-radius: 6px;
  min-width: 0;
}
.collapsed:hover { background: var(--dsw-alias-interactive-bg-hover); }
.tool-row[data-state='running'] .collapsed { position: relative; overflow: hidden; }
.tool-row[data-state='running'] .collapsed::after {
  content: '';
  position: absolute;
  top: 0; bottom: 0;
  width: 300px;
  left: -300px;
  background: linear-gradient(90deg, transparent 0%,
    color-mix(in srgb, var(--dsw-alias-bg-base) 60%, transparent) 55%, transparent 100%);
  animation: sweep 2.6s ease-out infinite;
}
@keyframes sweep { 0% { left: -300px; } 100% { left: 100%; } }
.icon { width: 16px; margin-right: 6px; color: var(--dsw-alias-label-tertiary); font-size: 14px; flex-shrink: 0; }
.title { color: var(--dsw-alias-label-secondary); white-space: nowrap; flex-shrink: 0; }
.sep { width: 2px; height: 2px; border-radius: 1px; background: var(--dsw-alias-label-caption); margin: 0 8px; flex-shrink: 0; }
.summary { color: var(--dsw-alias-label-tertiary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.summary.err { color: var(--dsw-alias-state-error-primary); }
.diff {
  font-family: var(--ds-font-family-code);
  font-size: 11px;
  color: var(--dsw-alias-label-caption);
  margin-left: 10px;
  flex-shrink: 0;
}
.running-dot {
  width: 8px; height: 8px; border-radius: 50%;
  background: var(--dsw-alias-state-business-primary);
  margin-left: 8px;
  flex-shrink: 0;
  animation: pulse 1s ease-in-out infinite;
}
@keyframes pulse { 50% { opacity: 0.3; } }
.expanded { margin: 4px 0 2px 22px; padding-left: 8px; border-left: 0.5px solid var(--dsw-alias-border-l2); }
.io-card {
  border: 0.5px solid var(--dsw-alias-border-l1);
  background: var(--dsw-alias-markdown-code-block);
  border-radius: 12px;
  margin: 4px 0 4px 4px;
  max-height: 260px;
  overflow-y: auto;
}
.io-row {
  display: grid;
  grid-template-columns: max-content 1fr;
  column-gap: 14px;
  padding: 12px 16px;
  border-bottom: 0.5px solid var(--dsw-alias-border-l2);
}
.io-row:last-child { border-bottom: none; }
.io-label { color: var(--dsw-alias-label-caption); position: sticky; top: 12px; }
.io-content {
  margin: 0;
  font-family: var(--ds-font-family-code);
  font-size: 11px;
  line-height: 16px;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--dsw-alias-label-secondary);
}
.io-content.err { color: var(--dsw-alias-state-error-primary); }
</style>
