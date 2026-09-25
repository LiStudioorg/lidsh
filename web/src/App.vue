<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import Sidebar from './components/Sidebar.vue'
import Conversation from './views/Conversation.vue'
import SettingsView from './views/SettingsView.vue'
import { useConversation } from './composables/useConversation'
import { useTheme } from './composables/useTheme'

const convo = useConversation()
const theme = useTheme()

// 右侧设置面板开关。
const showSettings = ref(false)

onMounted(async () => {
  theme.apply()
  convo.connect()
  try {
    await convo.refreshSessions()
  } catch (e) {
    console.error('load sessions failed', e)
  }
})
onUnmounted(() => convo.dispose())
</script>

<template>
  <div class="frame">
    <aside class="sidebar-col">
      <Sidebar
        :sessions="convo.sessions.value"
        :current-id="convo.currentId.value"
        :busy="convo.busy.value"
        @new-session="convo.newSession()"
        @select="convo.openSession($event)"
        @open-settings="showSettings = !showSettings"
      />
    </aside>

    <main class="center-col">
      <Conversation
        v-if="convo.currentId.value"
        :convo="convo"
      />
      <div v-else class="empty">
        <div class="empty-headline">新建会话，开始使用 lidsh</div>
        <button class="btn primary" @click="convo.newSession()">+ 新建会话</button>
      </div>
    </main>

    <aside class="rightbar-col" v-if="showSettings">
      <SettingsView :theme="theme" />
    </aside>
  </div>
</template>

<style scoped>
.frame {
  display: grid;
  grid-template-rows: 100%;
  height: 100vh;
  background: var(--dsw-alias-bg-base);
  position: relative;
  overflow: hidden;
}
.sidebar-col {
  background: var(--dsw-specific-sidebar-fill);
  border-right: 0.5px solid var(--dsw-alias-border-l3);
  min-width: 0;
  overflow: hidden;
}
.center-col {
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}
.rightbar-col {
  width: 320px;
  min-width: 0;
  position: relative;
  overflow: auto;
  border-left: 0.5px solid var(--dsw-alias-border-l3);
  background: var(--dsw-alias-bg-base);
}
.empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  color: var(--dsw-alias-label-secondary);
}
.empty-headline {
  font-size: 26px;
  line-height: 32px;
  font-weight: 500;
  color: var(--dsw-alias-label-primary);
}
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  border: none;
  border-radius: 18px;
  font-size: 14px;
  line-height: 22px;
  padding: 0 14px;
  height: 36px;
  cursor: pointer;
  background: var(--dsw-alias-brand-primary);
  color: var(--dsw-alias-bg-base);
}
.btn:hover { opacity: 0.9; }
</style>
