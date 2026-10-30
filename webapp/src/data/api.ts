/**
 * 后端调用的共用部分：统一响应体 → 业务数据。
 *
 * 口径来自 `api/R.java`：`{"code":0,"msg":"ok","data":...}`，code 非 0 一律当失败抛出去，
 * 调用方只写 `try/catch`，不用每次判断 `body.code`。
 *
 * 模板和账户两份 store 都用它，别各写一份 —— 两份解包逻辑迟早对同一件事有两种说法。
 */

interface Envelope<T> {
  code: number
  msg: string
  data: T
}

/** 发请求并拆信封。失败时抛 Error，message 就是后端给的中文原因。 */
export async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  const body = (await res.json().catch(() => null)) as Envelope<T> | null
  if (!body || body.code !== 0) throw new Error(body?.msg || `HTTP ${res.status}`)
  return body.data
}

/** JSON 请求体。PUT 和 POST 共用。 */
export const send = (method: 'POST' | 'PUT', body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})

/** 把 catch 到的东西变成一句能给用户看的话。 */
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
