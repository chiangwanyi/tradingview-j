import type { ChartPalette } from '@/lib/chart'
import { moneyText, pctText, pnlText } from '@/lib/records'
import type { RecordAggregate, TradeRow } from '@/lib/records'
import type { EChartsCoreOption } from 'echarts/core'

/**
 * 六张统计图的 option。
 *
 * 为什么单独一个文件、而且**全是纯函数**（aggregate + 配色 → option）：
 * 图的形状（坐标系、双轴、标注）是这套报表的「口径」之一，和数字同样需要能一眼读完。
 * 放在 .vue 里会和模板、筛选器混在一起，改一个轴就要在模板之间来回找。
 *
 * 三条共同的约定：
 *  1. **红涨绿跌**：盈利用 up（红），亏损用 down（绿）。和 K 线、列表完全一致。
 *  2. 坐标轴、网格线、tooltip 的颜色一律取自 `ChartPalette`（源头是 CSS 变量），
 *     这样切浅色/深色只需要重算 option，不用维护第二份配色表。
 *  3. 空数据也要能画：系列数据为空数组、坐标轴保留 —— 页面会先显示「还没有记录」，
 *     而不是让图表抛错。
 */

const AXIS_FONT = 10

/** 距形数值的紧凑写法：轴刻度上 12,345.67 会把整个图挤扁。 */
function compact(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 10000) return `${(n / 1000).toFixed(abs >= 100000 ? 0 : 1)}k`
  if (abs >= 1000) return n.toFixed(0)
  return String(Math.round(n))
}

function signColor(p: ChartPalette, v: number): string {
  if (v > 1e-6) return p.up
  if (v < -1e-6) return p.down
  return p.flat
}

/** 数值轴（带虚线网格）。`show: false` 用于「共享横轴的第二张子图」。 */
function valueAxis(p: ChartPalette, extra: Record<string, unknown> = {}) {
  return {
    type: 'value',
    axisLine: { show: false },
    axisTick: { show: false },
    // 4 段就够读了：格子再密，图里就只剩网格线
    splitNumber: 4,
    axisLabel: { color: p.faint, fontSize: AXIS_FONT, formatter: (v: number) => compact(v) },
    splitLine: { lineStyle: { color: p.line, type: 'dashed' } },
    ...extra,
  }
}

/** 类目轴（横轴）。 */
function categoryAxis(p: ChartPalette, extra: Record<string, unknown> = {}) {
  return {
    type: 'category',
    axisLine: { lineStyle: { color: p.line } },
    axisTick: { show: false },
    axisLabel: { color: p.faint, fontSize: AXIS_FONT },
    splitLine: { show: false },
    ...extra,
  }
}

function tooltipBase(p: ChartPalette) {
  return {
    backgroundColor: p.panel,
    borderColor: p.line,
    borderWidth: 1,
    padding: [6, 9] as [number, number],
    textStyle: { color: p.ink, fontSize: 11 },
    extraCssText: 'box-shadow:0 6px 20px rgba(0,0,0,.18);border-radius:4px;',
  }
}

/** 表格/提示里统一的「一笔」摘要：时间 · 方向手数 · 盈亏。 */
function rowLine(r: TradeRow, p: ChartPalette): string {
  const s = r.rec.stats
  const dir = s.side === 'long' ? '多' : s.side === 'short' ? '空' : '—'
  return `<div style="display:flex;gap:8px;justify-content:space-between">
    <span style="color:${p.dim}">${r.time} · ${dir} ${s.quantity}</span>
    <b style="color:${signColor(p, s.realizedPnl)}">${pnlText(s.realizedPnl)}</b>
  </div>`
}

// ---------------- 1. 累计盈亏 + 回撤 ----------------

/**
 * 上：累计已实现盈亏曲线（面积）；下：相对峰值的回撤（负值）。
 *
 * 为什么把回撤单独放一张子图而不是叠在曲线上：回撤是「距离峰值多远」，
 * 和「累计多少」共用一根纵轴时，回撤会被累计的绝对值压成一条贴着 0 的线；
 * 分轴之后两条曲线的形状才都看得清。
 */
export function equityOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  const x = a.rows.map((r) => r.time)
  const cum = a.rows.map((r) => r.cum)
  const dd = a.rows.map((r) => r.dd)
  const last = cum.length ? cum[cum.length - 1] : 0
  const tone = signColor(p, last)

  return {
    animationDuration: 420,
    grid: [
      { left: 48, right: 16, top: 18, height: '52%' },
      { left: 48, right: 16, top: '70%', height: '18%' },
    ],
    tooltip: {
      trigger: 'axis',
      ...tooltipBase(p),
      axisPointer: { type: 'line', lineStyle: { color: p.faint, type: 'dashed' } },
      formatter: (params: { dataIndex: number }[]) => {
        const r = a.rows[params[0]?.dataIndex ?? 0]
        if (!r) return ''
        return `<div style="margin-bottom:3px;color:${p.dim}">第 ${r.seq} 笔 · ${r.time}</div>${rowLine(
          r,
          p,
        )}<div style="color:${p.dim}">累计 <b style="color:${signColor(p, r.cum)}">${moneyText(
          r.cum,
        )}</b> · 回撤 <b style="color:${p.down}">${moneyText(r.dd)}</b></div>`
      },
    },
    xAxis: [
      categoryAxis(p, { data: x, gridIndex: 0, axisLabel: { show: false }, boundaryGap: false }),
      categoryAxis(p, {
        data: x,
        gridIndex: 1,
        boundaryGap: false,
        axisLabel: { color: p.faint, fontSize: 9, hideOverlap: true },
      }),
    ],
    yAxis: [
      valueAxis(p, { gridIndex: 0, scale: true }),
      // 回撤子图不画刻度：它就几十像素高，刻度只会叠成一团字。
      // 「回撤了多少」卡片标题和 tooltip 里都有，这里只需要形状
      valueAxis(p, {
        gridIndex: 1,
        splitLine: { show: false },
        axisLabel: { show: false },
      }),
    ],
    series: [
      {
        name: '累计盈亏',
        type: 'line',
        data: cum,
        smooth: false,
        showSymbol: a.count <= 60,
        symbolSize: 4,
        lineStyle: { width: 2, color: tone },
        itemStyle: { color: tone },
        areaStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 0,
            y2: 1,
            colorStops: [
              { offset: 0, color: tone === p.up ? p.upSoft : p.downSoft },
              { offset: 1, color: 'rgba(0,0,0,0)' },
            ],
          },
        },
        markLine: {
          symbol: 'none',
          silent: true,
          lineStyle: { color: p.line, type: 'dashed' },
          label: { show: false },
          data: [{ yAxis: 0 }],
        },
        // 最大回撤那一段用一块底色标出来：数字卡片上已经有了，图里要的是「哪一段」。
        // 只有一笔的时候不画 —— 那会变成整块底色，看起来像渲染错。
        markArea:
          a.ddFrom > 0 && a.ddTo > a.ddFrom + 1
            ? {
                silent: true,
                itemStyle: { color: p.upSoft, opacity: 0.5 },
                label: { show: false },
                data: [
                  [{ xAxis: a.rows[a.ddFrom - 1]?.time }, { xAxis: a.rows[a.ddTo - 1]?.time }],
                ],
              }
            : undefined,
      },
      {
        name: '回撤',
        type: 'line',
        xAxisIndex: 1,
        yAxisIndex: 1,
        data: dd,
        showSymbol: false,
        lineStyle: { width: 1, color: p.down },
        itemStyle: { color: p.down },
        areaStyle: { color: p.downSoft },
      },
    ],
  }
}

// ---------------- 2. 每笔盈亏 + 累计 ----------------

/**
 * 柱 = 每一笔的盈亏（红盈绿亏），线 = 到这一笔为止的累计（右轴）。
 *
 * 这张图回答的是「曲线是被哪一笔打下来的」：柱子和累计线同屏之后，
 * 一笔大亏后面跟着多少笔小赚才填回来，一眼就能数出来。
 */
export function perTradeOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  const x = a.rows.map((r) => `#${r.seq}`)
  const pnl = a.rows.map((r) => ({
    value: r.rec.stats.realizedPnl,
    itemStyle: { color: signColor(p, r.rec.stats.realizedPnl) },
  }))
  const cum = a.rows.map((r) => r.cum)

  return {
    animationDuration: 420,
    grid: { left: 48, right: 52, top: 26, bottom: 24 },
    legend: {
      top: 0,
      right: 0,
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: p.dim, fontSize: 10 },
      data: ['每笔盈亏', '累计'],
    },
    tooltip: {
      trigger: 'axis',
      ...tooltipBase(p),
      axisPointer: { type: 'shadow', shadowStyle: { color: p.line, opacity: 0.25 } },
      formatter: (params: { dataIndex: number }[]) => {
        const r = a.rows[params[0]?.dataIndex ?? 0]
        if (!r) return ''
        const s = r.rec.stats
        return `<div style="margin-bottom:3px;color:${p.dim}">第 ${r.seq} 笔 · ${r.time}</div>${rowLine(
          r,
          p,
        )}<div style="color:${p.dim}">持仓 ${s.barsHeld} 根 · 成交 ${s.fills} 次 · 累计 <b style="color:${signColor(
          p,
          r.cum,
        )}">${moneyText(r.cum)}</b></div>`
      },
    },
    xAxis: categoryAxis(p, {
      data: x,
      axisLabel: { color: p.faint, fontSize: 9, hideOverlap: true },
    }),
    // 两根轴都不写轴名：图例已经说明哪根柱子、哪根线是什么了，轴名只会挤掉刻度
    yAxis: [valueAxis(p), valueAxis(p, { splitLine: { show: false } })],
    series: [
      {
        name: '每笔盈亏',
        type: 'bar',
        data: pnl,
        barMaxWidth: 18,
        markLine: {
          symbol: 'none',
          silent: true,
          lineStyle: { color: p.line, type: 'dashed' },
          label: { show: false },
          data: [{ yAxis: 0 }],
        },
        markPoint:
          a.count > 1
            ? {
                symbol: 'circle',
                symbolSize: 5,
                silent: true,
                label: {
                  fontSize: 9,
                  color: p.dim,
                  offset: [0, -9],
                  formatter: (v: { name?: string }) => v.name ?? '',
                },
                data: [
                  { type: 'max', name: '最佳', itemStyle: { color: p.up } },
                  { type: 'min', name: '最差', itemStyle: { color: p.down } },
                ],
              }
            : undefined,
      },
      {
        name: '累计',
        type: 'line',
        yAxisIndex: 1,
        data: cum,
        showSymbol: false,
        smooth: false,
        lineStyle: { width: 1.6, color: p.accent },
        itemStyle: { color: p.accent },
        z: 3,
      },
    ],
  }
}

// ---------------- 3. 胜率环形 ----------------

/**
 * 胜率 + 盈亏笔数构成。中心的数字是**已平仓**交易的胜率 ——
 * 还在持仓中的那几笔不参与胜负判定（它们还没有结果），只在副标题里报个数。
 */
export function winRateOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  const data = [
    { name: '盈利', value: a.wins, itemStyle: { color: p.up } },
    { name: '亏损', value: a.losses, itemStyle: { color: p.down } },
    { name: '打平', value: a.flats, itemStyle: { color: p.flat } },
  ].filter((d) => d.value > 0)

  return {
    animationDuration: 420,
    title: {
      text: a.closed ? pctText(a.winRate) : '—',
      subtext: a.count ? `${a.wins} 胜 / ${a.losses} 负 · 共 ${a.count} 笔` : '还没有记录',
      left: 'center',
      top: '38%',
      textStyle: { color: signColor(p, a.totalPnl), fontSize: 22, fontWeight: 600 },
      subtextStyle: { color: p.faint, fontSize: 10 },
    },
    tooltip: {
      ...tooltipBase(p),
      formatter: (v: { name: string; value: number }) =>
        `${v.name} <b>${v.value}</b> 笔${a.closed ? ` · ${pctText(v.value / a.count)}` : ''}`,
    },
    legend: {
      bottom: 0,
      itemWidth: 8,
      itemHeight: 8,
      icon: 'circle',
      textStyle: { color: p.dim, fontSize: 10 },
    },
    series: [
      {
        type: 'pie',
        radius: ['58%', '80%'],
        center: ['50%', '46%'],
        avoidLabelOverlap: true,
        label: { show: false },
        labelLine: { show: false },
        itemStyle: { borderColor: p.panel, borderWidth: 2 },
        // 空数据时给一个灰环：比一片空白更像「还没交易」，而不是「图挂了」
        data: data.length ? data : [{ name: '暂无', value: 1, itemStyle: { color: p.line } }],
      },
    ],
  }
}

// ---------------- 4. 盈亏分布直方图 ----------------

/**
 * 盈亏落在哪些区间。看的是**形状**：集中在 0 附近 = 多数是小赢小亏；
 * 右侧一根长尾 = 少数大赢撑起全部利润（这种分布下胜率低是正常的）。
 */
export function histogramOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  // 类目轴只写区间左边界（-300 / -200 / …）：完整区间放不下，横轴会糊成一片
  const x = a.histogram.map((b) => b.tick)
  const data = a.histogram.map((b) => ({
    value: b.count,
    itemStyle: { color: b.mid >= 0 ? p.up : p.down, opacity: 0.85 },
  }))

  return {
    animationDuration: 420,
    grid: { left: 34, right: 12, top: 18, bottom: 40 },
    tooltip: {
      ...tooltipBase(p),
      formatter: (v: { dataIndex: number }) => {
        const b = a.histogram[v.dataIndex]
        if (!b) return ''
        return `${b.label}<br/><b>${b.count}</b> 笔${
          a.count ? ` · ${pctText(b.count / a.count)}` : ''
        }`
      },
    },
    xAxis: categoryAxis(p, {
      data: x,
      name: '盈亏区间',
      nameLocation: 'middle',
      nameGap: 24,
      nameTextStyle: { color: p.faint, fontSize: 9 },
      axisLabel: { color: p.faint, fontSize: 9, hideOverlap: true },
    }),
    yAxis: valueAxis(p, { minInterval: 1 }),
    series: [
      {
        type: 'bar',
        data,
        barMaxWidth: 26,
        barCategoryGap: '18%',
        label: { show: true, position: 'top', color: p.dim, fontSize: 9 },
      },
    ],
  }
}

// ---------------- 5. 持仓根数 × 盈亏 ----------------

/**
 * 每一笔一个点：横轴持仓多少根，纵轴盈亏多少。
 *
 * 这张图专门用来回答「拿得久是不是更容易赚」。点的大小按 |盈亏| 放大，
 * 交叉的两条参考线（0 盈亏、平均持仓）把平面分成四块：
 * 左上「久拿但亏」、右下「快进快出反而赚」这类结论，看是哪一块点多。
 */
export function holdingOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  const data = a.rows.map((r) => ({
    value: [r.rec.stats.barsHeld, r.rec.stats.realizedPnl],
    symbolSize: Math.max(6, Math.min(20, 4 + Math.sqrt(Math.abs(r.rec.stats.realizedPnl)))),
    itemStyle: { color: signColor(p, r.rec.stats.realizedPnl), opacity: 0.8 },
    row: r,
  }))

  return {
    animationDuration: 420,
    grid: { left: 52, right: 18, top: 22, bottom: 34 },
    tooltip: {
      ...tooltipBase(p),
      formatter: (v: { data: { row: TradeRow } }) => {
        const r = v.data.row
        const s = r.rec.stats
        return `<div style="margin-bottom:3px;color:${p.dim}">第 ${r.seq} 笔 · ${r.time}</div>${rowLine(
          r,
          p,
        )}<div style="color:${p.dim}">持仓 ${s.barsHeld} 根 · MFE <b style="color:${p.up}">${pnlText(
          s.mfe,
        )}</b> / MAE <b style="color:${p.down}">${pnlText(s.mae)}</b></div>`
      },
    },
    xAxis: valueAxis(p, {
      name: '持仓根数',
      nameLocation: 'middle',
      nameGap: 22,
      nameTextStyle: { color: p.faint, fontSize: 9 },
      minInterval: 1,
      scale: true,
    }),
    yAxis: valueAxis(p, { name: '盈亏', nameTextStyle: { color: p.faint, fontSize: 9 } }),
    series: [
      {
        type: 'scatter',
        data,
        markLine: {
          symbol: 'none',
          silent: true,
          lineStyle: { color: p.line, type: 'dashed' },
          label: { color: p.faint, fontSize: 9 },
          data: [
            { yAxis: 0, label: { show: false } },
            {
              xAxis: a.avgBars,
              label: { formatter: '平均持仓', position: 'end', rotate: 0, padding: [0, 2, 0, 2] },
            },
          ],
        },
      },
    ],
  }
}

// ---------------- 6. 多空对比 ----------------

/**
 * 多头 / 空头各自的累计盈亏与平均每笔。
 *
 * 两个系列都是「钱」，可以共用一根轴；笔数和胜率放进 tooltip 与柱顶标签里 ——
 * 把「笔数」也画成柱子的话，一根 30 和一根 300 的柱子并排，反而没人看盈亏了。
 */
export function sideOption(a: RecordAggregate, p: ChartPalette): EChartsCoreOption {
  const sides = [
    { name: '多头', s: a.long },
    { name: '空头', s: a.short },
  ]
  return {
    animationDuration: 420,
    grid: { left: 48, right: 16, top: 26, bottom: 24 },
    legend: {
      top: 0,
      right: 0,
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: p.dim, fontSize: 10 },
    },
    tooltip: {
      trigger: 'axis',
      ...tooltipBase(p),
      axisPointer: { type: 'shadow', shadowStyle: { color: p.line, opacity: 0.25 } },
      formatter: (params: { dataIndex: number }[]) => {
        const side = sides[params[0]?.dataIndex ?? 0]
        if (!side) return ''
        const s = side.s
        return `<div style="margin-bottom:3px;color:${p.dim}">${side.name}</div>
          <div>累计盈亏 <b style="color:${signColor(p, s.pnl)}">${moneyText(s.pnl)}</b></div>
          <div style="color:${p.dim}">${s.count} 笔 · 胜 ${s.wins} / 负 ${s.losses} · 平均持仓 ${s.avgBars} 根</div>`
      },
    },
    xAxis: categoryAxis(p, {
      data: sides.map((x) => x.name),
      axisLabel: { color: p.dim, fontSize: 11 },
    }),
    yAxis: valueAxis(p),
    series: [
      {
        name: '累计盈亏',
        type: 'bar',
        barMaxWidth: 40,
        data: sides.map((x) => ({
          value: x.s.pnl,
          itemStyle: { color: signColor(p, x.s.pnl) },
        })),
        markLine: {
          symbol: 'none',
          silent: true,
          lineStyle: { color: p.line, type: 'dashed' },
          label: { show: false },
          data: [{ yAxis: 0 }],
        },
      },
      {
        name: '平均每笔',
        type: 'bar',
        barMaxWidth: 40,
        data: sides.map((x) => ({
          value: x.s.avg,
          itemStyle: { color: signColor(p, x.s.avg), opacity: 0.45 },
        })),
      },
    ],
  }
}
