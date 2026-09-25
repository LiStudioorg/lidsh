<script setup lang="ts">
// 输入条 / composer（§5.4 REF-tokens）：卡片 elevation-soft、22px 圆角、
// caret 品牌色、无 focus 高亮、34×34 发送按钮。
import { ref } from 'vue'

const props = defineProps<{ busy: boolean }>()
const emit = defineEmits<{ (e: 'send', text: string): void }>()

const text = ref('')

function submit() {
  const v = text.value.trim()
  if (!v || props.busy) return
  emit('send', v)
  text.value = ''
}

function onKeydown(e: KeyboardEvent) {
  // Enter 发送，Shift+Enter 换行。
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    submit()
  }
}
</script>

<template>
  <div class="root">
    <div class="card">
      <textarea
        v-model="text"
        class="input"
        :placeholder="busy ? '…' : '给 lidsh 发消息'"
        @keydown="onKeydown"
        rows="1"
      />
      <div class="bottom">
        <button class="add" title="附件（预留）">＋</button>
        <button class="send" :disabled="busy || !text.trim()" @click="submit" title="发送">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 19V5M5 12l7-7 7 7" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.root { padding: 0 16px 8px; }
.card {
  max-width: var(--dsh-composer-card-max-width, 920px);
  margin: 0 auto;
  background: var(--dsw-specific-input-major);
  border: 0;
  box-shadow: var(--dsw-elevation-soft);
  border-radius: 22px;
  padding-top: 12px;
  color: var(--dsw-alias-label-primary);
}
.input {
  display: block;
  width: 100%;
  box-sizing: border-box;
  min-height: 36px;
  padding: 4px 14px 0;
  border: none;
  outline: none;
  resize: none;
  font-family: var(--dsw-font-family);
  font-size: var(--dsh-content-font-size, 14px);
  line-height: calc(24px + var(--dsh-content-font-delta, 0px));
  color: var(--dsw-alias-label-primary);
  background: transparent;
  caret-color: var(--dsw-alias-state-business-primary);
  white-space: pre-wrap;
  word-break: break-word;
  overflow-wrap: anywhere;
}
.input::placeholder { color: var(--dsw-alias-label-tertiary); }
.bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 8px 8px;
  gap: 12px;
}
.add {
  width: 28px; height: 28px;
  border-radius: 999px;
  background: var(--dsw-specific-selector);
  color: var(--dsw-alias-label-primary);
  border: none;
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.add:hover { background: var(--dsw-alias-interactive-bg-hover-solid); }
.send {
  width: 34px; height: 34px;
  border-radius: 999px;
  background: var(--dsw-alias-button-info-fill);
  color: #fff;
  border: none;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: background-color 0.1s;
  transform: translateY(-2px);
}
.send:hover { background: var(--dsw-alias-button-info-hover); }
.send:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
