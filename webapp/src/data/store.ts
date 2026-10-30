import { reactive } from 'vue'

import type { Template } from './rules'

/*
 * 模板存在 localStorage —— 后端目前只有 PropFirmTemplateService，没有 REST 接口。
 * 换成真接口时只改这个文件：load/save 换成 fetch，其余组件不用动。
 */

const KEY = 'webapp.pf-templates.v1'

/** 20 个字符够当 id 了，不引入 uuid 依赖。 */
function newId(): string {
  return Math.random().toString(36).slice(2, 12)
}

/** 一组 LucidFlex 的样例：EVAL 有目标、FUNDED 没目标，两条串成一条链。 */
function seed(): Template[] {
  const sizes: Array<[number, number, number, number]> = [
    // [规模, 盈利目标, 最大回撤, EVAL 手数上限]
    [25000, 1250, 1000, 20],
    [50000, 3000, 2000, 40],
    [100000, 6000, 4000, 60],
  ]
  const now = Date.now()
  const out: Template[] = []

  sizes.forEach(([size, target, drawdown, maxContracts], i) => {
    // 冻结水位只有 50K 有出处（PropFirmRules 的注释里写着 52100），其他档留空，不编。
    const lock = size === 50000 ? 52100 : null
    const k = size / 1000
    const fundedTiers = [{ fromProfit: 0, maxContracts }]

    out.push({
      id: `seed-eval-${k}`,
      name: `LucidFlex ${k}K EVAL`,
      description: '考试阶段：打到盈利目标即进下一套',
      stage: 'EVAL',
      nextTemplateId: `seed-funded-${k}`,
      rules: {
        accountSize: size,
        profitTarget: target,
        maxDrawdown: drawdown,
        drawdownType: 'TRAILING_EOD',
        trailingLockLevel: lock,
        consistencyRatio: 0.4,
        minProfitDays: null,
        profitDayThreshold: null,
        positionTiers: [{ fromProfit: 0, maxContracts }],
      },
      createdAt: now + i,
    })

    out.push({
      id: `seed-funded-${k}`,
      name: `LucidFlex ${k}K FUNDED`,
      description: '出金号：没有目标，攒够盈利日即可出金',
      stage: 'FUNDED',
      nextTemplateId: null,
      rules: {
        accountSize: size,
        profitTarget: null,
        maxDrawdown: drawdown,
        drawdownType: 'TRAILING_EOD',
        trailingLockLevel: lock,
        consistencyRatio: null,
        minProfitDays: 5,
        profitDayThreshold: 150,
        positionTiers: fundedTiers,
      },
      createdAt: now + i + 100,
    })
  })

  return out
}

function load(): Template[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) return JSON.parse(raw) as Template[]
  } catch {
    // 存的东西坏了就当没有，下面重新播种 —— 前端只有这一份数据，不给修复 UI
  }
  const fresh = seed()
  persist(fresh)
  return fresh
}

function persist(list: Template[]): void {
  localStorage.setItem(KEY, JSON.stringify(list))
}

export const store = reactive({ list: load() })

/** 建或改，按 id 是否存在判断。 */
export function save(t: Template): void {
  const i = store.list.findIndex((x) => x.id === t.id)
  if (i >= 0) store.list[i] = t
  else store.list.push(t)
  persist(store.list)
}

export function remove(id: string): void {
  store.list = store.list.filter((x) => x.id !== id)
  // 断链要一起修：留着指向已删模板的 id，升级路径就指向空气了
  store.list.forEach((t) => {
    if (t.nextTemplateId === id) t.nextTemplateId = null
  })
  persist(store.list)
}

/** 空白模板，给「新建」用。建出来先落在 EVAL —— 那是链的第一环。 */
export function blank(): Template {
  return {
    id: newId(),
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
    createdAt: Date.now(),
  }
}
