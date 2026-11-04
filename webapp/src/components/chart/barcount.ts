import type { Bar } from '@/types/protocol'

/**
 * Bar Count：在 K 线下方标出「这是当天的第几根」。
 *
 * 口径（用户定的，四条）：
 *   1. **按交易日重新数**，不是按日历日：交易日 D 从 `D 06:00` 起算，一直数到
 *      **次日 04:55/05:55 那根**（也就是下一个【缺口】的前一根），这一场数完，
 *      下一场再从 #1 开始。`00:00–04:55` 那几十根是**上一场的尾巴**，接着昨晚 23:55
 *      往下数，不能当成新的一天重新开张 —— 这是和「日历日」最大的差别，
 *      也是这个指标唯一容易改错的地方；
 *   2. **只显示单数**（1、3、5…）：这个指标是用来「两根一组」读节奏的，隔一根标一次
 *      密度刚好，数字不会糊成一片；
 *   3. **一分为二的那一刀在 06:00**，口径与后端 `PropFirmClock.SESSION_START` 一致。
 *      冬令时 CME 在北京时间 07:00 开盘，06:00 这一刀正好落在中间那段休市里
 *      （06:00–06:55 根本没有 K 线），所以同一刀两个季节都对：夏令时切在
 *      `04:55 → 06:00` 那个缺口上，冬令时切在 `05:55 → 07:00` 那个缺口上。
 *      **不要**拿「23:55」或「日历日变了」当边界 —— 那会把一场切成两段；
 *   4. 数字画在**这根 K 线的最低价下方**，颜色可调（见 `BarCountSettings.color`）。
 *
 * 为什么放在前端而不是后端：和 Doji X / ATR 一样是**显示参数**（颜色、开关），
 * 不是盘面事实 —— 关掉它不会改变任何一根 K 线的价格，也不该让别人看到我的设置。
 *
 * 已知的边界（两种「数不全」的情况，都不是算法能补的 —— 数据不在手里）：
 *   - 交易页给的这一段是「上一交易日整天 + 今天到现在」，所以**数组开头那几十根**
 *     （上一份日文件的 00:00–04:55）属于**更早那一场**的尾巴，它的 #1 不在数据里。
 *     这一段**整个不标号**：硬从 00:00 数成 #1 是错的（它的真实编号在 200 开外），
 *     凭空外推只会更错；
 *   - 如果这一段是「从某天的中间开始」的（比如某天没抓到 06:00 那根），那一场
 *     头几根不在内存里，编号就只能从手里第一根算起 —— 这种情况没法识别，只能认。
 */

/** 参数。开关 + 颜色，改一个就重画，不发给后端、不进存档。 */
export interface BarCountSettings {
  /** 总开关：关掉就一个数字都不画（参数仍然留着） */
  show: boolean
  /** 数字颜色（CSS 颜色串，界面上用取色器给） */
  color: string
}

/**
 * 出厂值：默认**打开**、蓝色。
 *
 * 颜色取自参考图里那串蓝字（#1d4ed8）：白底上比纯蓝易读，也不会和 K 线的黑白、
 * EMA 的琥珀、ATR 的紫撞色。
 */
export const DEFAULT_BAR_COUNT: BarCountSettings = {
  show: true,
  color: '#1d4ed8',
}

/**
 * 夹到合法值。
 *
 * 颜色只接受 `#rgb` / `#rrggbb`：手改 localStorage 塞进一个 `red;}` 之类的串会让
 * `ctx.fillStyle` 静默失效 —— 数字还是画得出来，但会变成上一个 fillStyle 的颜色，
 * 看起来像「颜色设置坏了」。认不出来就回出厂值。
 */
export function sanitizeBarCount(s: BarCountSettings): BarCountSettings {
  const color = typeof s.color === 'string' && /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(s.color)
    ? s.color
    : DEFAULT_BAR_COUNT.color
  return {
    show: s.show !== false,
    color,
  }
}

/** 一个标签：第 `index` 根 K 线，当天第 `count` 根。 */
export interface BarCountMark {
  index: number
  count: number
}

/**
 * 交易日切换时刻 `HH:mm`，和后端 `PropFirmClock.SESSION_START` **同一口径**。
 *
 * 后端那边这 06:00 管的是「成交归哪一天」「禁交易窗口从哪开始」；这边只管画号，
 * 但两边必须是同一刀 —— 否则图上说「今天第 3 根」、账簿说「昨天的尾巴」，看着都是对的。
 */
const SESSION_START = '06:00'

const DAY_MS = 86_400_000

const pad2 = (n: number): string => (n < 10 ? `0${n}` : String(n))

/**
 * 这根 K 线归哪个交易日（`yyyy-MM-dd` 键）。
 *
 * 06:00 及以后 = 当天这一场；06:00 以前 = **前一天**那一场（凌晨那段是尾巴）。
 * 只做字符串比较、只减一天，不碰时区：CSV 里存的是本地明文，换算反而会错一天。
 */
function sessionKey(time: string): string {
  const day = time.slice(0, 10)
  // 定长明文，字典序 = 时间序；`HH:mm` 直接比字符串就够
  if (time.slice(11, 16) >= SESSION_START) return day
  const t = new Date(Date.UTC(
    Number(day.slice(0, 4)),
    Number(day.slice(5, 7)) - 1,
    Number(day.slice(8, 10)),
  ) - DAY_MS)
  return `${t.getUTCFullYear()}-${pad2(t.getUTCMonth() + 1)}-${pad2(t.getUTCDate())}`
}

const EMPTY: BarCountMark[] = []

/**
 * 缓存：画布每帧都会来要一次，而这里要扫全量 K 线。
 *
 * 键用**数组本身**（WeakMap）：步进时 `instance-reducer` 是整份换新数组，
 * 所以「换了数组 = 数据变了」天然成立；再配 `length` 与 `lastTime` 兜底
 * （万一有人原地 push，不会拿到旧结果）。
 */
const cache = new WeakMap<Bar[], { length: number; lastTime: string; result: BarCountMark[] }>()

/** 入口：给定已揭示的 K 线，算出要画的那些单数编号。 */
export function barCountMarks(bars: Bar[]): BarCountMark[] {
  if (bars.length === 0) return EMPTY
  const lastTime = bars[bars.length - 1].time
  const hit = cache.get(bars)
  if (hit && hit.length === bars.length && hit.lastTime === lastTime) return hit.result

  const result: BarCountMark[] = []
  let key = ''
  let n = 0
  /** 这一组是不是「完整的交易日」：第一组如果从 06:00 之前开始，它的 #1 不在数据里 */
  let numbered = true
  for (let i = 0; i < bars.length; i += 1) {
    const k = sessionKey(bars[i].time)
    // 换了一场（06:00 那一刀）：从 #1 重新数
    if (k !== key) {
      key = k
      n = 0
      numbered = i > 0 || bars[i].time.slice(11, 16) >= SESSION_START
    }
    n += 1
    // 开头那段「上一场的尾巴」不标号 —— 它的编号在数据之外，编一个只会更错
    if (!numbered) continue
    if (n % 2 === 1) result.push({ index: i, count: n })
  }

  cache.set(bars, { length: bars.length, lastTime, result })
  return result
}
