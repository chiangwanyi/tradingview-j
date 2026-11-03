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
  /**
   * **建户时**在哪个阶段 —— 由后端从阶段流水的第一行（`seq = 0`）读出来，同样不是列。
   *
   * 和 `stage` 一比就知道这个号的来路：两者不同 = 升上来的；相同 = 建户时就是它。
   * 首页那个阶段标签靠它区分「考核 → 出金号」和「一开始就是出金号」，
   * 这一格缺（`undefined`）表示这次没算，标签退回只写当前阶段。
   */
  originStage?: Stage
  /**
   * **已经走过几个交易日**（起点那根所在交易日 → `lastBarTime` 所在交易日，含两头）。
   *
   * 由后端按数据目录里**真实的开盘日**数出来（`TradingCalendar`），不是日历日、
   * 也不是「周一到周五」—— 休市日没有行情文件，按工作日数会多算。
   * 和 `passed` 一样是现算字段，写操作的返回里没有（读到 `undefined`）。
   */
  tradingDays?: number
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

/**
 * 进度轨道的三个位置（都是 0~1 的比例）：
 *
 *   0 ────── mark（起点）────── 1
 *   爆仓线                      目标
 *
 * 左端是**爆仓线**（`lossLine`，随日终峰值每天上移，不是固定值），右端是盈利目标，
 * 起点落在两者之间 —— **位置是算出来的**，不是画在正中。
 *
 * `fill` = 当前余额在轨道上的位置。没有盈利目标（PAYOUT，或没配目标的 FUNDED）时右端退化成日终峰值：
 * 那时要盯的是「离峰值和爆仓线各有多远」。
 */
export function equityTrack(a: Account): { lo: number; hi: number; mark: number; fill: number } {
  const lo = lossLine(a) ?? a.baseBalance
  const target = profitTarget(a)
  const hi = target != null ? a.baseBalance + target : Math.max(a.eodPeakBalance, a.balance, lo)
  const span = hi - lo
  const at = (v: number) => (span > 0 ? Math.min(1, Math.max(0, (v - lo) / span)) : 0)
  return { lo, hi, mark: at(a.baseBalance), fill: at(a.balance) }
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

/**
 * 阶段标签的配色档位（**只有语义，具体色号在 `badgeClass`**）：
 *
 * - `exam`            考试号 —— 灰
 * - `funded`          出金号（建户就是出金号）—— 绿
 * - `funded-upgraded` 出金号（从考试号考上来的）—— 粉
 *
 * 「成功」「失败」不是阶段，是终态标记，画在阶段标签**旁边**（见 `AccountCard.vue`），
 * 所以不在这里占一档。
 */
export type BadgeTone = 'exam' | 'funded' | 'funded-upgraded'

/** 阶段标签的三个部分：显示什么字、悬停说什么、哪一档配色。 */
export interface StageBadge {
  text: string
  hint: string
  tone: BadgeTone
}

/**
 * 标签的样式类。卡片和详情页共用一份 —— 两处各写一遍，改颜色时必然漏一处。
 *
 * 用 Tailwind 的语义色 + 少量固定色号：粉（升级上来的出金号）和琥珀（成功）没有语义色，
 * 就地写 `pink-*` / `amber-*`，别为它们往 `style.css` 里加变量（只有这一处用）。
 */
export function badgeClass(tone: BadgeTone): string {
  switch (tone) {
    case 'funded':
      return 'border-accent/40 bg-accent-soft text-accent-ink'
    case 'funded-upgraded':
      return 'border-pink-300 bg-pink-50 text-pink-700'
    default:
      return 'border-line bg-elev text-dim'
  }
}

/**
 * 账户阶段标签（卡片右上角 / 详情页标题旁）。
 *
 * 字面口径（用户 2026-11-01 定的）：EVAL = **考试号**（灰）、
 * 直开的 FUNDED = **出金号**（绿）、从考试号考上来的 FUNDED = **出金号（升级）**（粉）。
 * PAYOUT 是「出金号走完了」这个终态，标签仍是出金号，另挂一颗【成功】（在组件里）。
 *
 * **要区分「从考试升上来的」和「一开始就是出金号的」**：两者现在的 `stage` 都是 `FUNDED`，
 * 光看阶段说不出区别，所以拿后端给的 `originStage`（建户时的阶段，取自阶段流水第一行）
 * 和现在的 `stage` 比对 —— 不一样就是升上来的。
 *
 * 为什么不是后端直接给一个 `promoted` 布尔：升到出金有两条来路
 * （EVAL 一路考上来 / 建户就是出金号再升上来），一个布尔会把它们压成同一种，
 * 而这里要写的字并不一样。事实给全，字面在这儿组。
 *
 * `originStage` 缺（写操作的返回里没有）时退回「出金号」—— 不猜它是考上的。
 */
export function stageBadge(a: Account): StageBadge {
  if (a.stage === 'EVAL') {
    return {
      text: '考试号',
      tone: 'exam',
      hint: '考核阶段：有盈利目标，达标后升到出金号',
    }
  }
  const earned = a.originStage === 'EVAL'
  return {
    text: earned ? '出金号（升级）' : '出金号',
    tone: earned ? 'funded-upgraded' : 'funded',
    hint: earned
      ? '考试号考上去的出金号 —— 和「一开始就是出金号」的账户不是一回事'
      : a.originStage == null
        ? '建户时的阶段查不到流水，这里按出金号显示'
        : '建户时就是出金号（用这套模板直接开的），没经过考试',
  }
}

/** 走完了：PAYOUT 是终点（出金号已完成出金），卡片上另挂一颗【成功】。 */
export function finished(a: Account): boolean {
  return a.stage === 'PAYOUT'
}

/**
 * 标的长名 → 短名：`CME_MINI:MES1!` → `MES1!`。
 *
 * 账户上的 `symbolId` 是「交易所:代码」的全名（`_symbols.json` 的键，不能改），
 * 但卡片上那一格只是个身份角标，交易所前缀每次都一样，写全了既挤又难扫。
 * 所以**只切冒号**：取最后一段，没有冒号就原样返回 —— 不猜代码格式。
 */
export function shortSymbol(symbolId: string | null | undefined): string {
  if (!symbolId) return ''
  const at = symbolId.lastIndexOf(':')
  return at >= 0 ? symbolId.slice(at + 1) : symbolId
}

/**
 * 账户当前这套模板叫什么（如 `LucidFlex 50K EVAL`），查不到就返回空串。
 *
 * **不在账户行上**：`Account.templateId` 只有一个 id，名字在 `pf-templates` 里，
 * 由调用方把模板列表传进来（总览页本来就要拉一份给新建弹窗用，等于白捡）。
 * 之所以不让后端往账户 JSON 里塞一个 `templateName`：那是同一份事实的第二份投影，
 * 模板改名之后两边就会打架，而这里查一次是纯本地查找。
 *
 * 空串 = 模板被删了 / 列表还没加载回来 —— 界面上**不画这一格**，
 * 而不是画一个 id 冒充名字。
 */
export function templateName(a: Account, templates: { id: string; name: string }[]): string {
  return templates.find((t) => t.id === a.templateId)?.name ?? ''
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

/**
 * 已进行几个**交易日**（含起点那天）。
 *
 * ⚠️ **这个数只由后端给**（`tradingDays`）：交易日历是「数据目录里哪几天真的开了盘」，
 * 前端拿不到 —— 按日历日或多按「周一到周五」数会把圣诞、元旦、MLK 这些休市日算进去。
 * 所以这里不再自己数，缺了就返回 `null`，卡片上写「—」而不是编一个数字。
 *
 * 代价和 `passed` 一样：**写操作的返回里没有这个字段**（后端只在读接口里现算），
 * 但卡片永远吃的是 `list()` 的结果，所以界面上不会缺。
 */
export function tradingDaysElapsed(a: Account): number | null {
  return a.tradingDays ?? null
}
