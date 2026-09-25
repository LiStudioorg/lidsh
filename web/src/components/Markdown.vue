<script setup lang="ts">
// 轻量 Markdown 渲染：标题/加粗/斜体/行内代码/代码块/列表/链接/段落/换行。
// 不做脚注|表格|图片（可通过后续接入成熟库升级）。
import { computed } from 'vue'

const props = defineProps<{ source: string }>()

interface Tok { type: string; text?: string; items?: Tok[]; lang?: string }

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function inline(s: string): string {
  // 代码：先保护行内代码，避免被其它规则处理。
  const codes: string[] = []
  let out = s.replace(/`([^`]+)`/g, (_m, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000` })
  out = out
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>')
  out = out.replace(/\u0000(\d+)\u0000/g, (_m, i) => `<code>${escapeHtml(codes[Number(i)])}</code>`)
  return out
}

function parse(src: string): Tok[] {
  const lines = src.split('\n')
  const out: Tok[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    const h = /^(#{1,4})\s+(.*)$/.exec(line)
    if (h) { out.push({ type: `h${h[1].length}`, text: inline(h[2]) }); i++; continue }
    if (/^```/.test(line)) {
      const lang = (line.match(/^```(\S*)/) ?? [,''])[1] ?? ''
      const buf: string[] = []
      i++
      while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++ }
      i++ // skip closing fence
      out.push({ type: 'code', lang, text: buf.join('\n') })
      continue
    }
    if (/^\s*[-*]\s+/.test(line)) {
      const items: Tok[] = []
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        items.push({ type: 'li', text: inline(lines[i].replace(/^\s*[-*]\s+/, '')) })
        i++
      }
      out.push({ type: 'ul', items })
      continue
    }
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: Tok[] = []
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push({ type: 'li', text: inline(lines[i].replace(/^\s*\d+\.\s+/, '')) })
        i++
      }
      out.push({ type: 'ol', items })
      continue
    }
    if (line.trim() === '') { i++; continue }
    const buf = [line]
    i++
    while (i < lines.length && lines[i].trim() !== '' && !/^(#{1,4})\s/.test(lines[i]) && !/^```/.test(lines[i]) && !/^\s*[-*]\s+/.test(lines[i]) && !/^\s*\d+\.\s+/.test(lines[i])) {
      buf.push(lines[i]); i++
    }
    out.push({ type: 'p', text: inline(buf.join(' ')) })
  }
  return out
}

const toks = computed(() => parse(props.source))
</script>

<template>
  <div class="markdown">
    <template v-for="(tok, idx) in toks" :key="idx">
      <h1 v-if="tok.type === 'h1'" class="h1" v-html="tok.text" />
      <h2 v-else-if="tok.type === 'h2'" class="h2" v-html="tok.text" />
      <h3 v-else-if="tok.type === 'h3'" class="h3" v-html="tok.text" />
      <h4 v-else-if="tok.type === 'h4'" class="h4" v-html="tok.text" />
      <p v-else-if="tok.type === 'p'" class="p" v-html="tok.text" />
      <pre v-else-if="tok.type === 'code'" class="code-block"><code>{{ tok.text }}</code></pre>
      <ul v-else-if="tok.type === 'ul'" class="list"><li v-for="(it, j) in tok.items" :key="j" v-html="it.text" /></ul>
      <ol v-else-if="tok.type === 'ol'" class="list ordered"><li v-for="(it, j) in tok.items" :key="j" v-html="it.text" /></ol>
    </template>
  </div>
</template>

<style scoped>
.markdown {
  font-size: var(--dsh-content-font-size, 14px);
  line-height: calc(24px + var(--dsh-content-font-delta, 0px));
  color: var(--dsw-alias-label-primary);
  word-break: break-word;
}
.h1 { font-size: 21px; line-height: 30px; font-weight: 700; margin: 18px 0 8px; }
.h2 { font-size: 19px; line-height: 28px; font-weight: 700; margin: 16px 0 8px; }
.h3 { font-size: 18px; line-height: 26px; font-weight: 700; margin: 14px 0 8px; }
.h4 { font-size: 14px; line-height: 24px; font-weight: 600; margin: 12px 0 6px; }
.p { margin: 8px 0; }
.list { margin: 8px 0; padding-left: 24px; }
.ordered { list-style: decimal; }
.list li { margin: 4px 0; }
.code-block {
  font-family: var(--ds-font-family-code);
  font-size: 11px;
  line-height: 19px;
  background: var(--dsw-alias-markdown-code-block);
  border-radius: 12px;
  padding: 12px 14px;
  overflow-x: auto;
  margin: 8px 0;
}
:deep(code) {
  font-family: var(--ds-font-family-code);
  font-size: 0.9em;
  background: var(--dsw-alias-markdown-inline-code);
  border-radius: 6px;
  padding: 1px 5px;
}
a { color: var(--dsw-alias-link); text-decoration: none; }
a:hover { text-decoration: underline; }
</style>
