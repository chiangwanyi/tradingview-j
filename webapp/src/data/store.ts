import { reactive } from 'vue'

import { call, send } from './api'
import type { Template } from './rules'

export { errorText } from './api'

/*
 * 模板走后端 `PropFirmTemplateController`（/api/pf-templates）。
 *
 * 服务端是唯一事实来源：每次写完都重新拉一遍列表，不在前端「自己拼一个结果」——
 * 那样界面看着对、库里对不上，最难查。
 *
 * 开发时靠 vite.config.ts 的 proxy 把 /api 转到 127.0.0.1:8791。
 */

const BASE = '/api/pf-templates'

/** 建和改共用一个请求体；update 时后端会忽略 stage（阶段建出来之后不能改）。 */
const payload = (t: Template) => ({
  name: t.name,
  description: t.description,
  stage: t.stage,
  nextTemplateId: t.nextTemplateId,
  rules: t.rules,
})

/** 补回被后端 non_null 省掉的 null，组件才能照 Template 的类型用。 */
const normalize = (t: Template): Template => ({
  ...t,
  nextTemplateId: t.nextTemplateId ?? null,
  rules: { ...t.rules, positionTiers: t.rules.positionTiers ?? [] },
})

export const store = reactive({ list: [] as Template[] })

/** 拉全量列表（按创建时间升序）。 */
export async function refresh(): Promise<void> {
  store.list = (await call<Template[]>(BASE)).map(normalize)
}

/** 建或改，按 id 是否存在判断 —— 新建的空白模板 id 是空串，一律走 POST。 */
export async function save(t: Template): Promise<void> {
  const exists = store.list.some((x) => x.id === t.id)
  await call(exists ? `${BASE}/${t.id}` : BASE, send(exists ? 'PUT' : 'POST', payload(t)))
  await refresh()
}

/** 删一套；顺手断链 —— 后端不级联，留着指向已删模板的 id 会让升级路径指向空气。 */
export async function remove(id: string): Promise<void> {
  const broken = store.list.filter((t) => t.nextTemplateId === id)
  await call(`${BASE}/${id}`, { method: 'DELETE' })
  for (const t of broken) {
    await call(`${BASE}/${t.id}`, send('PUT', payload({ ...t, nextTemplateId: null })))
  }
  await refresh()
}

/** 空白模板，给「新建」用。建出来先落在 EVAL —— 那是链的第一环。id 由后端发。 */
export function blank(): Template {
  return {
    id: '',
    name: '',
    description: '',
    stage: 'EVAL',
    nextTemplateId: null,
    rules: {
      accountSize: null,
      profitTarget: null,
      maxDrawdown: null,
      drawdownType: 'TRAILING_EOD',
      trailingLockLevel: null,
      consistencyRatio: null,
      minProfitDays: null,
      profitDayThreshold: null,
      positionTiers: [],
    },
  }
}
