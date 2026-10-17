import type { ClientMessage, ServerMessage } from '@/types/protocol'

/**
 * 一条到复盘后端的 WebSocket 连接。
 *
 * 为什么所有业务都走 WS（而不是「读用 HTTP、写用 WS」）：需求要求「除缩放平移外
 * 所有操作都发往后端处理再广播回来」。缩放平移是纯像素变换，留在前端；
 * 其余一律是同一条有序的消息流 —— 这样多用户看到的顺序天然一致，
 * 也才有可能把「谁做了什么」按顺序记下来做演示回放。
 *
 * 断线由这里负责重连（退避 0.8s → 5s）。重连成功后由 store 重新 list + join：
 * 服务端只有内存状态，重连等于「重新入场」，这一点必须显式做，不能假设连接还在。
 */

export type ConnectionStatus = 'connecting' | 'open' | 'closed'

type MessageListener = (msg: ServerMessage) => void
type StatusListener = (status: ConnectionStatus) => void

export function defaultSocketUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${window.location.host}/ws`
}

export class ReplaySocket {
  readonly url: string
  status: ConnectionStatus = 'closed'

  private ws: WebSocket | null = null
  private readonly messageListeners = new Set<MessageListener>()
  private readonly statusListeners = new Set<StatusListener>()
  private reconnectTimer: number | null = null
  private attempts = 0
  private stopped = false

  constructor(url: string) {
    this.url = url
  }

  connect(): void {
    if (this.stopped) return
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    this.setStatus('connecting')
    const ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = () => {
      this.attempts = 0
      this.setStatus('open')
    }
    ws.onmessage = (ev: MessageEvent<string>) => {
      let msg: ServerMessage
      try {
        msg = JSON.parse(ev.data) as ServerMessage
      } catch {
        console.warn('[ws] 收到非 JSON 消息，已忽略')
        return
      }
      for (const fn of this.messageListeners) fn(msg)
    }
    ws.onclose = () => {
      this.ws = null
      this.setStatus('closed')
      this.scheduleReconnect()
    }
    ws.onerror = () => {
      // 浏览器紧接着一定会回调 onclose，重连统一在那里安排
    }
  }

  /** 发送一条上行消息。没连上时返回 false，由调用方提示用户。 */
  send(msg: ClientMessage): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false
    this.ws.send(JSON.stringify(msg))
    return true
  }

  onMessage(fn: MessageListener): () => void {
    this.messageListeners.add(fn)
    return () => this.messageListeners.delete(fn)
  }

  onStatus(fn: StatusListener): () => void {
    this.statusListeners.add(fn)
    fn(this.status)
    return () => this.statusListeners.delete(fn)
  }

  close(): void {
    this.stopped = true
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
  }

  private setStatus(s: ConnectionStatus): void {
    if (this.status === s) return
    this.status = s
    for (const fn of this.statusListeners) fn(s)
  }

  private scheduleReconnect(): void {
    if (this.stopped || this.reconnectTimer !== null) return
    const delay = Math.min(5000, 800 * 2 ** this.attempts)
    this.attempts += 1
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }
}
