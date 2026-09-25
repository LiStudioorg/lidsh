<script setup lang="ts">
import type { SessionSummary } from '../api/client'

defineProps<{
  sessions: SessionSummary[]
  currentId: string | null
  busy: boolean
}>()
defineEmits<{
  (e: 'new-session'): void
  (e: 'select', id: string): void
  (e: 'open-settings'): void
}>()
</script>

<template>
  <div class="root">
    <div class="logo-row">
      <span class="brand">lidsh</span>
    </div>

    <button class="new-session" @click="$emit('new-session')" :disabled="busy">
      + 新建会话
    </button>

    <div class="section-label">最近</div>
    <div class="session-list">
      <button
        v-for="s in sessions"
        :key="s.sessionId"
        class="session-row"
        :class="{ active: s.sessionId === currentId }"
        @click="$emit('select', s.sessionId)"
      >
        <span class="title">{{ s.agentPreset ?? 'standard' }}</span>
        <span class="time">{{ fmtTime(s.createdAt) }}</span>
      </button>
      <div v-if="sessions.length === 0" class="empty-list">暂无会话</div>
    </div>

    <div class="footer">
      <button class="ghost" @click="$emit('open-settings')">设置</button>
    </div>
  </div>
</template>

<script lang="ts">
function fmtTime(ms: number): string {
  const d = new Date(ms)
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}
</script>

<style scoped>
.root {
  padding: 6px 12px;
  height: 100%;
  display: flex;
  flex-direction: column;
  font-size: 14px;
}
.logo-row {
  height: 60px;
  display: flex;
  align-items: center;
  padding: 8px 4px;
}
.brand {
  font-size: 18px;
  line-height: 24px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--dsw-alias-label-primary);
}
.new-session {
  height: 38px;
  border-radius: 12px;
  border: 0.5px solid var(--dsw-alias-border-l3);
  background: var(--dsw-alias-button-elevated-fill);
  padding: 8px 16px;
  font-size: 14px;
  line-height: 22px;
  font-weight: 500;
  color: var(--dsw-alias-label-primary);
  cursor: pointer;
  margin-bottom: 8px;
}
.new-session:hover { background: var(--dsw-alias-button-floating-hover); }
.new-session:disabled { opacity: 0.5; cursor: not-allowed; }
.section-label {
  padding: 7px 8px;
  font-size: 12px;
  color: var(--dsw-alias-label-tertiary);
}
.session-list {
  flex: 1;
  overflow-y: auto;
}
.session-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  height: 32px;
  border-radius: 8px;
  padding: 0 8px;
  border: none;
  background: transparent;
  cursor: pointer;
  color: var(--dsw-alias-label-secondary);
}
.session-row:hover { background: var(--dsw-alias-interactive-bg-hover); }
.session-row.active { background: var(--dsw-alias-interactive-bg-active); color: var(--dsw-alias-label-primary); font-weight: 500; }
.title { font-size: 14px; line-height: 20px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.time { font-size: 12px; color: var(--dsw-alias-label-tertiary); flex-shrink: 0; margin-left: 8px; }
.empty-list { padding: 8px; color: var(--dsw-alias-label-tertiary); font-size: 13px; }
.footer { padding-top: 8px; }
.ghost {
  border: none;
  background: transparent;
  color: var(--dsw-alias-label-secondary);
  cursor: pointer;
  padding: 6px 8px;
  border-radius: 8px;
  font-size: 14px;
}
.ghost:hover { background: var(--dsw-alias-interactive-bg-hover); }
</style>
