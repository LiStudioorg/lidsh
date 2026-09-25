<script setup lang="ts">
// 设置-模型页：主题切换（亮/暗/跟随系统）+ 正文字号轴（12–17，默认 14）+ 模型概览。
// 模型选择（session/selectModel / modelCatalog）服务端尚未实现，此处只读展示 agentPreset。
import { ref } from 'vue'
import type { useTheme } from '../composables/useTheme'

// 用一个局部类型避免与返回值耦合。
type Theme = ReturnType<typeof useTheme>
const props = defineProps<{ theme: Theme }>()

const preset = ref('standard')
</script>

<template>
  <div class="settings">
    <h2 class="title">设置</h2>

    <section class="group">
      <h3 class="group-title">外观</h3>

      <div class="row">
        <span class="label">主题</span>
        <div class="seg">
          <button
            v-for="opt in ([{v:'light',l:'亮'},{v:'dark',l:'暗'},{v:'system',l:'跟随系统'}] as const)"
            :key="opt.v"
            class="seg-btn"
            :class="{ active: theme.scheme.value === opt.v }"
            @click="theme.setScheme(opt.v)"
          >{{ opt.l }}</button>
        </div>
      </div>

      <div class="row">
        <span class="label">正文字号</span>
        <div class="font-size">
          <input
            type="range" min="12" max="17" step="1"
            :value="theme.fontSize.value"
            @input="theme.setFontSize(Number(($event.target as HTMLInputElement).value))"
          />
          <span class="font-val">{{ theme.fontSize.value }}px</span>
        </div>
      </div>
    </section>

    <section class="group">
      <h3 class="group-title">模型</h3>
      <div class="row">
        <span class="label">预设</span>
        <span class="value">{{ preset }}</span>
      </div>
      <p class="hint">模型选择支持将在后续里程碑接入（session/selectModel）。</p>
    </section>
  </div>
</template>

<style scoped>
.settings { padding: 20px 16px; font-size: 14px; }
.title { font-size: 18px; font-weight: 600; margin: 0 0 16px; color: var(--dsw-alias-label-primary); }
.group { margin-bottom: 20px; }
.group-title { font-size: 12px; font-weight: 500; color: var(--dsw-alias-label-tertiary); margin: 0 0 8px; }
.row { display: flex; align-items: center; justify-content: space-between; padding: 8px 0; }
.label { color: var(--dsw-alias-label-secondary); }
.value { color: var(--dsw-alias-label-primary); }
.seg { display: flex; gap: 4px; }
.seg-btn {
  border: 0.5px solid var(--dsw-alias-border-l3);
  background: var(--dsw-alias-button-elevated-fill);
  color: var(--dsw-alias-label-secondary);
  border-radius: 8px;
  padding: 4px 10px;
  font-size: 13px;
  cursor: pointer;
}
.seg-btn.active {
  background: var(--dsw-alias-interactive-bg-active);
  color: var(--dsw-alias-label-primary);
  font-weight: 500;
}
.font-size { display: flex; align-items: center; gap: 8px; }
.font-val { font-family: var(--ds-font-family-code); color: var(--dsw-alias-label-primary); }
input[type='range'] { accent-color: var(--dsw-alias-state-business-primary); }
.hint { font-size: 12px; color: var(--dsw-alias-label-tertiary); margin: 4px 0 0; }
</style>
