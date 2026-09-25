<script setup lang="ts">
import type { MessageBlock } from '../composables/useConversation'
import Markdown from './Markdown.vue'
import ToolCard from './ToolCard.vue'

defineProps<{ message: MessageBlock }>()
</script>

<template>
  <div class="msg" :class="message.role">
    <!-- 用户：气泡 -->
    <div v-if="message.role === 'user'" class="user-stack">
      <div class="bubble">{{ message.text }}</div>
    </div>

    <!-- assistant：无气泡，正文 + 工具卡片 -->
    <div v-else class="assistant">
      <div v-if="message.text" class="body">
        <Markdown :source="message.text" />
      </div>
      <div v-if="message.toolCalls.length" class="tools">
        <ToolCard v-for="(t, i) in message.toolCalls" :key="t.seq + '-' + i" :tool="t" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.msg { margin-top: var(--dsh-chat-flow-gap, 16px); }
.user-stack { display: flex; justify-content: flex-end; }
.bubble {
  background: var(--dsw-specific-bubble);
  border-radius: 22px;
  padding: 10px 16px;
  font-size: var(--dsh-content-font-size, 14px);
  line-height: calc(22px + var(--dsh-content-font-delta, 0px));
  color: var(--dsw-alias-label-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-width: min(calc(var(--dsh-chat-content-width, 748px) * 0.702), 82%);
}
.assistant { color: var(--dsw-alias-label-primary); }
.body { font-size: var(--dsh-content-font-size, 14px); line-height: calc(24px + var(--dsh-content-font-delta, 0px)); }
.tools { margin-top: 8px; display: flex; flex-direction: column; gap: 4px; }
</style>
