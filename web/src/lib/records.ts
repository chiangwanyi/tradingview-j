import type { RecordSource, RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 交易记录的**统计口径**：把后端算好的每笔 stats 汇总成一张能看懂的报表。
 *
 * 分界必须说清楚，否则以后一定会有人在这里加「按价格重算盈亏」：
 *
 *  - **一笔的钱不在这里算**。盈亏、MFE/MAE、持仓根数全部来自 `RecordingSummary.stats`，
 *    它们是后端成交撮合的唯一口径（见 internal/replay/trade.go）。这里只做**求和、排序、
 *    取极值**，不碰价格、不碰手数乘数。
 *  - 所以「胜率」「盈亏比」这类**只看正负和个数**的派生指标，前端算一遍是安全的；
 *    真要改口径（比如按 R 倍数、扣手续费），改的地方是后端，不是这里。
 *
 * 另一个刻意的选择：函数全部是纯函数（输入列表 → 输出统计），不碰 store、不碰 DOM。
 * 页面换筛选条件时只是换一份输入，不存在「筛完之后状态没同步」这种问题。
 */

/** 一笔交易在时间轴上的位置：累计盈亏与回撤都相对它之前的所有笔。 */
export interface TradeRow {
  rec: RecordingSummary
  /** 1 起的序号（按交易时间正序） */
  seq: number
  /** 到这一笔为止的累计已实现盈亏 */
  cum: number
  /** 到这一笔为止的累计峰值（>= cum） */
  peak: number
  /** 这一笔之后的回撤（<= 0）；-100 就是「从峰值回撤了 100」 */
  dd: number
  /** 排序键：交易时间（入场那根 K 线的时间），格式 `YYYY-MM-DD HH:MM` */
  time: string
}

/** 一个方向（多 / 空）的汇总。 */
export interface SideStats {
  count: number
  /** 累计已实现盈亏 */
  pnl: number
  wins: number
  losses: number
  /** 平均每笔 */
  avg: number
  /** 平均持仓根数 */
  avgBars: number
}

/** 直方图的一格。`from`/`to` 是左闭右开的区间。 */
export interface HistBin {
  from: number
  to: number
  /** 落在这一格里的笔数 */
  count: number
  /** 区间中点，画图时当作横轴位置 */
  mid: number
  /** 格子标签（完整区间，例如 `-700 ~ -500`）：tooltip 用 */
  label: string
  /** 横轴刻度上的短标签（区间左边界）：类目轴塞不下完整区间，只写一个数才读得清 */
  tick: string
}

/** 按标的汇总（一笔交易记录只会有一个标的，多标的时才有意义）。 */
export interface SymbolStats {
  symbol: string
  count: number
  pnl: number
  wins: number
  winRate: number
  avg: number
}

/** 一份完整的统计结果。空列表时也是合法的（全是 0 / 空数组）。 */
export interface RecordAggregate {
  /** 参与统计的笔数 */
  count: number
  /** 其中正常平完的笔数 */
  closed: number
  /** 还在持仓中的笔数 */
  open: number
  wins: number
  losses: number
  /** 打平（盈亏为 0）的笔数 */
  flats: number
  /** 胜率 = 盈利笔数 / 已平仓笔数，0..1；没有已平仓交易时为 0 */
  winRate: number
  totalPnl: number
  /** 盈利单的盈亏之和（>= 0） */
  grossWin: number
  /** 亏损单的盈亏之和（<= 0） */
  grossLoss: number
  /** 盈亏比 = 总盈利 / |总亏损|；没有亏损单时为 Infinity，两边都没有时为 0 */
  profitFactor: number
  /** 期望值：平均每笔盈亏 */
  avgPnl: number
  avgWin: number
  /** 平均亏损（<= 0；用 0 表示没有亏损单） */
  avgLoss: number
  /** 最大回撤（正数）：累计曲线上「峰值 → 之后最低点」的最大落差 */
  maxDrawdown: number
  /** 最大回撤开始 / 结束的那一笔序号（1 起；没有回撤时为 0） */
  ddFrom: number
  ddTo: number
  maxWinStreak: number
  maxLossStreak: number
  avgBars: number
  totalBars: number
  totalFills: number
  totalSteps: number
  /** 单笔最佳 / 最差（按盈亏），空列表时为 null */
  best: TradeRow | null
  worst: TradeRow | null
  long: SideStats
  short: SideStats
  bySymbol: SymbolStats[]
  histogram: HistBin[]
  /** 按交易时间正序排好的每一笔 */
  rows: TradeRow[]
}

/** 页面上的筛选条件。空字符串 / 'all' 表示不限。 */
export interface RecordsFilter {
  /** 标的；'' = 全部 */
  symbol: string
  side: 'all' | 'long' | 'short'
  status: 'all' | 'closed' | 'open'
  /**
   * 操作来源（v0.21.0）：'' = 全部，'human' = 真人，'bot' = 全部机器人，
   * `bot:<策略 id>` = 某个策略（`bot:demo` 是随机演示）。
   */
  source: string
  /** 标签名（小写）。**同时满足**才留下：多选是收窄，不是并集 */
  tags: string[]
  /** 起止日期，`YYYY-MM-DD`；'' = 不限。两端都含（结束日按整天算） */
  from: string
  to: string
  /** 关键字：匹配名称 / 标的 / 操作者 / 标签 */
  keyword: string
}

export const EMPTY_FILTER: RecordsFilter = {
  symbol: '',
  side: 'all',
  status: 'all',
  source: '',
  tags: [],
  from: '',
  to: '',
  keyword: '',
}

/** 筛选条件是不是「什么都没限」。日历/统计页用它决定要不要显示「清除筛选」。 */
export function filterIsEmpty(f: RecordsFilter): boolean {
  return (
    !f.symbol &&
    f.side === 'all' &&
    f.status === 'all' &&
    !f.source &&
    f.tags.length === 0 &&
    !f.from &&
    !f.to &&
    !f.keyword.trim()
  )
}

// ---------------- 小工具 ----------------

const round2 = (n: number): number => Math.round(n * 100) / 100

/** 秒级时间戳 → `YYYY-MM-DD HH:MM`（本地时区，和后端给的 K 线时间同格式）。 */
export function clock(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return ''
  const d = new Date(at * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes(),
  )}`
}

/**
 * 一笔交易的时间。
 *
 * 优先用**入场那根 K 线的时间**（`stats.entryTime`），因为复盘看的是行情时间；
 * 老记录里它可能为空（没成交就断了），退到 anchor（记录第 1 步的 K 线），
 * 再退到「记录生成时间」。三种格式都是 `YYYY-MM-DD HH:MM`，可以直接字符串比大小 ——
 * 这比 new Date() 解析快得多，也不会因为时区/格式差异悄悄错一天。
 */
export function tradeTime(rec: RecordingSummary): string {
  return rec.stats.entryTime || rec.anchor || clock(rec.createdAt)
}

export function pnlText(n: number): string {
  return `${n >= 0 ? '+' : ''}${n.toFixed(2)}`
}

/** 千分位（大数字在卡片上挤在一起最难读） */
export function moneyText(n: number): string {
  const v = round2(n)
  const s = Math.abs(v).toFixed(2)
  const [int, frac] = s.split('.')
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${v < 0 ? '-' : ''}${grouped}.${frac}`
}

export function pctText(n: number, digits = 1): string {
  if (!Number.isFinite(n)) return '—'
  return `${(n * 100).toFixed(digits)}%`
}

/**
 * 金额的**短写**（日历格子、KPI 角标用）。
 *
 * 为什么另写一个而不是复用 moneyText：日历一格只有一百多像素宽，
 * `-2,000.00` 会挤成两行；而「今天亏了两千」不需要精确到分。所以：
 * 上千的数取整 + 千分位，小钱保留两位（`-31.50`），正数带 `+` —— 符号本身是信息。
 */
export function moneyShort(n: number, signed = true): string {
  const v = round2(n)
  const abs = Math.abs(v)
  const body =
    abs >= 1000
      ? String(Math.round(abs)).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
      : abs.toFixed(2)
  const sign = v < 0 ? '-' : v > 0 && signed ? '+' : ''
  return `${sign}${body}`
}

export function sizeText(n: number): string {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  if (n >= 1024) return `${Math.round(n / 1024)} KB`
  return `${n} B`
}

/** 方向 + 手数：`多 2` / `空 1` / `—`（没有成交的记录不会出现，兜一层而已）。 */
export function dirText(rec: RecordingSummary): string {
  const s = rec.stats
  if (s.side === 'long') return `多 ${s.quantity}`
  if (s.side === 'short') return `空 ${s.quantity}`
  return '—'
}

/** 盈亏的正负号：1 盈 / -1 亏 / 0 平。阈值取得比「分」更小，纯粹为了不把 1e-12 当成赚。 */
export function pnlSign(n: number): -1 | 0 | 1 {
  if (n > 1e-6) return 1
  if (n < -1e-6) return -1
  return 0
}

// ---------------- 操作来源 ----------------

/** 来源的三个字面量，和后端 protocol.go 里的常量一一对应。 */
export const SOURCE_HUMAN = 'human'
export const SOURCE_BOT = 'bot'
/** 随机演示机器人不是一个注册策略，用这个固定 id 代表它 */
export const SOURCE_DEMO = 'demo'
export const SOURCE_DEMO_NAME = '随机演示'

/**
 * 这一笔是谁做的。
 *
 * 后端在开仓那一刻就写死了 source（v0.21.0 起），这里只是**兜底**：老记录、
 * 或者将来某个把字段丢了的地方，一律按 createdBy 判断 —— 「记录上不显示来源」
 * 比「显示错了来源」更糟，因为筛选会悄悄漏掉它。
 */
export function sourceOf(rec: RecordingSummary): RecordSource {
  if (rec.source === SOURCE_BOT || rec.source === SOURCE_HUMAN) return rec.source
  return rec.createdBy === SOURCE_BOT ? SOURCE_BOT : SOURCE_HUMAN
}

/** 机器人的策略 id（真人是空串，随机演示是 `demo`）。 */
export function sourceIdOf(rec: RecordingSummary): string {
  if (sourceOf(rec) !== SOURCE_BOT) return ''
  return rec.sourceId || SOURCE_DEMO
}

/** 给人看的来源名：真人一律叫「手动」，机器人用策略名。 */
export function sourceNameOf(rec: RecordingSummary): string {
  if (sourceOf(rec) === SOURCE_HUMAN) return '手动'
  const id = sourceIdOf(rec)
  return rec.sourceName || (id === SOURCE_DEMO ? SOURCE_DEMO_NAME : id)
}

/** 筛选键：`human` / `bot`（机器人整体）/ `bot:<策略 id>`。 */
export function sourceKeyOf(rec: RecordingSummary): string {
  return sourceOf(rec) === SOURCE_BOT ? `${SOURCE_BOT}:${sourceIdOf(rec)}` : SOURCE_HUMAN
}

/** 一条记录是否命中来源筛选。`''` 表示不限。 */
export function matchSource(rec: RecordingSummary, key: string): boolean {
  if (!key) return true
  const src = sourceOf(rec)
  if (key === SOURCE_HUMAN) return src === SOURCE_HUMAN
  if (key === SOURCE_BOT) return src === SOURCE_BOT
  if (key.startsWith(`${SOURCE_BOT}:`)) {
    return src === SOURCE_BOT && sourceIdOf(rec) === key.slice(SOURCE_BOT.length + 1)
  }
  return true
}

/** 来源切换控件的一个选项。 */
export interface SourceOption {
  key: string
  label: string
  count: number
}

/**
 * 从记录里现算出「有哪些来源」——**不写死策略清单**。
 *
 * 策略是编译期注册的，将来加一个就自动出现在筛选里；反过来，某个策略一条记录
 * 都没有时也不该占一个永远筛出空结果的按钮。所以选项 = 数据里出现过的来源。
 */
export function sourceOptions(list: RecordingSummary[]): { all: number; human: number; bots: SourceOption[]; bot: number } {
  let human = 0
  const bots = new Map<string, { label: string; count: number }>()
  for (const r of list) {
    if (sourceOf(r) === SOURCE_HUMAN) {
      human++
      continue
    }
    const id = sourceIdOf(r)
    const cur = bots.get(id) ?? { label: sourceNameOf(r), count: 0 }
    cur.count++
    // 同一个 id 可能有新旧两种名字（策略改过名）：取更长的那个，信息更多
    if (sourceNameOf(r).length > cur.label.length) cur.label = sourceNameOf(r)
    bots.set(id, cur)
  }
  const list2 = [...bots.entries()]
    .map(([id, v]) => ({ key: `${SOURCE_BOT}:${id}`, label: v.label, count: v.count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
  return {
    all: list.length,
    human,
    bots: list2,
    bot: list2.reduce((n, b) => n + b.count, 0),
  }
}

// ---------------- 按天聚合（日历用） ----------------

/** 一天的汇总：日历格子上写的就是这几个数。 */
export interface DayStats {
  /** `YYYY-MM-DD`（交易时间的那一天，不是记录生成的那一天） */
  day: string
  count: number
  /** 其中正常平完的笔数 */
  closed: number
  /** 当天已实现盈亏合计 */
  pnl: number
  wins: number
  losses: number
  /** 当天的记录，按交易时间正序 —— 详情页照着这个顺序列 */
  rows: RecordingSummary[]
}

/** 这一笔属于哪一天（`YYYY-MM-DD`）。用的是 tradeTime，和列表里的时间列同源。 */
export function dayOf(rec: RecordingSummary): string {
  return tradeTime(rec).slice(0, 10)
}

/**
 * 把记录按「交易日」分组。
 *
 * 为什么是交易日而不是记录生成日：复盘时问的是「那天我怎么做的」，
 * 而记录生成时间只是「我什么时候点开这一段」（同一天可能补做前几天的行情）。
 * 两者混用会让日历上的格子对不上列表里的时间列 —— 那是最容易让人不信任的一张图。
 */
export function groupByDay(list: RecordingSummary[]): Map<string, DayStats> {
  const out = new Map<string, DayStats>()
  for (const rec of list) {
    const day = dayOf(rec)
    if (!day) continue
    let d = out.get(day)
    if (!d) {
      d = { day, count: 0, closed: 0, pnl: 0, wins: 0, losses: 0, rows: [] }
      out.set(day, d)
    }
    const s = rec.stats
    d.count++
    if (s.closed) d.closed++
    d.pnl = round2(d.pnl + s.realizedPnl)
    const sign = pnlSign(s.realizedPnl)
    if (sign > 0) d.wins++
    else if (sign < 0) d.losses++
    d.rows.push(rec)
  }
  for (const d of out.values()) {
    d.rows.sort((a, b) => tradeTime(a).localeCompare(tradeTime(b)))
  }
  return out
}

// ---------------- 标签 ----------------

/** 一个标签被用过多少次（打标签面板里的「已有标签」就是它）。 */
export interface TagUsage {
  name: string
  color: string
  count: number
}

/**
 * 统计标签用量。**同名不区分大小写**（和后端的去重口径一致）。
 *
 * 颜色取「最近一次用过的那个」：同一个人给「追单」先标黄、后来觉得该标红，
 * 面板上应该显示红 —— 那才是他下次想点的那一个。所以这里按记录时间从新到旧扫。
 */
export function tagUsage(list: RecordingSummary[]): TagUsage[] {
  const sorted = [...list].sort((a, b) => tradeTime(b).localeCompare(tradeTime(a)))
  const m = new Map<string, TagUsage>()
  for (const rec of sorted) {
    for (const t of rec.tags ?? []) {
      const key = t.name.trim().toLowerCase()
      if (!key) continue
      const cur = m.get(key)
      if (cur) cur.count++
      else m.set(key, { name: t.name.trim(), color: t.color, count: 1 })
    }
  }
  return [...m.values()].sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
}

/** 这条记录有没有某个标签（按名字，不区分大小写）。 */
export function hasTag(rec: RecordingSummary, name: string): boolean {
  const key = name.trim().toLowerCase()
  return (rec.tags ?? []).some((t) => t.name.trim().toLowerCase() === key)
}

/**
 * 去掉一个标签，返回新的那一组。**不改原数组**：这份数组来自 store，
 * 就地改会让「界面上已经变了、服务端还没回」的那一帧看起来像已经存好了。
 */
export function withoutTag(tags: RecordTag[] | undefined, name: string): RecordTag[] {
  const key = name.trim().toLowerCase()
  return (tags ?? []).filter((t) => t.name.trim().toLowerCase() !== key)
}

/** 加一个标签（同名则换成新的颜色），返回新的那一组。 */
export function withTag(tags: RecordTag[] | undefined, tag: RecordTag): RecordTag[] {
  const key = tag.name.trim().toLowerCase()
  const rest = (tags ?? []).filter((t) => t.name.trim().toLowerCase() !== key)
  return [...rest, { name: tag.name.trim(), color: tag.color }]
}

/**
 * 让标签的颜色在白底上读得清。
 *
 * 用户可能挑一个很亮的黄（#facc15）：直接拿来当文字色，白底上几乎看不见。
 * 这里按感知亮度把过亮的颜色压暗一档 —— 色相不变，深浅变了，用户还是认得出
 * 「这是我自己选的那个黄」。
 */
export function tagTextColor(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return '#475569'
  const n = parseInt(m[1], 16)
  const r = (n >> 16) & 0xff
  const g = (n >> 8) & 0xff
  const b = n & 0xff
  const lum = (0.299 * r + 0.587 * g + 0.114 * b) / 255
  if (lum <= 0.62) return `#${m[1].toLowerCase()}`
  const k = 0.55
  const to2 = (v: number) => Math.round(v * k).toString(16).padStart(2, '0')
  return `#${to2(r)}${to2(g)}${to2(b)}`
}



// ---------------- 筛选 ----------------

export function filterRecords(list: RecordingSummary[], f: RecordsFilter): RecordingSummary[] {
  const kw = f.keyword.trim().toLowerCase()
  return list.filter((r) => {
    if (f.symbol && r.symbol !== f.symbol) return false
    if (f.side !== 'all' && r.stats.side !== f.side) return false
    if (f.status === 'closed' && !r.stats.closed) return false
    if (f.status === 'open' && r.stats.closed) return false
    if (!matchSource(r, f.source)) return false
    // 标签是**全部命中**才留下：多选一个标签是在收窄，不是并集 ——
    // 复盘时想问的是「既追单又没设止损的那几笔」。
    for (const name of f.tags) {
      if (!hasTag(r, name)) return false
    }
    if (f.from || f.to) {
      const day = tradeTime(r).slice(0, 10) // YYYY-MM-DD
      if (f.from && day < f.from) return false
      if (f.to && day > f.to) return false
    }
    if (kw) {
      const tags = (r.tags ?? []).map((t) => t.name).join(' ')
      const hay = `${r.name} ${r.symbol} ${r.createdBy} ${r.stats.side} ${sourceNameOf(r)} ${tags}`.toLowerCase()
      if (!hay.includes(kw)) return false
    }
    return true
  })
}

/** 列表里出现过的标的（给下拉框用），按出现次数多到少。 */
export function symbolOptions(list: RecordingSummary[]): { symbol: string; count: number }[] {
  const m = new Map<string, number>()
  for (const r of list) m.set(r.symbol, (m.get(r.symbol) ?? 0) + 1)
  return [...m.entries()]
    .map(([symbol, count]) => ({ symbol, count }))
    .sort((a, b) => b.count - a.count || a.symbol.localeCompare(b.symbol))
}

// ---------------- 直方图 ----------------

/**
 * 把盈亏切成「好看的整数区间」。
 *
 * 为什么不用固定格数等分：等分出来的边界是 -697.5 / -461.3 这种数，横轴标签没法看。
 * 这里先把区间宽度凑成 1/2/5×10ⁿ，再对齐到整数边界 —— 图上每一格都是一句人话。
 */
function niceStep(range: number, bins: number): number {
  const raw = range / Math.max(1, bins)
  const mag = Math.pow(10, Math.floor(Math.log10(raw || 1)))
  const norm = raw / mag
  const step = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10
  return step * mag
}

function buildHistogram(values: number[]): HistBin[] {
  if (values.length === 0) return []
  const min = Math.min(...values)
  const max = Math.max(...values)
  if (max - min < 1e-9) {
    return [
      {
        from: min,
        to: min,
        count: values.length,
        mid: min,
        label: `${round2(min)}`,
        tick: fmtNum(min),
      },
    ]
  }
  const wanted = Math.max(4, Math.min(10, Math.round(Math.sqrt(values.length) * 1.5)))
  const step = niceStep(max - min, wanted)
  const from = Math.floor(min / step) * step
  const count = Math.ceil((max - from) / step) || 1
  const bins: HistBin[] = []
  for (let i = 0; i < count; i++) {
    const lo = from + i * step
    bins.push({
      from: lo,
      to: lo + step,
      count: 0,
      mid: lo + step / 2,
      label: `${fmtNum(lo)} ~ ${fmtNum(lo + step)}`,
      tick: fmtNum(lo),
    })
  }
  for (const v of values) {
    // 最大值落在最后一格的右边界上：归进最后一格，否则会多出一个空格子
    const i = Math.min(bins.length - 1, Math.floor((v - from) / step))
    bins[i].count++
  }
  return bins
}

function fmtNum(n: number): string {
  return Math.abs(n) >= 10 ? String(Math.round(n)) : String(round2(n))
}

// ---------------- 汇总 ----------------

function emptySide(): SideStats {
  return { count: 0, pnl: 0, wins: 0, losses: 0, avg: 0, avgBars: 0 }
}

/**
 * 汇总一批交易记录。
 *
 * 顺序很重要：先按**交易时间正序**排，再累加 —— 累计曲线、峰值、回撤、连胜连亏
 * 全都依赖这个顺序，而后端给的列表是「新 → 旧」。
 */
export function aggregate(list: RecordingSummary[]): RecordAggregate {
  const sorted = [...list].sort((a, b) => tradeTime(a).localeCompare(tradeTime(b)))

  const rows: TradeRow[] = []
  let cum = 0
  let peak = 0
  let maxDrawdown = 0
  let ddFrom = 0
  let ddTo = 0
  /** 峰值出现在第几笔（1 起，0 = 还没开始） */
  let peakAt = 0

  for (let i = 0; i < sorted.length; i++) {
    const rec = sorted[i]
    cum = round2(cum + rec.stats.realizedPnl)
    if (cum > peak) {
      peak = cum
      peakAt = i + 1
    }
    const dd = round2(cum - peak)
    if (dd < maxDrawdown) {
      maxDrawdown = dd
      ddFrom = peakAt
      ddTo = i + 1
    }
    rows.push({ rec, seq: i + 1, cum, peak, dd, time: tradeTime(rec) })
  }

  const agg: RecordAggregate = {
    count: sorted.length,
    closed: 0,
    open: 0,
    wins: 0,
    losses: 0,
    flats: 0,
    winRate: 0,
    totalPnl: rows.length ? rows[rows.length - 1].cum : 0,
    grossWin: 0,
    grossLoss: 0,
    profitFactor: 0,
    avgPnl: 0,
    avgWin: 0,
    avgLoss: 0,
    maxDrawdown: Math.abs(maxDrawdown),
    ddFrom: maxDrawdown < 0 ? ddFrom : 0,
    ddTo: maxDrawdown < 0 ? ddTo : 0,
    maxWinStreak: 0,
    maxLossStreak: 0,
    avgBars: 0,
    totalBars: 0,
    totalFills: 0,
    totalSteps: 0,
    best: null,
    worst: null,
    long: emptySide(),
    short: emptySide(),
    bySymbol: [],
    histogram: [],
    rows,
  }
  if (sorted.length === 0) return agg

  let winStreak = 0
  let lossStreak = 0
  const bySymbol = new Map<string, { count: number; pnl: number; wins: number }>()

  for (const row of rows) {
    const s = row.rec.stats
    const sign = pnlSign(s.realizedPnl)

    if (s.closed) agg.closed++
    else agg.open++
    agg.totalBars += s.barsHeld
    agg.totalFills += s.fills
    agg.totalSteps += row.rec.stepCount

    if (sign > 0) {
      agg.wins++
      agg.grossWin += s.realizedPnl
      winStreak++
      lossStreak = 0
    } else if (sign < 0) {
      agg.losses++
      agg.grossLoss += s.realizedPnl
      lossStreak++
      winStreak = 0
    } else {
      agg.flats++
      winStreak = 0
      lossStreak = 0
    }
    agg.maxWinStreak = Math.max(agg.maxWinStreak, winStreak)
    agg.maxLossStreak = Math.max(agg.maxLossStreak, lossStreak)

    const side = s.side === 'long' ? agg.long : s.side === 'short' ? agg.short : null
    if (side) {
      side.count++
      side.pnl = round2(side.pnl + s.realizedPnl)
      side.avgBars += s.barsHeld
      if (sign > 0) side.wins++
      else if (sign < 0) side.losses++
    }

    const sym = bySymbol.get(row.rec.symbol) ?? { count: 0, pnl: 0, wins: 0 }
    sym.count++
    sym.pnl += s.realizedPnl
    if (sign > 0) sym.wins++
    bySymbol.set(row.rec.symbol, sym)

    if (!agg.best || s.realizedPnl > agg.best.rec.stats.realizedPnl) agg.best = row
    if (!agg.worst || s.realizedPnl < agg.worst.rec.stats.realizedPnl) agg.worst = row
  }

  agg.grossWin = round2(agg.grossWin)
  agg.grossLoss = round2(agg.grossLoss)
  agg.totalPnl = round2(agg.totalPnl)
  agg.avgPnl = round2(agg.totalPnl / agg.count)
  agg.avgWin = agg.wins ? round2(agg.grossWin / agg.wins) : 0
  agg.avgLoss = agg.losses ? round2(agg.grossLoss / agg.losses) : 0
  agg.winRate = agg.closed ? agg.wins / agg.closed : 0
  agg.profitFactor =
    agg.grossLoss < 0 ? agg.grossWin / Math.abs(agg.grossLoss) : agg.grossWin > 0 ? Infinity : 0
  agg.avgBars = round2(agg.totalBars / agg.count)

  for (const side of [agg.long, agg.short]) {
    side.avg = side.count ? round2(side.pnl / side.count) : 0
    side.avgBars = side.count ? round2(side.avgBars / side.count) : 0
  }

  agg.bySymbol = [...bySymbol.entries()]
    .map(([symbol, v]) => ({
      symbol,
      count: v.count,
      pnl: round2(v.pnl),
      wins: v.wins,
      winRate: v.count ? v.wins / v.count : 0,
      avg: v.count ? round2(v.pnl / v.count) : 0,
    }))
    .sort((a, b) => b.pnl - a.pnl)

  agg.histogram = buildHistogram(rows.map((r) => r.rec.stats.realizedPnl))
  return agg
}

/** 表格排序：`time` / `pnl` / `bars`，dir=1 升序、-1 降序。 */
export type SortKey = 'time' | 'pnl' | 'bars'

export function sortRows(rows: TradeRow[], key: SortKey, dir: 1 | -1): TradeRow[] {
  const pick = (r: TradeRow): number =>
    key === 'pnl' ? r.rec.stats.realizedPnl : key === 'bars' ? r.rec.stats.barsHeld : 0
  return [...rows].sort((a, b) => {
    if (key === 'time') return a.time.localeCompare(b.time) * dir
    return (pick(a) - pick(b)) * dir || a.seq - b.seq
  })
}
