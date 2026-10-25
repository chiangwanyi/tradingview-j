import type { Bar } from '@/types/protocol'

/**
 * Bar Count：在 K 线下方标出「这是当天的第几根」。
 *
 * 口径（用户定的，四条）：
 *   1. **每张 CSV（= 每个交易日）从 #1 重新数**，而不是整份数据从 1 数到底。
 *      实例加载时每一天都是**整日文件**（见 internal/replay/data.go：锚点日整日收、
 *      往前往后也都是整日），所以「这一天在数组里的第一根」就是 CSV 的第一行 ——
 *      也就是 00:00 或 06:00 那一根；
 *   2. **只显示单数**（1、3、5…）：这个指标是用来「两根一组」读节奏的，隔一根标一次
 *      密度刚好，数字不会糊成一片；
 *   3. 一天数完，下一天回到 #1 —— 判据是 `time` 的日期部分变化（`yyyy-MM-dd HH:MM` 的前 10 位）；
 *   4. 数字画在**这根 K 线的最低价下方**，颜色可调（见 `BarCountSettings.color`）。
 *
 * 为什么放在前端而不是后端：和 Doji X / ATR 一样是**显示参数**（颜色、开关），
 * 不是盘面事实 —— 关掉它不会改变任何一根 K 线的价格，也不该让别人看到我的设置。
 *
 * 已知的边界：如果实例是「从某天的中间开始」的（比如交易记录回放里那份 400 根的起点快照
 * 落在半天上），那一天的头几根不在内存里，编号就只能从手里第一根算起。
 * 这不是算法能补的 —— 数据都不在，凭空外推一个编号只会更错。
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
  let day = ''
  let n = 0
  for (let i = 0; i < bars.length; i += 1) {
    const d = bars[i].time.slice(0, 10)
    // 换了一张表（换了一天）：从 #1 重新数
    if (d !== day) {
      day = d
      n = 0
    }
    n += 1
    if (n % 2 === 1) result.push({ index: i, count: n })
  }

  cache.set(bars, { length: bars.length, lastTime, result })
  return result
}
