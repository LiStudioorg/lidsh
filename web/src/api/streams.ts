// WS 远程流客户端：/api/remote.mux（§5.3-5.6）。
// 打开流 = open 帧；停止 = cancel 帧；收 item/end/error。事件经回调派发。

export interface SessionWireEvent {
  type: string
  seq: number
  time: number
  data?: Record<string, unknown>
  surfaceOp?: string
  sourceEventSeqs?: number[]
}

export interface SessionSnapshot {
  type: 'snapshot'
  header: Record<string, unknown>
  cursor: number
  records: { type: 'event'; event: SessionWireEvent }[]
  hasMore: boolean
  projections?: { asOfSeq: number; values: Record<string, unknown> }
}

export interface SessionFollowFrame {
  sessionId: string
  onSnapshot: (s: SessionSnapshot) => void
  onEvent: (e: SessionWireEvent) => void
  onError: (code: string, message: string) => void
  onEnd?: () => void
}

export interface EventsReady {
  type: 'ready'
  clientId: string
  host: { home: string }
}

export interface WaterfallFrame {
  type: 'waterfall'
  event: string
  eventId: string
  agentId: string
  request: Record<string, unknown>
}

export interface EventsCancelFrame {
  type: 'cancel'
  eventId: string
}

export interface MuxClientOptions {
  onOpen?: () => void
  onClose?: (code?: number) => void
  onEventsReady?: (r: EventsReady) => void
  onEmit?: (event: string, args: unknown[]) => void
  onWaterfall?: (wf: WaterfallFrame) => void
  onWaterfallCancel?: (eventId: string) => void
}

/**
 * MuxClient 维护一条共享 WS 连接，复用所有远程流（session/follow、$events…）。
 * 断线自动重连并重开已注册流。
 */
export class MuxClient {
  private ws: WebSocket | null = null
  private url: string
  private streams = new Map<string, { endpoint: string; payload: unknown; handlers: {
    onItem: (v: unknown) => void; onError: (c: string, m: string) => void; onEnd?: () => void } }>()
  private reconnectTimer: number | undefined
  private closed = false
  private clientId: string | null = null

  constructor(private opts: MuxClientOptions = {}) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    this.url = `${proto}//${location.host}/api/remote.mux`
  }

  connect() {
    this.closed = false
    this.open()
  }

  private open() {
    const ws = new WebSocket(this.url)
    this.ws = ws
    ws.onopen = () => {
      this.opts.onOpen?.()
      // 重开后重开所有流。
      for (const [id, st] of this.streams) {
        this.send({ type: 'open', streamId: id, endpoint: st.endpoint, payload: { args: st.payload } })
      }
    }
    ws.onmessage = (ev) => this.handleMessage(ev.data)
    ws.onclose = (ev) => {
      if (ev.code === 4000) return // 主动关闭
      this.opts.onClose?.(ev.code)
      this.clientId = null
      if (!this.closed) this.scheduleReconnect()
    }
    ws.onerror = () => ws.close()
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = undefined
      if (!this.closed) this.open()
    }, 1000)
  }

  private send(frame: unknown) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(frame))
  }

  private handleMessage(data: unknown) {
    let frame: Record<string, unknown>
    try {
      frame = JSON.parse(String(data))
    } catch {
      return
    }
    switch (frame.type) {
      case 'item': {
        const st = this.streams.get(frame.streamId as string)
        st?.handlers.onItem(frame.value)
        break
      }
      case 'end': {
        const st = this.streams.get(frame.streamId as string)
        st?.handlers.onEnd?.()
        break
      }
      case 'error': {
        const st = this.streams.get(frame.streamId as string)
        const e = frame.error as { code: string; message: string }
        st?.handlers.onError(e.code, e.message)
        break
      }
      case 'ready': {
        const r = frame as unknown as EventsReady
        this.clientId = r.clientId
        this.opts.onEventsReady?.(r)
        break
      }
      case 'emit':
        this.opts.onEmit?.((frame.event as string) ?? '', (frame.args as unknown[]) ?? [])
        break
      case 'waterfall':
        this.opts.onWaterfall?.(frame as unknown as WaterfallFrame)
        break
      case 'cancel':
        this.opts.onWaterfallCancel?.((frame.eventId as string) ?? '')
        break
    }
  }

  /** 打开一个通用流。返回流 id。 */
  openStream<T>(endpoint: string, payload: unknown, handlers: {
    onItem: (v: T) => void; onError: (c: string, m: string) => void; onEnd?: () => void
  }): string {
    const id = crypto.randomUUID()
    this.streams.set(id, {
      endpoint, payload,
      handlers: { onItem: handlers.onItem as (v: unknown) => void, onError: handlers.onError, onEnd: handlers.onEnd },
    })
    this.send({ type: 'open', streamId: id, endpoint, payload: { args: payload } })
    return id
  }

  cancel(id: string) {
    const st = this.streams.get(id)
    if (st) this.send({ type: 'cancel', streamId: id })
    this.streams.delete(id)
  }

  /** 打开 $events 流并持续接收瀑布/emit。 */
  openEvents() {
    this.openStream<never>('$events', {}, {
      onItem: () => {},
      onError: (c, m) => console.error('[events]', c, m),
    })
  }

  get currentClientId(): string | null { return this.clientId }

  close() {
    this.closed = true
    if (this.reconnectTimer) window.clearTimeout(this.reconnectTimer)
    this.ws?.close(4000)
    this.ws = null
  }
}
