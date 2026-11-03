/**
 * 无头跑交易机器人：`node --experimental-strip-types scripts/bot-run.mts`
 *
 * 和页面里的机器人**同一份引擎**（`src/lib/bot/engine.ts`），只是把进度打到终端。
 * 用途是「改完后端先跑一遍」——不用开浏览器、不用点按钮，几秒钟就能拿到违例清单。
 *
 * ## 参数（全部走环境变量，避免和 npm 的参数分隔符打架）
 *
 * ```
 * BOT_BASE     后端根地址，默认 http://127.0.0.1:8792（**故意不用 8791**：
 *              那是用户正在用的实例，机器人会往库里塞账户）
 * BOT_WS       /ws 地址，默认由 BOT_BASE 推导
 * BOT_ACCOUNTS 开几个账户，默认 5
 * BOT_BARS     每户最多推进多少根，默认 80
 * BOT_SEED     随机种子，默认 20261103
 * BOT_TEMPLATE 模板 id；不给就自动挑第一个 EVAL 模板
 * BOT_SYMBOL   标的，默认 CME_MINI:MES1!
 * BOT_KEEP     =1 时跑完不删账户（默认删掉，免得库越跑越脏）
 * ```
 *
 * 退出码：0 = 没有违例；1 = 有违例（把清单打出来）；2 = 跑不起来。
 */

import { BotRun } from '../src/lib/bot/engine.ts'

interface Template {
  id: string
  name: string
  stage: string
}

const BASE = process.env.BOT_BASE ?? 'http://127.0.0.1:8792'
const ACCOUNTS = Number(process.env.BOT_ACCOUNTS ?? 5)
const BARS = Number(process.env.BOT_BARS ?? 80)
const SEED = Number(process.env.BOT_SEED ?? 20261103)
const SYMBOL = process.env.BOT_SYMBOL ?? 'CME_MINI:MES1!'
const KEEP = process.env.BOT_KEEP === '1'

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${BASE}${path}`, init)
  const body = (await response.json()) as { code: number; msg: string; data: T }
  if (body.code !== 0) throw new Error(`${path} → ${body.msg}`)
  return body.data
}

async function pickTemplate(): Promise<string> {
  if (process.env.BOT_TEMPLATE) return process.env.BOT_TEMPLATE
  const templates = await api<Template[]>('/api/pf-templates')
  const evalTemplate = templates.find((t) => t.stage === 'EVAL')
  if (!evalTemplate) throw new Error('库里没有 EVAL 模板：先在页面上建一套考核模板')
  return evalTemplate.id
}

async function main(): Promise<number> {
  const templateId = await pickTemplate()
  console.log(`后端 ${BASE}｜模板 ${templateId}｜标的 ${SYMBOL}｜${ACCOUNTS} 户 × ${BARS} 根｜seed ${SEED}`)

  const bot = new BotRun({
    baseUrl: BASE,
    wsUrl: process.env.BOT_WS,
    templateId,
    symbolId: SYMBOL,
    accounts: ACCOUNTS,
    maxBars: BARS,
    seed: SEED,
    concurrency: Number(process.env.BOT_CONCURRENCY ?? 3),
    namePrefix: '机器人-',
  })

  const timer = setInterval(() => {
    const s = bot.state
    console.log(`  进行中：${s.runs.filter((r) => r.status === 'RUNNING').length} 户在跑，`
      + `已推进 ${s.totalBars} 根，违例 ${s.violations.length}`)
  }, 3000)

  const state = await bot.run()
  clearInterval(timer)

  console.log('\n账户结果')
  for (const run of state.runs) {
    const bad = run.violations.length ? ` ⚠️ ${run.violations.length}` : ''
    console.log(`  ${run.name.padEnd(12)} ${run.status.padEnd(15)} 根 ${String(run.bars).padStart(3)}`
      + ` 成交 ${String(run.trades).padStart(3)}  余额 ${run.balance.toFixed(2).padStart(10)}`
      + `  权益 ${run.equity.toFixed(2).padStart(10)}${bad}${run.error ? `  ${run.error}` : ''}`)
  }

  const kinds = new Map<string, number>()
  for (const v of state.violations) kinds.set(v.kind, (kinds.get(v.kind) ?? 0) + 1)
  console.log(`\n合计：${state.totalBars} 根、${state.totalTrades} 笔成交、${state.totalOrders} 次下单、`
    + `${state.runs.filter((r) => r.status === 'FAILED_ACCOUNT').length} 户撞线、`
    + `${state.violations.length} 条违例`)

  if (state.violations.length) {
    console.log('\n违例分类')
    for (const [kind, count] of kinds) console.log(`  ${kind}: ${count}`)
    console.log('\n前 12 条')
    for (const v of state.violations.slice(0, 12)) {
      console.log(`  [${v.kind}] ${v.account} ${v.barTime} — ${v.detail}`)
    }
  }

  if (!KEEP) {
    for (const run of state.runs) {
      await fetch(`${BASE}/api/pf-accounts/${run.id}`, { method: 'DELETE' }).catch(() => undefined)
    }
    console.log('\n已删除本次建的账户（BOT_KEEP=1 可保留）')
  } else {
    console.log('\n账户已保留：' + state.runs.map((r) => r.id).join(', '))
  }

  return state.violations.length ? 1 : 0
}

main()
  .then((code) => process.exit(code))
  .catch((error) => {
    console.error(error instanceof Error ? error.message : error)
    process.exit(2)
  })
