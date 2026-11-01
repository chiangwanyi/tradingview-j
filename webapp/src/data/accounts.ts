/**
 * PF 账户的接口与类型。字段对齐后端
 * `controller/PropFirmAccountController.java` + `entity/propfirm/PropFirmAccount.java`。
 *
 * 前端不自己算账：余额、阶段、规则快照全部由后端给，这里只做展示和发请求。
 *
 * 后端 `default-property-inclusion: non_null` 会把取值为 null 的字段整个省掉，
 * 所以拿到的对象里读到的是 `undefined` —— 判断一律写 `== null`。
 */

import { call, send } from './api'
import type { Rules, Stage } from './rules'

const BASE = '/api/pf-accounts'

export type AccountStatus = 'ACTIVE' | 'FAILED'

/** 一次考核。`stage` 是当前阶段，`status` 只区分「还在」和「考挂了」。 */
export interface Account {
  id: string
  name: string
  /** 从哪套模板开的（只用于追溯；真正生效的是下面的 rules 快照）。 */
  templateId: string
  /**
   * 这个账户唯一可交易的标的（`_symbols.json` 的键，如 `CME_MINI:MES1!`）。
   * **建户时定，之后不可改** —— 交易页直接读它，不再提供标的切换。
   */
  symbolId: string
  stage: Stage
  status: AccountStatus
  /**
   * **已达标、等升级**。后端在读接口里现算（从阶段起点重放到账户停住的那根 K 线），
   * 不是数据库列 —— 所以只有 `list` / `get` 拿到的对象上有它，写操作（成交、推进、改名）
   * 返回的账户没有（读到 `undefined`，一律写 `== null` 判断）。
   *
   * 首页的【升级】按钮就按它出不出来：**不要**拿 `profitTarget` 和 `minProfitDays`
   * 在这里自己推一遍，那是第二份考核口径。
   */
  passed?: boolean
  /** 开账户那一刻从模板复制过来的规则，之后与模板无关。 */
  rules: Rules
  /** 本阶段起点余额（升级时重置回账户规模）。 */
  baseBalance: number
  /** 本阶段「日终余额」历史最高，跟踪回撤靠它。 */
  eodPeakBalance: number
  /**
   * 本阶段**单日净盈利的最大值**（已扣手续费）。一致性规则的输入。
   *
   * 后端在日终结算时写回（`PropFirmSession#maxDayNet`），所以它是一个缓存的统计量，
   * 和 `eodPeakBalance` 同一个性质 —— 当天没结算之前，这一天还不参与「最大」。
   */
  maxDayNetBalance: number
  /**
   * **真实**盈利目标 = max(rules.profitTarget, 单日最大净盈利 / 一致性比例)。
   *
   * 后端算好发过来（`PropFirmAccount#getEffectiveProfitTarget()`），前端**不要自己算**：
   * 单日赚 2750、一致性 40% 时目标是 6875，而 `rules.profitTarget` 还是 3000 ——
   * 拿那个数显示就会得出「还差 40 就过关」的错结论。
   * `null`（被 non_null 省掉 = undefined）表示本阶段没有盈利目标（FUNDED / PAYOUT）。
   */
  effectiveProfitTarget?: number | null
  balance: number
  /** 进入当前阶段的时刻（ISO 字符串）。 */
  stageStartedAt: string
  /** 这行账户写进库的真实时刻。 */
  startedAt: string
  /** 模拟时间轴上的起点 K 线，如 "2024-03-04 09:00"。 */
  startBarTime: string
  startDayFile: string
  /**
   * 盘面现在停在哪一根 —— 账户的「最新 K 线时间」，如 "2024-03-04 09:35"。
   * 建户时 = `startBarTime`，用户每步进一根就往后走一格（只增不减）。
   * 交易页打开时**只揭示到这一根**。
   */
  lastBarTime: string
}

/** 阶段流水：这个账户当过哪几个阶段的什么身份。行数就说明身份。 */
export interface StageLog {
  id: string
  accountId: string
  seq: number
  stage: Stage
  startedAt: string
  endedAt: string | null
  baseBalance: number
  balanceAtEnd: number | null
  netPnl: number | null
  maxDailyProfit: number | null
  profitDays: number | null
  reason: 'CREATED' | 'PROMOTED'
  endReason: 'PROMOTED' | 'FAILED' | 'PAYOUT_DONE' | null
}

export const list = (status?: AccountStatus): Promise<Account[]> =>
  call(`${BASE}${status ? `?status=${status}` : ''}`)

/** 取单个账户。详情页用它自己拉一份，不依赖列表里那份可能已经过期的拷件。 */
export const get = (id: string): Promise<Account> => call(`${BASE}/${id}`)

/**
 * 开账户：模板 + 名字 + **交易标的**（三个都必填）。
 *
 * `symbolId` 只在建户这一刻有意义 —— 后端把它钉死在账户上，之后的改名、升级都不会碰它。
 */
export const open = (templateId: string, symbolId: string, name: string): Promise<Account> =>
  call(BASE, send('POST', { templateId, symbolId, name }))

/** 改名。只发 name：换模板或换标的都等于重开一个账户，后端也不认这两个字段。 */
export const rename = (id: string, name: string): Promise<Account> =>
  call(`${BASE}/${id}`, send('PUT', { name }))

export const remove = (id: string): Promise<null> => call(`${BASE}/${id}`, { method: 'DELETE' })

/** 升级到下一阶段。EVAL → FUNDED 会换规则、余额重置；FUNDED → PAYOUT 是终点。 */
export const promote = (id: string): Promise<Account> =>
  call(`${BASE}/${id}/promote`, { method: 'POST' })

/** 判失败（撞了亏损线）。终态，不会自动重开。 */
export const fail = (id: string): Promise<Account> => call(`${BASE}/${id}/fail`, { method: 'POST' })

/**
 * 步进一根：把账户的「盘面最新时间」推到 `barTime`（`yyyy-MM-dd HH:mm`）。
 *
 * 后端只接受**往后**的时间（退回去要回滚账，现在不做）。下一次「继续交易」
 * 就揭示到这一根。
 */
export const advanceCursor = (id: string, barTime: string): Promise<Account> =>
  call(`${BASE}/${id}/cursor`, send('POST', { barTime }))

/**
 * 【结算今日】：一步把盘面推到**下一个交易日**并结算上一个交易日。
 *
 * 端上不看时间、不挑那一根 —— 交易日是 [D 06:00, D+1 04:45)，只有后端算得对。
 * 有持仓或挂单时后端会拒绝（整段跳过不撮合中途的止损），这里照它的错误信息显示。
 */
export const settleDay = (id: string): Promise<Account> =>
  call(`${BASE}/${id}/settle-day`, { method: 'POST' })

export const stages = (id: string): Promise<StageLog[]> => call(`${BASE}/${id}/stages`)

/**
 * 交易日历上的一天（一个**交易日**，不是日历日）。
 *
 * `2026-10-29` 这一格装的是 `[10-29 06:00, 10-30 04:45)` 之间的成交，
 * 所以凌晨 03:10 平的那笔算前一天 —— 归日口径在后端 `PropFirmClock`，
 * 前端拿到的是已经分好组的天，不要自己再按 `entryBarTime` 切片。
 *
 * 金额是**净额**（已扣手续费），后端算好发过来。
 */
export interface DayPnl {
  /** 交易日 `YYYY-MM-DD` */
  day: string
  trades: number
  wins: number
  losses: number
  netPnl: number
  grossPnl: number
  commission: number
  /** 这一天的每一笔（只含已平仓的），按开仓时间升序 */
  items: DayTrade[]
}

/** 日历里一天的一笔：只留点开格子时要显示的那几列。 */
export interface DayTrade {
  id: string
  side: string | null
  quantity: number | null
  entryBarTime: string
  exitBarTime: string | null
  netPnl: number | null
}

/**
 * 这个账户的交易日历（全部历史，按交易日升序）。
 *
 * 只拉一次、按天分组在前端做月份筛选：一个考核号最多几百个交易日，
 * 一个月一次请求换来的只是「切月要等一下」。切月是纯前端动作，不该发请求。
 */
export const calendar = (id: string): Promise<DayPnl[]> => call(`${BASE}/${id}/calendar`)

/**
 * 当前该拿哪个数当「盈利目标」：**一致性抬高之后的目标**（后端给的 `effectiveProfitTarget`）。
 *
 * 退化到 `rules.profitTarget` 只发生在后端还没升级的时候 —— 那时至少显示模板目标，
 * 而不是空白。`null` = 本阶段没有目标（FUNDED / PAYOUT），调用方要自己处理。
 */
export function profitTarget(a: Account): number | null {
  const effective = a.effectiveProfitTarget
  if (effective != null) return effective
  return a.rules.profitTarget ?? null
}

/** 一致性把目标抬高了吗？（抬高了界面上要说明「为什么不是模板上那个 3000」） */
export function targetRaised(a: Account): boolean {
  const base = a.rules.profitTarget
  const real = profitTarget(a)
  return base != null && real != null && real > base + 0.005
}

/** 卡片上的进度：本阶段已赚到目标的几成。没有盈利目标（FUNDED / PAYOUT）返回 null。 */
export function targetProgress(a: Account): number | null {
  const target = profitTarget(a)
  if (target == null || target <= 0) return null
  const gained = a.balance - a.baseBalance
  return Math.min(1, Math.max(0, gained / target))
}

/**
 * 亏损线（美元）：本阶段日终峰值 − 最大回撤。
 *
 * **有一道冻结水位**（`rules.trailingLockLevel`，LucidFlex 50K 是 52100）：峰值到过这个数
 * 之后，亏损线停在 `水位 − 最大回撤`（50100），此后赚多少都不再上移。
 * 漏掉这一条就会让峰值涨一点、显示的线也涨一点 —— 涨的是「以为还剩多少缓冲」，
 * 而真实缓冲更小，正好在最危险的地方给出一个乐观数。
 *
 * 账户行里没有 `lossLine` 这个字段（后端只存峰值），所以这里按同一口径现算；
 * 交易页上那一条用的是后端每次重放算出来的 `PfView.lossLine`，两边必须一致。
 * 没有回撤规则就不编一个数，返回 `null`。
 */
export function lossLine(a: Account): number | null {
  const drawdown = a.rules.maxDrawdown
  if (drawdown == null) return null
  const lock = a.rules.trailingLockLevel
  const peak = lock != null && a.eodPeakBalance >= lock ? lock : a.eodPeakBalance
  return peak - drawdown
}

/** "2024-03-04 09:00" → "2024-03-04 09:00"，截到分钟，年月日全留（跨年账户要能分辨）。 */
export function shortTime(barTime: string): string {
  return barTime.length >= 16 ? barTime.slice(0, 16) : barTime
}

/** ISO → "YYYY-MM-DD HH:mm"（本地时区）。解析不了就原样给回去，别显示 Invalid Date。 */
export function shortInstant(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
