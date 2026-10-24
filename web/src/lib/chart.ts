import { BarChart, LineChart, PieChart, ScatterChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  MarkAreaComponent,
  MarkLineComponent,
  MarkPointComponent,
  TitleComponent,
  TooltipComponent,
} from 'echarts/components'
import * as echarts from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'

/**
 * ECharts 的**按需注册**入口。
 *
 * 为什么不 `import * as echarts from 'echarts'`：整包 1MB+，而这里只用四种图。
 * 只注册用得上的图表与组件，构建出来的体积能压到三分之一左右 —— 这个程序的
 * 前端是从本地 exe 旁边的 dist 目录现读的，快一点是用户能感觉到的。
 *
 * 加新图时**必须在这里 use**：忘了注册的表现是「图不报错、但一片空白」。
 */
echarts.use([
  LineChart,
  BarChart,
  PieChart,
  ScatterChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  TitleComponent,
  MarkLineComponent,
  MarkPointComponent,
  MarkAreaComponent,
  CanvasRenderer,
])

export default echarts

/**
 * 图表配色：**从 CSS 变量读**，而不是再写一份十六进制。
 *
 * 页面的语义色已经由 style.css 的 `:root` 定好了，图表再抄一遍就等于多了一个
 * 会忘记同步的地方。这里只在真正画图时读一次。
 */
export interface ChartPalette {
  /** 正文 / 数值 */
  ink: string
  /** 次要文字 */
  dim: string
  /** 轴标签 */
  faint: string
  /** 网格线 / 边框 */
  line: string
  /** 卡片底色（tooltip 底） */
  panel: string
  /** 强调色（主线、选中） */
  accent: string
  /** 盈 / 涨：美股习惯**绿** */
  up: string
  /** 亏 / 跌：美股习惯**红** */
  down: string
  /** 打平 / 中性 */
  flat: string
  upSoft: string
  downSoft: string
}

function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

export function chartPalette(): ChartPalette {
  return {
    ink: cssVar('--c-ink', '#0f172a'),
    dim: cssVar('--c-dim', '#475569'),
    faint: cssVar('--c-faint', '#64748b'),
    line: cssVar('--c-line', '#d7e0ea'),
    panel: cssVar('--c-panel', '#ffffff'),
    accent: cssVar('--c-accent', '#0369a1'),
    // 绿涨红跌：和 K 线（render.ts）、列表里的盈亏配色是同一套语义色 ——
    // 整个程序只有这两个方向色，不区分页面、组件、图表库。
    up: cssVar('--c-up', '#15803d'),
    down: cssVar('--c-down', '#b91c1c'),
    flat: '#64748b',
    upSoft: 'rgba(21, 128, 61, 0.12)',
    downSoft: 'rgba(185, 28, 28, 0.12)',
  }
}
