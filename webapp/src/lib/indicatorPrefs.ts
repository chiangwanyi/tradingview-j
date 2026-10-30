import { DEFAULT_ATR, sanitizeAtr, type AtrSettings } from '@/components/chart/atr'
import { DEFAULT_BAR_COUNT, sanitizeBarCount, type BarCountSettings } from '@/components/chart/barcount'
import { DEFAULT_INDICATOR, sanitizeIndicator, type IndicatorSettings } from '@/components/chart/indicators'

/**
 * 指标设置的**本机存档**（浏览器 localStorage）。
 *
 * 存的是【指标 ▾】里那四样东西的开关与参数：
 *
 *   showEma    EMA20 开 / 关（参数在后端算，没有可调的）
 *   atr        ATR 的 show / length / smoothing
 *   indicator  Doji X + Blue ZigZag 的 show / lookback / dojiSize
 *   barCount   Bar Count 的 show / color
 *
 * ## 为什么存浏览器，而不进后端存档
 *
 * 这几项全是**显示参数**，不是盘面事实（判断口径见 `indicators.ts` 开头那段）：
 * 关掉 Bar Count 不会改变任何一根 K 线的价格，别人也不该看到我的颜色。
 * 后端 `audit_event` 那套是给「账户发生了什么」用的，把「我今天喜欢多大的 Lag」
 * 塞进去只会污染操作记录。所以它和【下单】浮窗的位置（`TradeFloat` 的
 * `tv-trade-float-pos`）一样，属于**这台电脑这个浏览器**的偏好。
 *
 * ## 三条必须守住的
 *
 * 1. **读出来的东西一律当不可信**：localStorage 是用户能手改的 —— JSON 语法坏了、
 *    字段被删了、`length` 被写成 `"abc"` 或 `-5` 都是常态。所以 `loadPrefs()` 从不
 *    直接吐解析结果，全部过一遍 `sanitizeAtr` / `sanitizeIndicator` / `sanitizeBarCount`，
 *    单个字段坏了就回它自己的出厂值，**不连坐**另外三个（少一个指标不该把其余全清掉）。
 * 2. **隐私模式下不炸**：`localStorage.getItem / setItem` 在部分浏览器的无痕窗口里会
 *    直接抛异常。记不住偏好不影响这一次使用，所以两头都 try/catch 兜住。
 * 3. **键名带版本号**（`-v1`）：将来字段改名/改语义时换个键，老存档自然失效回到默认，
 *    不需要写迁移代码。真要保留老数据，再加一个读旧键的分支。
 */

/** 存档的键。改字段语义时**换版本号**，别就地改 —— 见上面第 3 条。 */
const PREFS_KEY = 'tv-trade-indicators-v1'

/** 一份完整的指标偏好。字段名和 store 里的那几个 ref 一一对应。 */
export interface IndicatorPrefs {
  showEma: boolean
  atr: AtrSettings
  indicator: IndicatorSettings
  barCount: BarCountSettings
}

/** 出厂状态 = 四个模块各自的 `DEFAULT_*`（也就是第一次打开这一页时的样子）。 */
export const DEFAULT_INDICATOR_PREFS: IndicatorPrefs = {
  showEma: true,
  atr: DEFAULT_ATR,
  indicator: DEFAULT_INDICATOR,
  barCount: DEFAULT_BAR_COUNT,
}

/** 把任意值收成一个普通对象：`null`、数组、字符串、数字一律当空对象（= 全取出厂值）。 */
function asObject(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
}

/**
 * 把任意输入夹成一份合法偏好。
 *
 * 做法是「先铺出厂值、再覆盖存档里的同名字段、最后过一遍各自的 sanitize」：
 * 这样**缺字段**（老存档、手删了一半）和**字段值非法**（手改成 `"abc"`）都能落到
 * 同一个出口，不用为每种坏法各写一段判断。
 */
export function sanitizePrefs(raw: unknown): IndicatorPrefs {
  const o = asObject(raw)
  return {
    // showEma 是纯布尔：只认真正的 true / false，别的一律回出厂值
    showEma: typeof o.showEma === 'boolean' ? o.showEma : DEFAULT_INDICATOR_PREFS.showEma,
    atr: sanitizeAtr({ ...DEFAULT_ATR, ...(asObject(o.atr) as Partial<AtrSettings>) }),
    indicator: sanitizeIndicator({ ...DEFAULT_INDICATOR, ...(asObject(o.indicator) as Partial<IndicatorSettings>) }),
    barCount: sanitizeBarCount({ ...DEFAULT_BAR_COUNT, ...(asObject(o.barCount) as Partial<BarCountSettings>) }),
  }
}

/** 读存档。没有存档、解析失败、localStorage 不可用 —— 一律回出厂值，不抛。 */
export function loadPrefs(): IndicatorPrefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (!raw) return sanitizePrefs(null)
    return sanitizePrefs(JSON.parse(raw) as unknown)
  } catch {
    // JSON 坏了 / 无痕模式里 getItem 抛异常：都按「没存过」处理
    return sanitizePrefs(null)
  }
}

/**
 * 写存档。传进来的对象先过 {@link sanitizePrefs}，顺手把不认识的老字段丢掉
 * （存档只会长成一份精确的形状，不用怕字段越攒越多）。
 *
 * 取色器拖一下会连着触发几十次 `input`，每次写一遍这个百来字节的串 —— 实测代价
 * 可以忽略（localStorage 同步写 <0.1ms），不值得为它加防抖再引一层时序问题。
 */
export function savePrefs(prefs: IndicatorPrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(sanitizePrefs(prefs)))
  } catch {
    // 隐私模式 / 配额满：记不住偏好不影响这次使用
  }
}
