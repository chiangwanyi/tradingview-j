/**
 * AB=CD 的自检（不引测试框架）：`npm run check`。
 *
 * 钉住的只有一条：**D = C + (B - A)**。它在屏幕坐标里算，因为 x 与时间下标、
 * y 与价格都是仿射关系 —— 所以「屏幕上同一段向量」等价于「同样根数、同样价差」。
 * 这条错了不会报错，只会画出一个看着像 AB=CD、其实两边不等长的图形。
 */
import assert from 'node:assert/strict'

import { buildFigure, type FigurePoint } from '../src/components/chart/annotations.ts'

/** 假视图：时间用数字字符串当横轴刻度，价格直接翻成屏幕 y */
const view = {
  plot: { left: 0, top: 0, right: 1000, bottom: 1000 },
  xOf: (p: FigurePoint) => Number(p.time) * 10 + (p.offset ?? 0) * 10,
  yOfPrice: (price: number) => 1000 - price,
  priceText: (price: number) => String(price),
}

const at = (time: string, price: number): FigurePoint => ({ time, price })

// A(0,100) → B(1,200) → C(3,150)：D 应当是 (4,250)，屏幕上就是 C 平移了 AB 那一段
const f = buildFigure({ id: 'a1', kind: 'abcd', start: at('0', 100), mid: at('1', 200), end: at('3', 150) }, view)
assert.ok(f, 'A/B/C 齐全时必须画得出来')

const [ab, bc, cd] = f.segs
assert.equal(ab.x1, 0)
assert.equal(ab.y1, 900)
assert.equal(bc.x2, 30, 'BC 从 B 画到 C')
assert.deepEqual([cd.x1, cd.y1, cd.x2, cd.y2], [30, 850, 40, 750], 'CD 从 C 起，终点是算出来的 D')
assert.deepEqual(
  [cd.x2 - cd.x1, cd.y2 - cd.y1],
  [ab.x2 - ab.x1, ab.y2 - ab.y1],
  'CD 与 AB 平行且等长（屏幕上同一段向量）',
)

// 过 A / B / D 三条水平线，横跨整个图形
const levels = f.segs.slice(3).map((s) => s.y1)
assert.deepEqual(levels, [900, 800, 750], '水平线过 A、B、D 的价位')
for (const s of f.segs.slice(3)) {
  assert.equal(s.y1, s.y2, '水平线必须是平的')
  assert.deepEqual([s.x1, s.x2], [0, 40], '横跨 A..D')
}

// 手柄是用户给的三个点（D 是算出来的，拖不了）
assert.deepEqual(
  f.anchors.map((a) => [a.x, a.y]),
  [
    [0, 900],
    [10, 800],
    [30, 850],
  ],
)

// 只点了 A 的预览：没有 B 就没有 D，但仍然要看得见一条线
const preview = buildFigure({ id: 'g', kind: 'abcd', start: at('0', 100), end: at('2', 120) }, view)
assert.deepEqual(preview?.segs.length, 1, '预览只有一个线段')
