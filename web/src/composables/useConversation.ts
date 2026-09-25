// 会话消息状态：订阅 session/follow，把事件累积成可渲染的消息列表。
// 事件→surface 的投影简化版：user/message → 用户气泡；assistant/message →
// assistant 块（含 tool call）；tool/call + tool/result → 工具卡片。
import { ref, shallowRef } from 'vue'
import { MuxClient, type SessionWireEvent } from '../api/streams'
import { listSessions, createSession, promptSession, cancelSession, type SessionSummary } from '../api/client'

export interface ToolCallBlock {
  seq: number
  name: string
  args: string
  time: number
  result?: { message?: string; error?: string; meta?: Record<string, unknown> }
  status: 'running' | 'done' | 'error'
}

export interface MessageBlock {
  eventType: string
  seq: number
  time: number
  role: 'user' | 'assistant' | 'system'
  text?: string
  toolCalls: ToolCallBlock[]
}

export function useConversation() {
  const sessions = ref<SessionSummary[]>([])
  const currentId = ref<string | null>(null)
  const messages = ref<MessageBlock[]>([])
  const snapshotLoaded = ref(false)
  const error = ref<string | null>(null)
  const busy = ref(false)
  const clientId = ref<string | null>(null)

  const mux = new MuxClient({
    onEventsReady: (r) => { clientId.value = r.clientId },
    onWaterfall: (wf) => {
      // 简化策略：默认拒绝一次审批瀑布（如实展示机制，未接真实授权面板）。
      // 达标但策略内，后续可接入审批 UI。
      void wf
    },
  })

  function connect() { mux.connect(); mux.openEvents() }

  async function refreshSessions() {
    const r = await listSessions()
    sessions.value = r.items.sort((a, b) => b.createdAt - a.createdAt)
  }

  async function newSession() {
    busy.value = true
    try {
      const r = await createSession({ agentPreset: 'standard' })
      sessions.value.unshift({
        sessionId: r.sessionId, createdAt: Date.now(), agentPreset: r.agentPreset,
      })
      await openSession(r.sessionId)
    } finally {
      busy.value = false
    }
  }

  async function openSession(id: string) {
    if (currentId.value === id) return
    currentId.value = id
    messages.value = []
    snapshotLoaded.value = false
    error.value = null
    mux.openStream<{ type: string }>('session/follow', {
      request: { address: { kind: 'session', sessionId: id }, assistantStream: true },
    }, {
      onItem: (v) => handleFollowFrame(v),
      onError: (c, m) => { error.value = `${c}: ${m}` },
    })
  }

  function handleFollowFrame(v: unknown) {
    const frame = v as { type: string }
    switch (frame.type) {
      case 'snapshot': {
        const records = (frame as unknown as { records: { type: string; event: SessionWireEvent }[] }).records
        for (const r of records) applyEvent(r.event)
        snapshotLoaded.value = true
        break
      }
      case 'event': {
        const ev = (frame as unknown as { event: SessionWireEvent }).event
        applyEvent(ev)
        break
      }
    }
  }

  function applyEvent(e: SessionWireEvent) {
    // 已存在的 seq 直接跳过（快照/增量幂等）。
    if (e.seq != null && messages.value.some((m) => m.seq === e.seq)) return
    if (e.type === 'user/message') {
      messages.value.push({
        eventType: e.type, seq: e.seq ?? 0, time: e.time,
        role: 'user', text: extractText(e.data?.content), toolCalls: [],
      })
    } else if (e.type === 'assistant/message') {
      const msg = (e.data?.message ?? {}) as { content?: unknown[] }
      const blocks = msg.content ?? []
      const toolCalls: ToolCallBlock[] = []
      let text = ''
      for (const b of blocks as { type?: string }[]) {
        if (b.type === 'text') text += ((b as unknown as { text: string }).text ?? '')
        else if (b.type === 'tool-call') {
          const tc = b as unknown as { id: string; name: string; arguments: string }
          toolCalls.push({
            seq: e.seq ?? 0, name: tc.name, args: tc.arguments, time: e.time, status: 'running',
          })
        }
      }
      messages.value.push({ eventType: e.type, seq: e.seq ?? 0, time: e.time, role: 'assistant', text, toolCalls })
    } else if (e.type === 'tool/result') {
      // 关联到最近一次匹配的工具调用。
      const name = (e.data?.name as string) ?? ''
      const last = [...messages.value].reverse().find((m) =>
        m.toolCalls.some((t) => t.name === name && t.status === 'running'))
      if (last) {
        const t = last.toolCalls.find((x) => x.name === name && x.status === 'running')
        if (t) {
          t.status = e.data?.error ? 'error' : 'done'
          t.result = {
            message: (e.data?.message as string) ?? '',
            error: e.data?.error as string | undefined,
            meta: e.data?.meta as Record<string, unknown> | undefined,
          }
        }
      }
    } else if (e.type === 'turn/start') {
      busy.value = true
    } else if (e.type === 'turn/end') {
      busy.value = false
    }
  }

  function extractText(content: unknown): string {
    if (typeof content === 'string') return content
    const parts = content as { type?: string; text?: string }[] | undefined
    if (!Array.isArray(parts)) return ''
    return parts.filter((p) => p.type === 'text').map((p) => p.text ?? '').join('')
  }

  async function send(text: string) {
    if (!currentId.value) return
    await promptSession(currentId.value, [{ type: 'text', text }])
    busy.value = true
  }

  async function cancel() {
    if (currentId.value) { await cancelSession(currentId.value); busy.value = false }
  }

  function dispose() { mux.close() }

  return {
    sessions, currentId, messages, snapshotLoaded, error, busy, clientId,
    connect, refreshSessions, newSession, openSession, send, cancel, dispose,
  }
}

export type Conversation = ReturnType<typeof useConversation>
