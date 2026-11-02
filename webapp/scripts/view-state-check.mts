/**
 * 取景存档的解析自检（不引测试框架）：`npm run check`。
 *
 * 存档是用户能手改的 localStorage，也是老版本留下的残渣 —— 坏数据必须能落到
 * 「当没存过」，而不是把 NaN 灌进 scroll / barWidth 把画布搞成空白。
 */
import assert from 'node:assert/strict'

import { ChartScale } from '../src/components/chart/scale.ts'
import { sanitizeView } from '../src/lib/viewState.ts'

const ok = {
  anchorTime: '2026-10-30 15:40',
  anchorDxBars: -0.5,
  barWidth: 7,
  autoPrice: false,
  priceMin: 100,
  priceMax: 200,
}

assert.deepEqual(sanitizeView(ok), ok)

// 没有锚点就不知道该摆在哪 -> 整份作废
assert.equal(sanitizeView(null), null)
assert.equal(sanitizeView({ ...ok, anchorTime: 'abc' }), null)

// 单个字段坏了不连坐：坏的取默认，其余照用
assert.equal(sanitizeView({ ...ok, barWidth: 999 })?.barWidth, 40)
assert.equal(sanitizeView({ ...ok, barWidth: 0 })?.barWidth, 2)
assert.equal(sanitizeView({ ...ok, barWidth: 'x' })?.barWidth, 7)
assert.equal(sanitizeView({ ...ok, anchorDxBars: Number.NaN })?.anchorDxBars, 0)

// 价格轴：自动模式不保留残影；区间反了 / 缺货 -> 回自动
assert.equal(sanitizeView({ ...ok, autoPrice: true })?.autoPrice, true)
assert.equal(sanitizeView({ ...ok, priceMin: 300, priceMax: 200 })?.autoPrice, true)
assert.equal(sanitizeView({ ...ok, priceMax: null })?.autoPrice, true)
assert.equal(sanitizeView(ok)?.priceMin, 100)

/**
 * 存「锚点 K 线 + 相对根数」再还原，必须回到同一个 scroll。
 * 钉住的是 `ChartPanel` 的 `currentView` / `restoreView` 那一对（那边只是把这两步接起来）：
 * 哪天有人把锚点改回存下标，跨交易日后画面就会整体错位。
 */
const bars = Array.from({ length: 60 }, (_, i) => ({
  time: `2026-10-30 ${String(Math.floor(i / 12)).padStart(2, '0')}:${String((i % 12) * 5).padStart(2, '0')}`,
  open: 100, high: 101, low: 99, close: 100.5, volume: 1, ema20: null,
})) as never

for (const scroll of [0, 0.5, -0.3, 37.25, 59]) {
  const a = new ChartScale()
  a.setSize(1200, 600)
  a.setBars(bars)
  a.scroll = scroll
  a.clampScroll()

  const i = Math.max(0, Math.min(59, Math.floor(a.scroll)))
  const anchorTime = a.bars[i].time
  const dxBars = a.scroll - i

  const b = new ChartScale()
  b.setSize(1200, 600)
  b.setBars(bars)
  const idx = b.indexOfTime(anchorTime)
  assert.notEqual(idx, undefined, `锚点 ${anchorTime} 找不到`)
  b.scroll = (idx as number) + dxBars
  b.clampScroll()
  assert.ok(Math.abs(b.scroll - a.scroll) < 1e-9, `${scroll}: ${a.scroll} -> ${b.scroll}`)
}

console.log('view-state check ok')
