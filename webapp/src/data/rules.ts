/**
 * PF 账户模板的类型与校验。
 *
 * 字段名、语义、校验口径都对齐后端
 * `src/main/java/com/tradingviewj/server/entity/propfirm/PropFirmTemplate.java` +
 * `PropFirmRules.java`：这里只是它的一份前端镜像，不另立规则。
 * 后端改了字段，这个文件要同步改。
 */

/** 账户所处阶段。单向 EVAL → FUNDED → PAYOUT。 */
export type Stage = 'EVAL' | 'FUNDED' | 'PAYOUT'

/** 回撤口径。EOD = 跟踪日终余额高点。 */
export type DrawdownType = 'STATIC' | 'TRAILING_INTRADAY' | 'TRAILING_EOD'

/** 手数上限的一档：净盈利达到 fromProfit 之后，净持仓最多 maxContracts 手。 */
export interface PositionTier {
  fromProfit: number
  maxContracts: number
}

/**
 * 一个阶段的规则。
 *
 * null 是**有意义的取值**，不是「没填」：profitTarget == null 读作「这个阶段没有盈利目标」。
 */
export interface Rules {
  accountSize: number | null
  profitTarget: number | null
  maxDrawdown: number | null
  drawdownType: DrawdownType
  trailingLockLevel: number | null
  /** 0~1，例如 0.5。单日最大净盈利 / 阶段总净盈利 不得超过它。 */
  consistencyRatio: number | null
  minProfitDays: number | null
  profitDayThreshold: number | null
  positionTiers: PositionTier[]
}

export interface Template {
  id: string
  name: string
  description: string
  stage: Stage
  /**
   * 达标后接哪套模板；null = 升到尽头。
   *
   * 提示：后端 `default-property-inclusion: non_null` 会把取值为 null 的字段整个省掉，
   * fetch 回来的对象里读到的是 `undefined`。所以判断一律写 `== null`，别写 `=== null`。
   */
  nextTemplateId: string | null
  rules: Rules
}

/**
 * 模板自身是否自洽 —— 和 `PropFirmTemplate.validate` 同一套条件。
 *
 * 重点是后两条：EVAL 没目标等于没考试；FUNDED 带目标实际上是第二场考试，
 * 这类错配在界面上看起来完全正常，只会让账户永远升不了级，所以宁可存不进去。
 */
export function validate(t: Template): string[] {
  const errors: string[] = []
  const r = t.rules

  if (!t.name.trim()) errors.push('模板必须有名字')
  if (r.accountSize == null || r.accountSize <= 0) errors.push('账户规模必须是正数')
  if (r.maxDrawdown == null || r.maxDrawdown <= 0) errors.push('最大回撤必须是正数')
  if (t.stage === 'EVAL' && r.profitTarget == null) errors.push('EVAL 阶段必须有盈利目标')
  if (t.stage === 'FUNDED' && r.profitTarget != null)
    errors.push('FUNDED 阶段不能有盈利目标（那是 EVAL 的规则）')

  return errors
}

const DRAW_DOWN_LABEL: Record<DrawdownType, string> = {
  STATIC: '静态',
  TRAILING_INTRADAY: '跟踪日内',
  TRAILING_EOD: 'EOD',
}

export function drawdownLabel(d: DrawdownType): string {
  return DRAW_DOWN_LABEL[d]
}

export function stageLabel(s: Stage): string {
  return s === 'EVAL' ? '考核' : s === 'FUNDED' ? '出金号' : '出金'
}

/** 1250 -> "$1,250"；null -> "—" */
export function usd(n: number | null): string {
  return n == null ? '—' : `$${n.toLocaleString('en-US')}`
}

/** 0.4 -> "40%"；null -> "—" */
export function pct(n: number | null): string {
  return n == null ? '—' : `${Math.round(n * 1000) / 10}%`
}

/** 分档表压成一行："0→40 · 1000→30"；空表 = 不限手数。 */
export function tiersLabel(tiers: PositionTier[]): string {
  if (tiers.length === 0) return '不限'
  return tiers.map((t) => `${t.fromProfit}→${t.maxContracts}`).join(' · ')
}
