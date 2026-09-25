// lidsh 前端协议层：HTTP 一元 RPC（/api/*）+ WS 远程流（/api/remote.mux）。
// 契约来源：docs/_parts/ws-protocol.md §5。

/** 一元 RPC 请求信封（§5.2）。 */
export interface RpcRequest {
  type: 'client-request'
  rpcId: string
  method: string
  payload: { args: unknown }
}

export interface RpcError {
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface RpcResponse<T = unknown> {
  type: 'server-response'
  rpcId: string
  result: { ok: boolean; value?: T; error?: RpcError }
}

let rpcSeq = 0
/** 发起一个 HTTP 一元 RPC；返回 result.value，失败抛 RpcError。 */
export async function rpc<T = unknown>(method: string, args: unknown): Promise<T> {
  const rpcId = `rpc-${Date.now()}-${rpcSeq++}`
  const body: RpcRequest = { type: 'client-request', rpcId, method, payload: { args } }
  const resp = await fetch(`/api/${method}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} on ${method}`)
  }
  const data = (await resp.json()) as RpcResponse<T>
  if (!data.result.ok) {
    const e = data.result.error
    const err = new Error(e?.message ?? 'rpc failed') as Error & { code?: string }
    err.code = e?.code
    throw err
  }
  return data.result.value as T
}

// ---------- 会话摘要 ----------

export interface SessionSummary {
  sessionId: string
  createdAt: number
  cwd?: string
  agentPreset?: string
}

/** session/list。 */
export async function listSessions(): Promise<{ items: SessionSummary[] }> {
  return rpc('session/list', {})
}

/** session/create。 */
export async function createSession(args: {
  workspaceId?: string
  agentPreset?: string
}): Promise<{ sessionId: string; agentPreset?: string }> {
  return rpc('session/create', args)
}

/** session/prompt（返回 {accepted}）。 */
export async function promptSession(
  sessionId: string,
  content: PromptContentPart[],
): Promise<{ accepted: boolean }> {
  return rpc('session/prompt', {
    request: {
      requestId: `req-${Date.now()}`,
      sessionId,
      mode: 'queue',
      content,
      clientTimeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    },
  })
}

/** session/cancel。 */
export async function cancelSession(sessionId: string): Promise<{ accepted: boolean }> {
  return rpc('session/cancel', { request: { sessionId } })
}

/** $events/result（§5.5 瀑布回环）。 */
export async function eventsResult(
  clientId: string,
  eventId: string,
  outcome: { kind: 'next' } | { kind: 'result'; value?: unknown } | { kind: 'rejected'; error: { name: string; message: string; code?: string } },
): Promise<void> {
  await rpc('$events/result', { clientId, eventId, outcome })
}

/** 上传文件（附件 §A1：octet-stream 原始字节）。 */
export async function uploadFile(
  sessionId: string,
  file: Blob,
  name?: string,
): Promise<{ receiptId: string; file: { attachmentId: string; name: string; bytes: number } }> {
  const q = new URLSearchParams({ sessionId })
  if (name) q.set('name', name)
  const resp = await fetch(`/api/session/uploadFileBinary?${q.toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: file,
  })
  const data = await resp.json()
  if (!data.ok) throw new Error(data.error?.message ?? 'upload failed')
  return data.value
}

// ---------- PromptContentPart ----------

export type PromptContentPart =
  | { type: 'text'; text: string }
  | { type: 'image'; mediaType: string; data: string; name?: string }
  | { type: 'file'; receiptId: string }
