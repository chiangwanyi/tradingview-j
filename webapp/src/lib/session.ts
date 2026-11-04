/**
 * 交易时段（用户 2026-11-01 定的规则，前端只做「提前置灰」）。
 *
 * ```
 *   交易日 D = [D 06:00, (D+1) 04:00)
 *   禁止交易时段 = [D 04:00, D 06:00]      ← 两端都关
 * ```
 *
 * 由此推出每天最早可交易 **06:05**、最晚可交易 **03:55**（K 线 5 分钟一根，04:00 那根就进窗口了）。
 *
 * ⚠️ 判官始终是后端：`PropFirmClock.tradable` 会在 `requireTradable` 里现算一次，
 * 日终强平（04:00 按收盘价平仓 + 撤单）也在后端做。这里的函数只是为了把按钮提前关掉、
 * 把原因写在按钮上 —— 让用户点下去再收到一条报错，等于把规则藏在错误弹窗里。
 *
 * 口径要和 `PropFirmClock` 一字不差地同步；改了一边就必须改另一边（`PropFirmClockTest` 钉的是后端那份）。
 */

/** 交易日结束时刻（含）：这一刻只能平仓，不能开新仓。 */
export const SESSION_CLOSE = '04:00'

/** 下一交易日开始时刻（含）：这一根归新的一天，但本身不可交易。 */
export const SESSION_START = '06:00'

/** 每天最后一根可交易的 K 线 / 第一根可交易的 K 线。 */
export const LAST_TRADABLE = '03:55'
export const FIRST_TRADABLE = '06:05'

/** 从 `"2024-08-21 04:05"` 里取 `"04:05"`；格式不认识时返回空串。 */
function hhmm(barTime: string): string {
  const t = barTime.trim()
  return t.length >= 16 ? t.slice(11, 16) : ''
}

/**
 * 这根 K 线所在的时刻能不能下单。
 *
 * 没有盘面（{@code barTime} 为空）时返回 {@code true}：那种情况下连市价单都取不到收盘价，
 * 后端会给出更准确的报错，凭空锁住按钮只会误导。
 */
export function isTradableBar(barTime: string): boolean {
  const at = hhmm(barTime)
  if (at.length !== 5) return true
  return at < SESSION_CLOSE || at > SESSION_START
}

/** 不能下单的原因；能下单时返回 {@code null}。 */
export function sessionBlockReason(barTime: string): string | null {
  if (isTradableBar(barTime)) return null
  return `盘面停在 ${barTime}，落在禁止交易时段 04:00–06:00 内：这段时间只能平仓和撤单，不能开新仓（每天最早 ${FIRST_TRADABLE}、最晚 ${LAST_TRADABLE} 可以下单）`
}
