/**
 * 画布取景的**本机存档**（浏览器 localStorage，按账户一份）。
 *
 * 存的是用户拖完 / 缩完之后停在哪：锚点 K 线 + 相对根数、柱子宽度、价格轴。
 * 和指标偏好（`indicatorPrefs.ts`）同族 —— 纯显示状态，不进后端、不写操作记录。
 *
 * 锚点存**时间**不存下标：刷新后快照给的窗口左端会随交易日滑动，同一个下标
 * 指向的是另一根 K 线。口径同 `chart/scale.ts` 的 `BarRef`。
 */

/** 左边缘 = 「某根真实 K 线」+ 向右几根（可为负 / 小数）。 */
export interface ViewState {
  anchorTime: string
  anchorDxBars: number
  barWidth: number
  autoPrice: boolean
  priceMin: number
  priceMax: number
}

/** 改字段语义时换版本号，老存档自然失效，不写迁移。 */
const KEY_PREFIX = 'tv-webapp-view-v1:'

/** 协议里的 K 线时间是定长文本，顺带挡住手改出来的 `"abc"`。 */
const TIME_RE = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

/**
 * 任意输入 -> 可用存档；收不出来就 `null`（= 当没存过）。
 *
 * 锚点是唯一的必需项：没有它就不知道该把画面摆在哪，剩下几个数没有意义。
 * 价格区间只在「用户手动缩过（`autoPrice === false`）且上下界正常」时保留，
 * 否则回自动贴合 —— 存档里那两个数只是 `fitPrice()` 的残影。
 */
export function sanitizeView(raw: unknown): ViewState | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const o = raw as Record<string, unknown>
  if (typeof o.anchorTime !== 'string' || !TIME_RE.test(o.anchorTime)) return null

  const barWidth = num(o.barWidth)
  const lo = num(o.priceMin)
  const hi = num(o.priceMax)
  const manual = o.autoPrice === false && lo !== null && hi !== null && hi > lo

  return {
    anchorTime: o.anchorTime,
    anchorDxBars: num(o.anchorDxBars) ?? 0,
    barWidth: barWidth === null ? 7 : Math.min(40, Math.max(2, barWidth)),
    autoPrice: !manual,
    priceMin: manual && lo !== null ? lo : 0,
    priceMax: manual && hi !== null ? hi : 1,
  }
}

/** 读存档。没存过 / JSON 坏了 / 无痕模式里 getItem 抛异常 —— 一律当没存过。 */
export function loadView(accountId: string): ViewState | null {
  if (!accountId) return null
  try {
    const raw = localStorage.getItem(KEY_PREFIX + accountId)
    return raw ? sanitizeView(JSON.parse(raw) as unknown) : null
  } catch {
    return null
  }
}

/** 写存档。无痕模式 / 配额满就记不住，不影响这次使用。 */
export function saveView(accountId: string, view: ViewState): void {
  if (!accountId) return
  try {
    localStorage.setItem(KEY_PREFIX + accountId, JSON.stringify(view))
  } catch {
    // 隐私模式下 localStorage 会直接抛
  }
}
