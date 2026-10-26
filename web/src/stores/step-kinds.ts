import { ACTION, STEP_AUDIO } from '@/types/protocol'

/**
 * 步骤类型的「中文名 + 颜色」。
 *
 * 一步 = 一次操作，一条交易记录会有几百格 —— 光靠位置看不出哪里是
 * 「换了一根 K 线」、哪里是「画了个图形」。所以按类型上色 + 给一句中文名，
 * 让整条时间轴能一眼扫出结构：一串蓝点 = 连点了几下【下一根】，中间夹着橙点
 * 就是边步进边下单。
 *
 * 颜色刻意用 Tailwind 的字面色值（不拼字符串）：JIT 只认源码里出现过的完整类名。
 */

/** 起点（开仓**之前**那一刻的盘面）。 */
export const STEP_BASE = 'base'

export function stepKindLabel(kind: string): string {
  switch (kind) {
    case STEP_BASE:
      return '入场前'
    case ACTION.barAdvanced:
      return '下一根'
    case ACTION.barExtended:
      return '加载K线'
    case ACTION.annotationAdded:
      return '绘制图形'
    case ACTION.annotationMoved:
      return '移动图形'
    case ACTION.annotationStyled:
      return '换样式'
    case ACTION.annotationDeleted:
      return '删除图形'
    case ACTION.annotationCleared:
      return '清空绘图'
    case ACTION.orderPlaced:
      return '挂单'
    case ACTION.orderFilled:
      return '成交'
    case ACTION.orderMoved:
      return '改价'
    case ACTION.orderCancelled:
      return '撤单'
    case ACTION.flatAll:
      return '一键退仓'
    case STEP_AUDIO:
      return '语音'
    default:
      return kind
  }
}

/** 步进条方块的配色（未选中 / 已走过 两种状态各一套，见 stepCellClass）。 */
export function stepKindClass(kind: string): string {
  switch (kind) {
    case STEP_BASE:
      return 'border-slate-400/70 bg-slate-400/20'
    case ACTION.barAdvanced:
      return 'border-sky-500/80 bg-sky-500/40'
    // 延伸出来的 K 线用更淡的青色：它和「当时真的点了下一根」是两回事，
    // 但都属于「盘面往前走了一格」，不该和画图/下单混成一色。
    case ACTION.barExtended:
      return 'border-cyan-400/70 bg-cyan-400/25'
    case ACTION.annotationAdded:
    case ACTION.annotationMoved:
    case ACTION.annotationStyled:
    case ACTION.annotationDeleted:
    case ACTION.annotationCleared:
      return 'border-emerald-500/80 bg-emerald-500/35'
    case ACTION.orderPlaced:
    case ACTION.orderFilled:
    case ACTION.orderMoved:
    case ACTION.orderCancelled:
    case ACTION.flatAll:
      return 'border-violet-500/80 bg-violet-500/35'
    case STEP_AUDIO:
      return 'border-amber-500 bg-amber-500/50'
    default:
      return 'border-line bg-elev'
  }
}

/**
 * 这个类型的步是不是「换了一根 K 线」—— 步进条在它前面画一条分隔，标出每根 K 线的边界。
 *
 * 延伸步（v0.27.0）也算：它就是「盘面又多了一根」，分隔线不画的话，
 * 回放末尾那几格会看起来像「还在同一根上做了几次操作」。
 */
export function isBarBoundary(kind: string): boolean {
  return kind === ACTION.barAdvanced || kind === ACTION.barExtended
}
