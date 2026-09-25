<script setup lang="ts">
// 主会话区：头部 + 消息流 + 输入条。内容列宽 clamp(680, col*0.64, 920)。
import { nextTick, ref, watch } from 'vue'
import type { Conversation } from '../composables/useConversation'
import MessageItem from '../components/MessageItem.vue'
import InputBar from '../components/InputBar.vue'

const props = defineProps<{ convo: Conversation }>()
const scrollEl = ref<HTMLElement | null>(null)

async function scrollToBottom() {
  await nextTick()
  if (scrollEl.value) scrollEl.value.scrollTop = scrollEl.value.scrollHeight
}

watch(() => props.convo.messages.value.length, scrollToBottom)
watch(() => props.convo.currentId.value, () => { props.convo.messages.value = []; scrollToBottom() })

function onSend(text: string) {
  void props.convo.send(text)
}

function onCancel() {
  void props.convo.cancel()
}
</script>

<template>
  <div class="conversation">
    <header class="header">
      <div class="title">{{ convo.busy.value ? '工作中…' : '与会话对话' }}</div>
      <button v-if="convo.busy.value" class="stop" @click="onCancel">停止</button>
    </header>

    <div ref="scrollEl" class="scroll">
      <div class="column">
        <div v-if="!convo.snapshotLoaded.value" class="loading">加载中…</div>
        <div v-else-if="!convo.messages.value.length" class="hero">
          <div class="hero-title">你好！我可以在这个工作区帮你写代码。</div>
        </div>
        <MessageItem v-for="m in convo.messages.value" :key="m.seq" :message="m" />
        <div v-if="convo.busy.value" class="typing">
          <span class="dot"></span><span class="dot"></span><span class="dot"></span>
        </div>
      </div>
    </div>

    <InputBar :busy="convo.busy.value" @send="onSend" />
  </div>
</template>

<style scoped>
.conversation { display: flex; flex-direction: column; height: 100%; min-width: 0; }
.header {
  min-height: 76px;
  padding: 10px 28px 0 20px;
  border-bottom: 0.5px solid var(--dsw-alias-border-l3);
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
.title { font-size: 16px; font-weight: 600; color: var(--dsw-alias-label-primary); }
.stop {
  border: none;
  background: var(--dsw-alias-interactive-bg-hover-solid);
  color: var(--dsw-alias-label-secondary);
  border-radius: 12px;
  padding: 4px 10px;
  cursor: pointer;
  font-size: 13px;
}
.scroll { flex: 1; overflow-y: auto; padding: 16px 32px; }
.column {
  max-width: clamp(680px, 100% * 0.64, 920px);
  margin: 0 auto;
}
.loading, .hero { color: var(--dsw-alias-label-tertiary); padding: 40px 0; }
.hero-title { font-size: 26px; line-height: 32px; font-weight: 500; color: var(--dsw-alias-label-secondary); }
.typing { display: flex; gap: 4px; padding: 12px 0; }
.dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--dsw-alias-label-tertiary);
  animation: bounce 1s ease-in-out infinite;
}
.dot:nth-child(2) { animation-delay: 0.15s; }
.dot:nth-child(3) { animation-delay: 0.3s; }
@keyframes bounce { 0%, 60%, 100% { transform: translateY(0); opacity: 0.4; } 30% { transform: translateY(-4px); opacity: 1; } }
</style>
