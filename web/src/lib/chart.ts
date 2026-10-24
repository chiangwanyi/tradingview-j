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
 * 页面的两套色（浅色 / 深色）已经由 style.css 的 `[data-theme]` 定好了，
 * 图表再抄一遍就等于多了一个会忘记同步的地方。这里只在真正画图时读一次，
 * 所以切换主题后只要重算 option，颜色就跟着变。
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
  /** 盈 / 涨：国内习惯**红** */
  up: string
  /** 亏 / 跌：国内习惯**绿** */
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
  const dark = document.documentElement.dataset.theme === 'dark'
  return {
    ink: cssVar('--c-ink', dark ? '#e2e8f0' : '#0f172a'),
    dim: cssVar('--c-dim', dark ? '#94a3b8' : '#475569'),
    faint: cssVar('--c-faint', dark ? '#64748b' : '#64748b'),
    line: cssVar('--c-line', dark ? '#1e293b' : '#d7e0ea'),
    panel: cssVar('--c-panel', dark ? '#0e1626' : '#ffffff'),
    accent: cssVar('--c-accent', dark ? '#38bdf8' : '#0369a1'),
    // 红涨绿跌：和 K 线、交易记录列表里的盈亏配色保持同一套，整个程序只用这两种语义色
    up: dark ? '#f87171' : '#dc2626',
    down: dark ? '#34d399' : '#059669',
    flat: dark ? '#94a3b8' : '#64748b',
    upSoft: dark ? 'rgba(248,113,113,0.18)' : 'rgba(220,38,38,0.12)',
    downSoft: dark ? 'rgba(52,211,153,0.18)' : 'rgba(5,150,105,0.12)',
  }
}
