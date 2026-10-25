<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import { TOOL_LABEL, useReplayStore, type ChartTool } from '@/stores/replay'
import { ACTION } from '@/types/protocol'
import type {
  AnnotationClearedPayload,
  AnnotationPayload,
  BarAdvancedPayload,
  BotPayload,
  ControlChangedPayload,
  FlatAllPayload,
  MembersChangedPayload,
  OrderFilledPayload,
  OrdersPayload,
  ReplayEvent,
} from '@/types/protocol'

/**
 * 操作记录 = 后端广播的原始流水。
 *
 * 它存在的理由不只是「日志」：需求要求所有操作经后端处理再广播，以便
 * 「多用户有序、演示观看、后端回溯」。这一栏就是那条广播流的可视化 ——
 * 看到的是**服务端认为发生过什么**，不是浏览器自己记得什么。
 */

const store = useReplayStore()
const box = ref<HTMLDivElement | null>(null)

const MAX_ROWS = 400
const rows = computed(() => store.log.slice(-MAX_ROWS))

watch(
  () => store.log.length,
  async () => {
    await nextTick()
    const el = box.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

function clock(at: number): string {
  const d = new Date(at * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function actionLabel(action: string): string {
  switch (action) {
    case ACTION.barAdvanced:
      return '步进'
    case ACTION.annotationAdded:
      return '加标注'
    case ACTION.annotationMoved:
      return '移标注'
    case ACTION.annotationStyled:
      return '换样式'
    case ACTION.annotationDeleted:
      return '删标注'
    case ACTION.annotationCleared:
      return '清空绘图'
    case ACTION.orderPlaced:
      return '挂单'
    case ACTION.orderFilled:
      return '市价成交'
    case ACTION.orderCancelled:
      return '撤单'
    case ACTION.flatAll:
      return '一键退仓'
    case ACTION.controlChanged:
      return '操作权'
    case ACTION.membersChanged:
      return '成员变化'
    case ACTION.botStarted:
      return '机器人启动'
    case ACTION.botStopped:
      return '机器人停止'
    default:
      return action
  }
}

function kindLabel(kind: string): string {
  if (kind === 'limit') return '限价'
  if (kind === 'market') return '市价'
  return '止损'
}

function detail(ev: ReplayEvent): string {
  switch (ev.action) {
    case ACTION.barAdvanced: {
      const p = ev.payload as BarAdvancedPayload
      const fills = p.fills.length > 0 ? ` · 成交 ${p.fills.length} 笔` : ''
      return `${p.bar.time} → 第 ${p.cursor + 1} 根${fills}`
    }
    case ACTION.annotationAdded:
    case ACTION.annotationMoved:
    case ACTION.annotationStyled: {
      const p = ev.payload as AnnotationPayload
      const a = p.annotation
      // 类型名只在 store 的 TOOL_LABEL 里写一次；这里遇到不认识的 kind 就原样显示
      const kind = TOOL_LABEL[a.kind as ChartTool] ?? a.kind
      const marks = [a.snapped ? '已吸附' : '']
      // 换样式的那一条要能一眼看出「换成了什么颜色/线型」，否则日志里只有个编号
      if (ev.action === ACTION.annotationStyled) {
        marks.push(`样式 ${a.style?.color ?? ''} ${a.style?.width ?? ''}px ${a.style?.dash === 'dashed' ? '虚线' : '实线'}`)
      }
      return `${kind} ${a.id}${marks.filter(Boolean).map((m) => ` · ${m}`).join('')}`
    }
    case ACTION.annotationDeleted: {
      const p = ev.payload as { annotationId: string }
      return p.annotationId
    }
    case ACTION.annotationCleared: {
      const p = ev.payload as AnnotationClearedPayload
      // 编号可能几十个，列表里只写数量；鼠标停上去看具体删了哪几个
      return `移除 ${p.count} 个：${p.annotationIds.join('、')}`
    }
    case ACTION.orderPlaced:
    case ACTION.orderCancelled: {
      const p = ev.payload as OrdersPayload
      const last = p.orders[p.orders.length - 1]
      if (!last) return ''
      return `${last.side === 'buy' ? '买' : '卖'}${kindLabel(last.kind)} ${last.price.toFixed(store.priceDecimals)} × ${last.quantity} (${last.status})`
    }
    case ACTION.orderFilled: {
      const p = ev.payload as OrderFilledPayload
      const sign = p.order.side === 'buy' ? '买' : '卖'
      const pnl = p.trade.realizedPnl !== 0 ? ` · 实现 ${formatSigned(p.trade.realizedPnl)}` : ''
      // nATR 定手数的单会顺手挂一张止损：日志里必须写出来，否则「这一笔有没有保护」
      // 在复盘时完全看不出来（挂单列表里那张也看不出是跟着这一笔来的）
      const stop = p.stopOrder
        ? ` · 止损 ${p.stopOrder.price.toFixed(store.priceDecimals)} × ${p.stopOrder.quantity}`
        : ''
      return `${sign}市价 ${p.trade.price.toFixed(store.priceDecimals)} × ${p.trade.quantity}${pnl}${stop}`
    }
    case ACTION.flatAll: {
      // 一句话说清它到底做了什么：可能只撤了单，也可能既平仓又撤单
      const p = ev.payload as FlatAllPayload
      const parts: string[] = []
      if (p.trade) {
        parts.push(
          `${p.trade.side === 'buy' ? '买' : '卖'}市价平 ${p.trade.quantity} 手 @ ${p.trade.price.toFixed(
            store.priceDecimals,
          )}`,
        )
      }
      if (p.cancelled > 0) parts.push(`撤销 ${p.cancelled} 张挂单`)
      return parts.join(' · ') || '已空仓'
    }
    case ACTION.controlChanged: {
      const p = ev.payload as ControlChangedPayload
      return p.operator ? `${p.operator} 拿到操作权` : '操作权已交还'
    }
    case ACTION.membersChanged: {
      const p = ev.payload as MembersChangedPayload
      return p.members.map((m) => m.name).join('、')
    }
    case ACTION.botStarted: {
      const p = ev.payload as BotPayload
      // 策略机器人：写清跑的是哪个策略、每笔几手、推进节奏
      if (p.strategy) {
        return `${p.name || p.strategy} · 每笔 ${p.quantity} 手 · 每 ${p.intervalMs}ms 推进一根`
      }
      return `随机演示 · 每 ${p.intervalMs}ms 动作一次`
    }
    case ACTION.botStopped: {
      const p = ev.payload as BotPayload | undefined
      return p?.name ? `${p.name} 已停止` : '已停止'
    }
    default:
      return ''
  }
}

function actionClass(action: string): string {
  if (action === ACTION.barAdvanced) return 'text-emerald-600'
  if (action.startsWith('annotation')) return 'text-amber-600'
  // 一键退仓不是 order_* 前缀，但它属于交易动作，和挂单/成交同色
  if (action.startsWith('order') || action === ACTION.flatAll) return 'text-sky-600'
  if (action === ACTION.controlChanged) return 'text-fuchsia-600'
  return 'text-dim'
}
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="flex items-center gap-2 px-2 py-1 text-[13px] text-faint">
      <span>服务端广播 · 共 {{ store.log.length }} 条</span>
      <span class="ml-auto">多用户按处理顺序广播</span>
    </div>
    <div ref="box" class="min-h-0 flex-1 overflow-auto px-2 font-mono text-[13px] leading-6">
      <div v-if="rows.length === 0" class="text-faint">
        还没有操作。步进一根、画一个标注或挂一张单试试。
      </div>
      <div v-for="ev in rows" :key="ev.seq" class="flex gap-2">
        <span class="w-8 shrink-0 text-right text-faint">{{ ev.seq }}</span>
        <span class="w-[4.5rem] shrink-0 text-faint">{{ clock(ev.at) }}</span>
        <span class="w-20 shrink-0 truncate text-dim" :title="ev.by">{{ ev.by }}</span>
        <span class="w-20 shrink-0" :class="actionClass(ev.action)">{{ actionLabel(ev.action) }}</span>
        <span class="min-w-0 flex-1 truncate text-dim" :title="detail(ev)">{{ detail(ev) }}</span>
      </div>
    </div>
  </div>
</template>
